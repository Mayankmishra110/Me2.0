package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/oauth2"

	"mayank2/internal/analytics"
	"mayank2/internal/blog"
	"mayank2/internal/builder"
	"mayank2/internal/compliance"
	"mayank2/internal/config"
	"mayank2/internal/content"
	"mayank2/internal/db"
	"mayank2/internal/events"
	"mayank2/internal/httpapi"
	"mayank2/internal/llm"
	"mayank2/internal/media"
	"mayank2/internal/publish"
	"mayank2/internal/queue"
	"mayank2/internal/scheduler"
	"mayank2/internal/secrets"
	"mayank2/internal/storage"
	"mayank2/internal/telegram"
)

// cmdRun implements `mayank2 run` (M2-116): the one place that starts the
// whole daemon described in ARCHITECTURE §1 under a single cancellable
// context, so the process is the single thing Task Scheduler supervises
// (ARCHITECTURE §8) and Ctrl+C / service stop shuts everything down without
// abandoning in-flight work.
func cmdRun(ctx context.Context, args []string) int {
	configPath, envPath, dbPath, rest, err := parseMigrateFlags(args)
	if err == errHelp {
		fmt.Fprint(os.Stderr, usage)
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "run: %v\n", err)
		return 2
	}
	if len(rest) > 0 {
		fmt.Fprintf(os.Stderr, "run: unexpected args %v\n", rest)
		return 2
	}

	log := slog.Default()

	if err := config.LoadEnvFile(envPath); err != nil {
		log.Error("run: load env failed", "error", err)
		return 1
	}

	cfgPath := configPath
	if !fileExists(cfgPath) {
		example := filepath.Join(filepath.Dir(cfgPath), "config.example.yaml")
		if fileExists(example) {
			cfgPath = example
		}
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Error("run: load config failed", "error", err)
		return 1
	}
	if dbPath == "" {
		dbPath = db.DefaultPath(cfg.DataDir)
	}

	// One cancellable context for the whole process (ARCHITECTURE §1/§8):
	// SIGINT/SIGTERM (Ctrl+C, Windows service stop) cancels everything at
	// once instead of tearing subsystems down independently.
	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runDaemon(runCtx, cfg, dbPath, log); err != nil {
		log.Error("run: fatal startup/runtime error", "error", err)
		return 1
	}
	log.Info("run: clean shutdown")
	return 0
}

