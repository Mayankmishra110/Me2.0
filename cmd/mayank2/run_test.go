package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"mayank2/internal/config"
	"mayank2/internal/db"
	"mayank2/internal/queue"
)

// testConfig builds a minimal-but-complete config.Config for runDaemon,
// without touching config.yaml/.env on disk: a loopback dashboard listen
// address (no Tailscale dependency), Builder disabled, and no LLM
// providers configured (nothing here should ever need network access).
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	dataDir := t.TempDir()
	return &config.Config{
		DataDir:   dataDir,
		ConfigDir: filepath.Join(dataDir, "config"),
		Queue: config.QueueConfig{
			HeavyWorkers: 1,
			LightWorkers: 2,
			NetWorkers:   1,
		},
		Telegram: config.TelegramConfig{
			DailySummaryAt: "22:30",
		},
		Dashboard: config.DashboardConfig{
			Listen: []string{"127.0.0.1:0"},
		},
		Content: config.ContentConfig{
			RetentionDays: 7,
		},
		Builder: config.BuilderConfig{
			Enabled: false,
		},
	}
}

// TestRunDaemon_integration is the ticket's required practical integration
// test: a real (temp, file-backed) SQLite DB, the whole cmdRun wiring
// (queue + every real handler + scheduler + http + telegram-disabled) built
// exactly as `mayank2 run` builds it, a throwaway job type registered and
// enqueued through the extraRegister hook, and proof it is actually picked
// up and completed by a worker — then a clean, non-leaking shutdown on
// context cancellation.
func TestRunDaemon_integration(t *testing.T) {
	t.Setenv("DASHBOARD_TOKEN", "test-dashboard-token")
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_USER_ID", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")

	cfg := testConfig(t)
	dbPath := filepath.Join(t.TempDir(), "run-integration.db")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	baseline := goroutineCountStable(t)

	ctx, cancel := context.WithCancel(context.Background())

	jobDone := make(chan struct{})
	extra := func(q *queue.Queue) {
		q.Register("test.noop", queue.ResourceLight, 3, func(_ context.Context, job queue.Job) (json.RawMessage, error) {
			close(jobDone)
			return json.RawMessage(`{"ok":true}`), nil
		})
		if _, err := q.Enqueue(context.Background(), "test.noop", map[string]string{"probe": "M2-116"}); err != nil {
			t.Errorf("enqueue test.noop: %v", err)
		}
	}

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- runDaemon(ctx, cfg, dbPath, log, extra)
	}()

	select {
	case <-jobDone:
		// picked up and completed by a worker.
	case err := <-runErrCh:
		t.Fatalf("runDaemon exited early before the test job ran: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("test.noop job was never picked up / completed")
	}

	// Confirm the job row itself is recorded succeeded (not just that our
	// handler ran) — open a second connection to the same file.
	verifyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open verify db: %v", err)
	}
	t.Cleanup(func() { _ = verifyDB.Close() })
	waitForJobStatus(t, verifyDB, "test.noop", "succeeded")

	// Now request shutdown and confirm the daemon drains cleanly.
	cancel()

	select {
	case err := <-runErrCh:
		if err != nil {
			t.Fatalf("runDaemon returned an error on shutdown: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("runDaemon did not shut down within 15s of context cancellation")
	}

	// Bounded-wait goroutine-leak check (no goleak dependency in this repo):
	// give background goroutines a moment to actually unwind, then require
	// we're back near the pre-run baseline instead of climbing with every
	// call to runDaemon.
	assertNoGoroutineLeak(t, baseline)
}

// TestRunDaemon_PublishInstagramWithR2Unconfigured_NoPanic is the M2-122
// regression test at the daemon level: before this ticket,
// registerPublishHandlers never assigned Instagram/Facebook/Pinterest's
// Presign field at all (left nil), and — separately — nothing in the repo
// ever built a real *storage.R2Client to wire in even when R2_* env keys
// were present, so every real publish attempt to those three platforms was
// guaranteed to fail. This drives a publish.instagram job through the exact
// wiring `mayank2 run` uses (runDaemon -> registerPublishHandlers ->
// buildPresigner) with R2_* and the Meta OAuth env explicitly unset, and
// confirms: no panic anywhere in the worker pool (queue.worker recovers
// handler panics per-job, but a wiring-level nil-interface panic would still
// surface as a hung/failed test here), the job reaches a terminal 'failed'
// state with a real error message (not silently dropped, not stuck
// 'publishing' forever), and runDaemon itself drains cleanly on shutdown —
// i.e. one broken publish job never takes the daemon down.
//
// Note: with Meta OAuth also unconfigured (the realistic "nothing set up
// yet" default), Instagram.Publish's own token() check fails before it ever
// reaches resolvePublicURL/Presign — so this test's specific failure reason
// is "oauth token unavailable", not an R2 message. That's fine for what
// this test proves (daemon-level: no panic, clean terminal failure,
// drains); internal/publish/presign_notconfigured_test.go covers the
// Presign-specific "R2 not configured" error message directly, with a
// valid token so execution actually reaches resolvePublicURL.
func TestRunDaemon_PublishInstagramWithR2Unconfigured_NoPanic(t *testing.T) {
	t.Setenv("DASHBOARD_TOKEN", "test-dashboard-token")
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_USER_ID", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")
	// R2 fully unconfigured (CONTEXT D24) — the exact condition the crash
	// risk this ticket fixes was reported under.
	t.Setenv("R2_ACCOUNT_ID", "")
	t.Setenv("R2_ACCESS_KEY_ID", "")
	t.Setenv("R2_SECRET_ACCESS_KEY", "")
	t.Setenv("R2_BUCKET", "")
	// Meta OAuth also unconfigured, so this is the realistic "nothing set
	// up yet" default state, not a hand-picked partial config.
	t.Setenv("META_CLIENT_ID", "")
	t.Setenv("META_CLIENT_SECRET", "")

	cfg := testConfig(t)
	dbPath := filepath.Join(t.TempDir(), "run-ig-r2.db")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	ctx, cancel := context.WithCancel(context.Background())

	const contentID, channelID, pubID = "c-ig-r2", "ch-ig-r2", "pub-ig-r2"
	const idemKey = "c-ig-r2:instagram:ig-biz-r2"

	// Seed + enqueue from inside extraRegister, not from a second connection
	// polling the DB file from outside: extraRegister is guaranteed by
	// runDaemon's own contract to run after db.Migrate and after every real
	// handler (including publish.instagram) is registered, but before
	// StartWorkers — so this is race-free (no "table doesn't exist yet" /
	// SQLITE_BUSY-under-load flake from a second connection racing
	// migration) and lets this enqueue through the *same* `q` that actually
	// has publish.instagram registered, via the real q.Enqueue rather than
	// a hand-built jobs-table INSERT.
	seeded := make(chan *sql.DB, 1)
	extra := func(q *queue.Queue) {
		conn, err := db.Open(context.Background(), dbPath)
		if err != nil {
			t.Errorf("open verify db: %v", err)
			seeded <- nil
			return
		}

		warmup := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339Nano)
		now := time.Now().UTC().Format(time.RFC3339Nano)
		execOrFail(t, conn, `
INSERT INTO channels (id, platform, handle, language, niche, account_ref, status, warmup_started_at)
VALUES (?, 'instagram', 'h', 'en', 'money', 'ig-biz-r2', 'active', ?)`, channelID, warmup)
		execOrFail(t, conn, `
INSERT INTO content_items (id, channel_id, kind, language, stage, created_at)
VALUES (?, ?, 'short', 'en', 'approved', ?)`, contentID, channelID, now)
		execOrFail(t, conn, `
INSERT INTO approvals (id, content_id, kind, summary, status, nonce, decided_at)
VALUES (?, ?, 'short', 'ok', 'approved', '', ?)`, "ap-"+contentID, contentID, now)
		execOrFail(t, conn, `
INSERT INTO publications (id, content_id, platform, account, scheduled_at, status, idempotency_key)
VALUES (?, ?, 'instagram', 'ig-biz-r2', ?, 'scheduled', ?)`, pubID, contentID, now, idemKey)

		if _, err := q.Enqueue(context.Background(), "publish.instagram", map[string]any{
			"publication_id":  pubID,
			"content_id":      contentID,
			"account":         "ig-biz-r2",
			"idempotency_key": idemKey,
			"video_path":      "renders/short.mp4",
			"description":     "M2-122 regression",
		}); err != nil {
			t.Errorf("enqueue publish.instagram: %v", err)
		}
		seeded <- conn
	}

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- runDaemon(ctx, cfg, dbPath, log, extra)
	}()

	var verifyDB *sql.DB
	select {
	case verifyDB = <-seeded:
		if verifyDB == nil {
			t.Fatal("extraRegister failed to open its verify db (see prior t.Errorf)")
		}
	case err := <-runErrCh:
		t.Fatalf("runDaemon exited early before extraRegister ran: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("extraRegister (seed+enqueue) never ran within 10s")
	}
	t.Cleanup(func() { _ = verifyDB.Close() })

	// Instagram.Publish marks the publications row 'failed' itself
	// (failPublication) the moment resolvePublicURL errors — independent of
	// the jobs table's own retry/backoff state (a plain, non-Permanent
	// handler error goes back to jobs.status='queued' with backoff, not
	// 'failed'/'dead', so this polls the publications row directly rather
	// than waiting on jobs to exhaust retries).
	var pubStatus, pubErr string
	deadline2 := time.Now().Add(10 * time.Second)
	for {
		err := verifyDB.QueryRow(`SELECT status, COALESCE(error,'') FROM publications WHERE id=?`, pubID).Scan(&pubStatus, &pubErr)
		if err == nil && pubStatus == "failed" {
			break
		}
		if time.Now().After(deadline2) {
			t.Fatalf("publications row never reached status='failed' (last status=%q, err=%v)", pubStatus, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if pubErr == "" {
		t.Fatal("want a non-empty publications.error explaining why, got empty")
	}
	t.Logf("publish.instagram failed cleanly as expected: %s", pubErr)

	cancel()
	select {
	case err := <-runErrCh:
		if err != nil {
			t.Fatalf("runDaemon returned an error on shutdown: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("runDaemon did not shut down within 15s of context cancellation")
	}
}

func execOrFail(t *testing.T, sqlDB *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := sqlDB.Exec(q, args...); err != nil {
		t.Fatalf("exec: %v\n%s", err, q)
	}
}

// goroutineCountStable returns a settled NumGoroutine() reading (a couple of
// GCs to let anything already-finishing actually finish) to use as a leak
// baseline.
func goroutineCountStable(t *testing.T) int {
	t.Helper()
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	return runtime.NumGoroutine()
}

// assertNoGoroutineLeak polls NumGoroutine() for up to a few seconds and
// fails only if it never comes back down near baseline — avoids flaking on
// goroutines that are mid-teardown rather than actually leaked.
func assertNoGoroutineLeak(t *testing.T, baseline int) {
	t.Helper()
	const slack = 3 // scheduler/http/db driver internals can lag teardown by a goroutine or two
	deadline := time.Now().Add(5 * time.Second)
	var last int
	for time.Now().Before(deadline) {
		runtime.GC()
		last = runtime.NumGoroutine()
		if last <= baseline+slack {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("possible goroutine leak: baseline=%d, still at %d after shutdown", baseline, last)
}

// specJobTypes is docs/SPEC.md §5's job-type list, kept as one literal here
// (rather than duplicated across every package that registers one) so a
// registration gap reopening — a type SPEC lists that cmd/mayank2/run.go
// stops registering — fails this test immediately instead of silently
// reappearing (exactly the class of gap M2-116 and M2-117 both closed).
//
// "approval.request" is deliberately excluded: internal/telegram.Bot (and
// therefore its RegisterHandlers call, which owns that job type) is only
// built when TELEGRAM_BOT_TOKEN is set (CONTEXT D24) — TestRunDaemon_integration
// already covers that conditional wiring by disabling it outright, and
// standing up a fake bot here would mean its Run() goroutine making real
// outbound calls to api.telegram.org in a test. That is an M2-116 concern,
// not a gap this ticket introduced.
var specJobTypes = []string{
	"scout.topics", "research.brief", "script.write", "compliance.script",
	"voice.tts", "visuals.fetch", "render.long", "render.short", "render.thumbnail",
	"compliance.final",
	"publish.youtube", "publish.instagram", "publish.facebook", "publish.x", "publish.pinterest", "publish.linkedin",
	"blog.draft", "blog.merge", "blog.repurpose", "blog.medium",
	"analytics.pull", "storage.cleanup", "summary.daily",
	"builder.plan", "builder.implement", "builder.audit", "builder.gate",
}

// TestRunDaemon_everySpecJobTypeRegistered is M2-117's acceptance test: every
// SPEC §5 job type (minus approval.request, see specJobTypes) must resolve
// through queue.Queue.Enqueue once runDaemon has finished registering
// handlers — including the six M2-117 added (research.brief, script.write,
// visuals.fetch, blog.draft, blog.merge, blog.repurpose) plus blog.medium
// (M2-121; not in docs/SPEC.md §5's own literal list — see
// internal/blog/medium.go's JobBlogMedium doc comment — but registered by
// the same registerBlogHandlers this test exercises, so it belongs here
// too). queue.Queue.Enqueue itself is the source of truth for "is this type
// registered" (it returns an explicit "job type not registered" error when
// not), so this test needs no access to the queue's unexported registration
// map. TELEGRAM_BOT_TOKEN is unset below, so blog.medium registers with a
// nil Sender (registerBlogHandlers' doc comment) — Enqueue only checks
// registration, not whether the job would actually succeed if run, so that
// nil Sender doesn't affect this assertion.
func TestRunDaemon_everySpecJobTypeRegistered(t *testing.T) {
	t.Setenv("DASHBOARD_TOKEN", "test-dashboard-token")
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_USER_ID", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")

	cfg := testConfig(t)
	// Enable the blog pipeline for this assertion only (CONTEXT D24: off by
	// default). NewDraft/NewMerge/NewDispatcher never touch the filesystem
	// or git at construction time, so a bare temp dir is enough to prove
	// registration without a real Mayankbuilt clone.
	cfg.Blog.RepoPath = t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "run-jobtypes.db")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type checkResult struct {
		jobType string
		err     error
	}
	checked := make(chan []checkResult, 1)
	extra := func(q *queue.Queue) {
		results := make([]checkResult, 0, len(specJobTypes))
		for _, jt := range specJobTypes {
			_, err := q.Enqueue(context.Background(), jt, nil)
			results = append(results, checkResult{jobType: jt, err: err})
		}
		checked <- results
	}

	runErrCh := make(chan error, 1)
	go func() { runErrCh <- runDaemon(ctx, cfg, dbPath, log, extra) }()

	var results []checkResult
	select {
	case results = <-checked:
	case err := <-runErrCh:
		t.Fatalf("runDaemon exited before the registration check ran: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for the registration check")
	}

	for _, r := range results {
		if r.err != nil && strings.Contains(r.err.Error(), "not registered") {
			t.Errorf("SPEC §5 job type %q has no registered handler: %v", r.jobType, r.err)
		}
	}

	// The extraRegister hook runs before StartWorkers/sched.Start/http
	// ListenAndServe (runDaemon's own startup order) — give the rest of
	// startup a moment to finish and reach the blocking <-ctx.Done() before
	// cancelling, so shutdown races a running daemon rather than one still
	// mid-startup (which would otherwise surface as a startup error, e.g.
	// scheduler.Start seeing an already-cancelled context).
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case err := <-runErrCh:
		if err != nil {
			t.Fatalf("runDaemon returned an error on shutdown: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("runDaemon did not shut down within 15s of context cancellation")
	}
}

// TestRunDaemon_scoutTopicsIsRealHandler is M2-119's acceptance test: proves
// the scheduler's old scout.topics placeholder (which always returned
// {"status":"placeholder","type":"scout.topics"} regardless of config) has
// been swapped for internal/content.Scout's real queue handler.
//
// It does this two ways, both without any network access:
//  1. No YOUTUBE_API_KEY set (the default here): the real handler's "not
//     configured" (CONTEXT D24) skip path returns a distinctly different,
//     reason-carrying result than the placeholder's fixed shape.
//  2. YOUTUBE_API_KEY set but ChannelsDir pointed at an empty directory (no
//     config/channels/*.yaml): the real handler runs its own
//     LoadChannels/SyncChannels logic and reports channels/topics_written/
//     errors counters — fields the placeholder never had — with zero
//     channels found, so still no outbound HTTP call is made.
func TestRunDaemon_scoutTopicsIsRealHandler(t *testing.T) {
	t.Setenv("DASHBOARD_TOKEN", "test-dashboard-token")
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_USER_ID", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")

	t.Run("not configured skips cleanly, not the old placeholder shape", func(t *testing.T) {
		t.Setenv("YOUTUBE_API_KEY", "")
		cfg := testConfig(t)
		cfg.Content.ChannelsDir = t.TempDir() // present but irrelevant: skip happens before it's read
		dbPath := filepath.Join(t.TempDir(), "run-scout-notconfigured.db")
		log := slog.New(slog.NewTextHandler(io.Discard, nil))

		ctx, cancel := context.WithCancel(context.Background())
		extra := func(q *queue.Queue) {
			if _, err := q.Enqueue(context.Background(), "scout.topics", map[string]string{"trigger": "cron"}); err != nil {
				t.Errorf("enqueue scout.topics: %v", err)
			}
		}
		runErrCh := make(chan error, 1)
		go func() { runErrCh <- runDaemon(ctx, cfg, dbPath, log, extra) }()

		verifyDB := waitForRunDB(t, dbPath)
		waitForJobStatus(t, verifyDB, "scout.topics", "succeeded")
		result := jobResult(t, verifyDB, "scout.topics")

		if strings.Contains(result, `"placeholder"`) {
			t.Fatalf("scout.topics still returned the scheduler placeholder shape: %s", result)
		}
		var parsed map[string]string
		if err := json.Unmarshal([]byte(result), &parsed); err != nil {
			t.Fatalf("scout.topics result not the expected skip shape: %s (%v)", result, err)
		}
		if parsed["status"] != "skipped" || parsed["reason"] != "not configured" {
			t.Fatalf("scout.topics result = %v, want status=skipped reason=\"not configured\"", parsed)
		}

		stopDaemon(t, cancel, runErrCh)
	})

	t.Run("configured: runs real LoadChannels/SyncChannels logic", func(t *testing.T) {
		t.Setenv("YOUTUBE_API_KEY", "test-key-not-a-real-credential")
		cfg := testConfig(t)
		cfg.Content.ChannelsDir = t.TempDir() // exists, but has no *.yaml -> 0 channels, no HTTP calls
		dbPath := filepath.Join(t.TempDir(), "run-scout-configured.db")
		log := slog.New(slog.NewTextHandler(io.Discard, nil))

		ctx, cancel := context.WithCancel(context.Background())
		extra := func(q *queue.Queue) {
			if _, err := q.Enqueue(context.Background(), "scout.topics", map[string]string{"trigger": "cron"}); err != nil {
				t.Errorf("enqueue scout.topics: %v", err)
			}
		}
		runErrCh := make(chan error, 1)
		go func() { runErrCh <- runDaemon(ctx, cfg, dbPath, log, extra) }()

		verifyDB := waitForRunDB(t, dbPath)
		waitForJobStatus(t, verifyDB, "scout.topics", "succeeded")
		result := jobResult(t, verifyDB, "scout.topics")

		if strings.Contains(result, `"placeholder"`) || strings.Contains(result, `"skipped"`) {
			t.Fatalf("scout.topics result looks like the placeholder or the not-configured skip: %s", result)
		}
		var parsed struct {
			Channels      int   `json:"channels"`
			TopicsWritten int   `json:"topics_written"`
			Errors        []any `json:"errors"`
		}
		if err := json.Unmarshal([]byte(result), &parsed); err != nil {
			t.Fatalf("scout.topics result not the real handler's shape: %s (%v)", result, err)
		}
		if parsed.Channels != 0 || parsed.TopicsWritten != 0 || len(parsed.Errors) != 0 {
			t.Fatalf("scout.topics result = %+v, want all zero (empty ChannelsDir)", parsed)
		}

		stopDaemon(t, cancel, runErrCh)
	})
}

func waitForRunDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var verifyDB *sql.DB
	for {
		db, err := sql.Open("sqlite", dbPath)
		if err == nil {
			if pingErr := db.PingContext(ctx); pingErr == nil {
				verifyDB = db
				break
			}
			_ = db.Close()
		}
		select {
		case <-ctx.Done():
			t.Fatalf("db at %s never became reachable: %v", dbPath, err)
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Cleanup(func() { _ = verifyDB.Close() })
	return verifyDB
}

func jobResult(t *testing.T, sqlDB *sql.DB, jobType string) string {
	t.Helper()
	var result sql.NullString
	if err := sqlDB.QueryRow(`SELECT result FROM jobs WHERE type=? ORDER BY created_at DESC LIMIT 1`, jobType).Scan(&result); err != nil {
		t.Fatalf("read result for job type %s: %v", jobType, err)
	}
	return result.String
}

func stopDaemon(t *testing.T, cancel context.CancelFunc, runErrCh chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-runErrCh:
		if err != nil {
			t.Fatalf("runDaemon returned an error on shutdown: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("runDaemon did not shut down within 15s of context cancellation")
	}
}

func waitForJobStatus(t *testing.T, sqlDB *sql.DB, jobType, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		var status string
		err := sqlDB.QueryRowContext(ctx, `SELECT status FROM jobs WHERE type=? ORDER BY created_at DESC LIMIT 1`, jobType).Scan(&status)
		if err == nil && status == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("job type %s never reached status %q (last err=%v, status=%q)", jobType, want, err, status)
		case <-time.After(50 * time.Millisecond):
		}
	}
}