// runDaemon does the actual work of cmdRun, factored out so tests can drive
// it directly against a temp DB and an in-memory config without going
// through flag parsing / env-file loading / config.Load from disk.
//
// extraRegister, if given, runs after every real handler is registered but
// before StartWorkers — tests use it to register and enqueue a throwaway
// job type to prove the whole wiring (queue → workers → completion →
// graceful drain) actually works end to end, without depending on any
// platform credentials. Production callers (cmdRun) pass none.
func runDaemon(ctx context.Context, cfg *config.Config, dbPath string, log *slog.Logger, extraRegister ...func(*queue.Queue)) error {
	sqlDB, err := db.Open(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("run: open db: %w", err)
	}
	defer sqlDB.Close()
	log.Info("run: db opened", "path", dbPath)

	applied, err := db.Migrate(ctx, sqlDB)
	if err != nil {
		return fmt.Errorf("run: migrate db: %w", err)
	}
	if len(applied) > 0 {
		log.Info("run: migrations applied", "migrations", applied)
	} else {
		log.Info("run: migrations up to date")
	}

	q := queue.New(sqlDB, queue.WithWorkerName("mayank2"), queue.WithLogger(log))

	// RecoverExpiredLeases must run before StartWorkers (its own doc
	// comment): requeue jobs a prior crash left claimed but unfinished.
	recovered, err := q.RecoverExpiredLeases(ctx)
	if err != nil {
		return fmt.Errorf("run: recover expired leases: %w", err)
	}
	log.Info("run: recovered expired leases", "count", recovered)

	// Shared infrastructure every handler group below is built from.
	bus := events.New(sqlDB)

	router, err := llm.New(cfg.LLM, llm.WithLogger(log))
	if err != nil {
		return fmt.Errorf("run: build llm router: %w", err)
	}

	layout, err := storage.NewLayout(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("run: build storage layout: %w", err)
	}

	secretsStore := secrets.NewStore(sqlDB)

	approvals := &content.ApprovalService{
		DB:          sqlDB,
		Enqueue:     q,
		Events:      bus,
		ChannelsDir: cfg.Content.ChannelsDir,
	}

	// Single point of registration (ticket AC): every real job handler is
	// registered exactly once, from here, before StartWorkers runs.
	if err := registerComplianceAndContent(q, sqlDB, router, approvals, layout, cfg, log); err != nil {
		return fmt.Errorf("run: register compliance/content handlers: %w", err)
	}
	registerPublishHandlers(q, sqlDB, secretsStore, log)
	if err := registerBlogHandlers(q, sqlDB, router, approvals, secretsStore, cfg, log); err != nil {
		return fmt.Errorf("run: register blog handlers: %w", err)
	}
	analyticsSvc := &analytics.Service{
		DB:     sqlDB,
		Tokens: analytics.TokenSourceFor(tokenSourceFor(secretsStore, "youtube", redirectURL())),
		Now:    time.Now,
		Log:    log,
	}
	analyticsSvc.RegisterHandler(q) // owns "analytics.pull" (M2-116 dup fix: scheduler no longer does)

	buildHandlers, err := registerBuilderHandlers(q, sqlDB, bus, router, approvals, cfg, log)
	if err != nil {
		return fmt.Errorf("run: register builder handlers: %w", err)
	}
	_ = buildHandlers

	// scheduler.RegisterHandlers owns summary.daily/storage.cleanup/scout.topics
	// (scout.topics stays a placeholder until M2-202 lands real content).
	sched, err := scheduler.New(sqlDB, q, scheduler.Config{
		Location:       cfg.Location,
		DailySummaryAt: cfg.Telegram.DailySummaryAt,
		DataDir:        cfg.DataDir,
	}, scheduler.WithLogger(log))
	if err != nil {
		return fmt.Errorf("run: build scheduler: %w", err)
	}
	sched.RegisterHandlers()

	for _, fn := range extraRegister {
		fn(q)
	}

	// queue.StartWorkers sized from config, not hardcoded.
	wg := q.StartWorkers(ctx, queue.WorkerPoolConfig{
		HeavyWorkers: cfg.Queue.HeavyWorkers,
		LightWorkers: cfg.Queue.LightWorkers,
		NetWorkers:   cfg.Queue.NetWorkers,
	})
	log.Info("run: workers started",
		"heavy", cfg.Queue.HeavyWorkers, "light", cfg.Queue.LightWorkers, "net", cfg.Queue.NetWorkers)

	if err := sched.Start(ctx); err != nil {
		return fmt.Errorf("run: start scheduler: %w", err)
	}
	log.Info("run: scheduler started")

	var extraWG sync.WaitGroup

	bot, err := buildTelegramBot(cfg, sqlDB, q, approvals, log)
	if err != nil {
		return fmt.Errorf("run: build telegram bot: %w", err)
	}
	if bot == nil {
		log.Info("run: telegram disabled (no TELEGRAM_BOT_TOKEN) — daemon stays up without it (CONTEXT D24)")
	} else {
		bot.RegisterHandlers(q) // owns "approval.request"
		extraWG.Add(1)
		go func() {
			defer extraWG.Done()
			if err := bot.Run(ctx); err != nil && ctx.Err() == nil {
				log.Error("run: telegram bot stopped unexpectedly", "error", err)
			}
		}()
		log.Info("run: telegram bot started")
	}

	dashboardToken := strings.TrimSpace(os.Getenv("DASHBOARD_TOKEN"))
	if dashboardToken == "" {
		return fmt.Errorf("run: DASHBOARD_TOKEN is required (set it in .env) — the dashboard is a core always-on component (ARCHITECTURE §1), not an optional integration")
	}
	httpSrv, err := httpapi.New(httpapi.Options{
		DashboardToken: dashboardToken,
		DB:             sqlDB,
		Events:         bus,
		Queue:          q,
		Approvals:      approvals,
		Version:        "dev",
		Logger:         log,
	})
	if err != nil {
		return fmt.Errorf("run: build http server: %w", err)
	}
	extraWG.Add(1)
	go func() {
		defer extraWG.Done()
		if err := httpSrv.ListenAndServe(ctx, cfg.Dashboard.Listen); err != nil && ctx.Err() == nil {
			log.Error("run: http server stopped unexpectedly", "error", err)
		}
	}()
	log.Info("run: http listening", "addrs", cfg.Dashboard.Listen)

	// Block until Ctrl+C / SIGTERM, then drain: stop cron, let in-flight
	// jobs finish (queue.runJob finalizes with a short detached context even
	// after ctx is cancelled — see internal/queue/worker.go), and let the
	// telegram/http goroutines observe ctx and shut themselves down.
	<-ctx.Done()
	log.Info("run: shutdown signal received, draining")

	sched.Stop()
	wg.Wait()
	extraWG.Wait()

	log.Info("run: drained, all subsystems stopped")
	return nil
}

// registerComplianceAndContent wires compliance.script, compliance.final,
// and the four content.Renderer heavy-pool job types.
func registerComplianceAndContent(
	q *queue.Queue, sqlDB *sql.DB, router *llm.Router, approvals *content.ApprovalService,
	layout *storage.Layout, cfg *config.Config, log *slog.Logger,
) error {
	eng := compliance.NewEngine(
		compliance.DefaultThresholds(),
		compliance.RouterEmbedder{Router: router},
		compliance.LLMClassifier{Completer: router},
	)
	eng.Store = &compliance.SQLFingerprintStore{DB: sqlDB}
	eng.Reports = &compliance.SQLReportSaver{DB: sqlDB}
	eng.Enqueue = q
	eng.Log = log
	eng.RegisterHandlers(q)

	finalEng := compliance.NewFinalEngine(&compliance.SQLReportSaver{DB: sqlDB}, approvals)
	finalEng.Log = log
	finalEng.RegisterHandlers(q)

	repoRoot := filepath.Dir(cfg.ConfigDir)
	tools := media.NewTools(filepath.Join(repoRoot, "remotion"), filepath.Join(repoRoot, "media-tools"))
	tools.Logger = log
	renderer := &content.Renderer{
		DB:            sqlDB,
		Layout:        layout,
		Tools:         tools,
		RetentionDays: cfg.Content.RetentionDays,
		Log:           log,
	}
	renderer.RegisterHandlers(q)

	// research.brief / script.write (M2-117): M2-203/M2-204's stages
	// (internal/content/research.go, script.go) existed with no queue
	// adapter until this ticket. TaskResearch/TaskScript both route through
	// config's llm.routes to free hosted tiers -> local Ollama last resort
	// (internal/llm/llm.go, ARCHITECTURE §6) — never Claude (CLAUDE.md
	// "Claude is for Builder and blog only"). Resource class: net (an LLM
	// API call / HTTP fetch, not a local model load or a render — CLAUDE.md's
	// "heavy" rule is specifically about those).
	researchDir, err := layout.Path(storage.KindWork, "research")
	if err != nil {
		return fmt.Errorf("run: research outdir: %w", err)
	}
	research, err := content.NewResearch(content.ResearchOptions{
		OutDir:    researchDir,
		Completer: router,
		DB:        sqlDB,
		Enqueue:   q,
		Logger:    log,
	})
	if err != nil {
		return fmt.Errorf("run: build research stage: %w", err)
	}
	research.RegisterHandler(q)

	scriptDir, err := layout.Path(storage.KindWork, "script")
	if err != nil {
		return fmt.Errorf("run: script outdir: %w", err)
	}
	script, err := content.NewScript(content.ScriptOptions{
		OutDir:    scriptDir,
		Completer: router,
		DB:        sqlDB,
		Enqueue:   q,
		Logger:    log,
	})
	if err != nil {
		return fmt.Errorf("run: build script stage: %w", err)
	}
	script.RegisterHandler(q)

	// visuals.fetch (M2-117): M2-207's Stock fetcher (internal/media/stock.go)
	// existed with no queue adapter. Stock is enabled only when
	// PEXELS_API_KEY/PIXABAY_API_KEY are set (D24); with neither key,
	// NewStockFromEnv returns *NotConfiguredError and the handler is still
	// registered (so an enqueued job fails cleanly at run time, not at
	// startup) with a nil Stock. Resource class: net (HTTP downloads).
	stock, err := media.NewStockFromEnv(os.LookupEnv, media.StockOptions{Logger: log})
	if err != nil {
		var notConfigured *media.NotConfiguredError
		if !errors.As(err, &notConfigured) {
			return fmt.Errorf("run: build stock fetcher: %w", err)
		}
		log.Info("run: visuals.fetch stock providers not configured (CONTEXT D24)", "missing_env", notConfigured.MissingEnv)
		stock = nil
	}
	visuals := &media.VisualsFetcher{
		DB:            sqlDB,
		Stock:         stock,
		Layout:        layout,
		RetentionDays: cfg.Content.RetentionDays,
		Log:           log,
	}
	visuals.RegisterHandler(q)

	return nil
}

// registerBlogHandlers wires blog.draft / blog.merge / blog.repurpose
// (M2-117; M2-401/402/403 built the stages with no daemon wiring — see
// tickets/M2-117.md). CONTEXT D24: when cfg.Blog.RepoPath is empty, none of
// these are registered — an enqueued blog.* job would fail loudly with
// "job type not registered" (queue.Queue.Enqueue) rather than the daemon
// silently no-op-ing every blog job forever, and nothing currently enqueues
// one automatically (no scheduler trigger fires blog.draft — it is manual,
// e.g. a future Telegram /blog command), so leaving it unregistered is safe.
func registerBlogHandlers(q *queue.Queue, sqlDB *sql.DB, router *llm.Router, approvals *content.ApprovalService, store *secrets.Store, cfg *config.Config, log *slog.Logger) error {
	if !cfg.Blog.Enabled() {
		log.Info("run: blog pipeline not configured (blog.repo_path empty) — blog.draft/blog.merge/blog.repurpose not registered (CONTEXT D24)")
		return nil
	}

	gitTimeout, err := time.ParseDuration(cfg.Blog.GitTimeout)
	if err != nil || cfg.Blog.GitTimeout == "" {
		gitTimeout = 2 * time.Minute
	}
	blogCfg := blog.Config{
		RepoPath:           cfg.Blog.RepoPath,
		PostsDir:           cfg.Blog.PostsDir,
		BaseBranch:         cfg.Blog.BaseBranch,
		Remote:             cfg.Blog.Remote,
		ChannelID:          cfg.Blog.ChannelID,
		SiteBaseURL:        cfg.Blog.SiteBaseURL,
		GitTimeout:         gitTimeout,
		PreviewURLTemplate: cfg.Blog.PreviewURLTemplate,
	}
	cred := blog.SecretsToken{Store: store, Platform: "mayankbuilt", Account: "git"}
	gitRepo := blog.NewLocalGitRepo(blogCfg, cred, nil)

	draft, err := blog.NewDraft(blog.DraftOptions{
		Config:      blogCfg,
		Completer:   router,
		Git:         gitRepo,
		DB:          sqlDB,
		Approvals:   approvals,
		BacklogPath: cfg.Blog.BacklogPath,
		Logger:      log,
	})
	if err != nil {
		return fmt.Errorf("build blog draft stage: %w", err)
	}
	q.Register(blog.JobDraft, queue.ResourceNet, 3, draft.Handler())

	merge, err := blog.NewMerge(blog.MergeOptions{
		Config:  blogCfg,
		Git:     gitRepo,
		DB:      sqlDB,
		Enqueue: q,
		Logger:  log,
	})
	if err != nil {
		return fmt.Errorf("build blog merge stage: %w", err)
	}
	q.Register(blog.JobMerge, queue.ResourceNet, 3, merge.Handler())

	// blog.repurpose (M2-117 dispatcher, internal/blog/repurpose.go): fans
	// out to the LinkedIn (M2-402) and X-personal (M2-403) legs, both of
	// which use llm.TaskBlog — Claude via claude_cli — the one deliberate
	// exception to "content tasks never use Claude" (CLAUDE.md, CONTEXT D13).
	linkedin, err := blog.NewRepurposer(blog.Options{Completer: router, Approver: approvals, Logger: log})
	if err != nil {
		return fmt.Errorf("build blog linkedin repurposer: %w", err)
	}
	xRepurposer := &blog.XRepurposer{
		DB:        sqlDB,
		LLM:       router,
		Approval:  approvals,
		ChannelID: cfg.Blog.ChannelID,
		Log:       log,
	}
	dispatcher, err := blog.NewDispatcher(blog.RepurposeOptions{
		DB:       sqlDB,
		LinkedIn: linkedin,
		X:        xRepurposer,
		Logger:   log,
	})
	if err != nil {
		return fmt.Errorf("build blog repurpose dispatcher: %w", err)
	}
	dispatcher.RegisterHandler(q)

	return nil
}

// registerPublishHandlers wires the five multi-platform publishers this
// ticket owns (publish.youtube / .facebook / .instagram / .x / .pinterest /
// .linkedin). Each publisher's OAuth token lookup is lazy (built from
// internal/secrets at publish time, not at startup) so a missing platform
// key switches that platform off cleanly at use time (CONTEXT D24) instead
// of blocking daemon startup.
func registerPublishHandlers(q *queue.Queue, sqlDB *sql.DB, store *secrets.Store, log *slog.Logger) {
	redirect := redirectURL()

	yt := &publish.YouTube{
		DB:     sqlDB,
		Tokens: publish.TokenSourceFor(tokenSourceFor(store, "youtube", redirect)),
		Log:    log,
	}
	yt.RegisterHandler(q)

	fb := &publish.Facebook{
		DB:     sqlDB,
		Tokens: publish.TokenSourceFor(tokenSourceFor(store, "meta", redirect)),
		Log:    log,
	}
	fb.RegisterHandler(q)

	ig := &publish.Instagram{
		DB:     sqlDB,
		Tokens: publish.TokenSourceFor(tokenSourceFor(store, "meta", redirect)),
		Log:    log,
	}
	ig.RegisterHandler(q)

	x := &publish.X{
		DB:     sqlDB,
		Tokens: publish.TokenSourceFor(tokenSourceFor(store, "x", redirect)),
		Log:    log,
	}
	x.RegisterHandler(q)

	pin := &publish.Pinterest{
		DB:     sqlDB,
		Tokens: publish.TokenSourceFor(tokenSourceFor(store, "pinterest", redirect)),
		Log:    log,
	}
	pin.RegisterHandler(q)

	li := &publish.LinkedIn{
		DB:     sqlDB,
		Tokens: publish.TokenSourceFor(tokenSourceFor(store, "linkedin", redirect)),
		Log:    log,
	}
	li.RegisterHandler(q)
}

// builderHandlers holds the four constructed builder components, mostly so
// tests (and future tickets that need to reach them, e.g. to Subscribe
// Auditor/Gate to the event bus) don't have to re-derive them.
type builderHandlers struct {
	Planner     *builder.Planner
	Implementer *builder.Implementer
	Auditor     *builder.Auditor
	Gate        *builder.Gate
}

// registerBuilderHandlers wires builder.plan/.implement/.audit/.gate
// (P5, internal/builder). Options are backed by config.Config.Builder per
// each constructor's own doc comment ("required even when Enabled is
// false so wiring stays honest; the handler no-ops before using them").
//
// Resource classes: builder.plan and builder.gate are quick LLM-call /
// git-status decisions (ResourceLight). builder.implement and
// builder.audit each run a `claude -p` subprocess for up to RunTimeout
// (default 60m) and are exactly the two threads D7 says run in parallel
// (Implementer building subphase N while Auditor audits N-1) — that's a
// concurrency-of-2 requirement, which matches queue.ResourceNet's default
// net_workers: 2 (config.example.yaml) exactly, whereas ResourceHeavy is
// capped at 1 worker by design (16 GB RAM / one local-model-or-render job
// at a time, CLAUDE.md) and would serialize them, contradicting D7. Claude
// Code calls Anthropic's API rather than loading a local model, so it does
// not fit CLAUDE.md's "heavy" definition either. Flagged in this ticket's
// Notes as a judgment call since neither ARCHITECTURE nor SPEC assigns
// resource classes to the builder job types explicitly.
func registerBuilderHandlers(
	q *queue.Queue, sqlDB *sql.DB, bus *events.Bus, router *llm.Router,
	approvals *content.ApprovalService, cfg *config.Config, log *slog.Logger,
) (*builderHandlers, error) {
	planner, err := builder.NewPlanner(builder.PlannerOptions{
		Enabled:   cfg.Builder.Enabled,
		Repos:     cfg.Builder.Repos,
		Completer: router,
		DB:        sqlDB,
		Approvals: approvals,
		Logger:    log,
	})
	if err != nil {
		return nil, fmt.Errorf("build planner: %w", err)
	}
	q.Register(builder.JobPlan, queue.ResourceLight, 3, planner.Handler())

	implementer, err := builder.NewImplementer(builder.ImplementerOptions{
		Enabled:          cfg.Builder.Enabled,
		Repos:            cfg.Builder.Repos,
		ImplementerModel: cfg.Builder.ImplementerModel,
		RunTimeout:       cfg.RunTimeout,
		DataDir:          cfg.DataDir,
		DB:               sqlDB,
		Events:           bus,
		Logger:           log,
	})
	if err != nil {
		return nil, fmt.Errorf("build implementer: %w", err)
	}
	q.Register(builder.JobImplement, queue.ResourceNet, 3, implementer.Handler())

	auditor, err := builder.NewAuditor(builder.AuditorOptions{
		Enabled:      cfg.Builder.Enabled,
		Repos:        cfg.Builder.Repos,
		AuditorModel: cfg.Builder.AuditorModel,
		RunTimeout:   cfg.RunTimeout,
		DataDir:      cfg.DataDir,
		DB:           sqlDB,
		Events:       bus,
		Logger:       log,
	})
	if err != nil {
		return nil, fmt.Errorf("build auditor: %w", err)
	}
	q.Register(builder.JobAudit, queue.ResourceNet, 3, auditor.Handler())

	gate, err := builder.NewGate(builder.GateOptions{
		Enabled:        cfg.Builder.Enabled,
		Repos:          cfg.Builder.Repos,
		MaxFixAttempts: cfg.Builder.MaxFixAttempts,
		RunTimeout:     cfg.RunTimeout,
		LimitPause:     cfg.LimitPause,
		DB:             sqlDB,
		Events:         bus,
		Enqueue:        q,
		Logger:         log,
	})
	if err != nil {
		return nil, fmt.Errorf("build gate: %w", err)
	}
	q.Register(builder.JobGate, queue.ResourceLight, 3, gate.Handler())

	return &builderHandlers{Planner: planner, Implementer: implementer, Auditor: auditor, Gate: gate}, nil
}

// buildTelegramBot constructs the bot with Approvals wired (telegram.NewFromEnv
// does not accept an Approvals field, so this replicates its env parsing —
// TELEGRAM_BOT_TOKEN/USER_ID/CHAT_ID — via telegram.New directly). Returns
// (nil, nil) when TELEGRAM_BOT_TOKEN is unset (CONTEXT D24: disabled, not
// fatal); returns a non-nil error only for a genuine config problem (token
// set but IDs missing/invalid).
func buildTelegramBot(cfg *config.Config, sqlDB *sql.DB, q *queue.Queue, approvals *content.ApprovalService, log *slog.Logger) (*telegram.Bot, error) {
	bot, err := telegram.New(telegram.Config{
		Token:     strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")),
		UserID:    envInt64("TELEGRAM_USER_ID"),
		ChatID:    envInt64("TELEGRAM_CHAT_ID"),
		DataRoot:  cfg.DataDir,
		DB:        sqlDB,
		Queue:     q,
		Approvals: approvals,
		Log:       log,
	})
	if errors.Is(err, telegram.ErrDisabled) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return bot, nil
}

func envInt64(key string) int64 {
	v, _ := strconv.ParseInt(strings.TrimSpace(os.Getenv(key)), 10, 64)
	return v
}

// redirectURL is the loopback URL `mayank2 auth` uses for the OAuth code
// flow (cmd/mayank2/auth.go); publish/analytics handlers need the same
// value to rebuild an oauth2.Config for refreshing a stored token.
func redirectURL() string {
	port := strings.TrimSpace(os.Getenv("OAUTH_REDIRECT_PORT"))
	if port == "" {
		port = "8912"
	}
	return fmt.Sprintf("http://127.0.0.1:%s/callback", port)
}

// tokenSourceFor builds a lazy oauth2.TokenSource factory for platform
// (e.g. "youtube", "meta", "x", "pinterest", "linkedin"): it only touches
// secrets.LookupPlatform/OAuthConfig/TokenSource when a publish/analytics
// handler actually runs, so a missing CLIENT_ID/SECRET env pair fails that
// one job cleanly instead of blocking daemon startup (CONTEXT D24).
func tokenSourceFor(store *secrets.Store, platform, redirect string) func(ctx context.Context, account string) (oauth2.TokenSource, error) {
	return func(ctx context.Context, account string) (oauth2.TokenSource, error) {
		plat, err := secrets.LookupPlatform(platform)
		if err != nil {
			return nil, err
		}
		oauthCfg, err := plat.OAuthConfig(redirect)
		if err != nil {
			return nil, err
		}
		return store.TokenSource(ctx, plat.Name, account, oauthCfg)
	}
}
