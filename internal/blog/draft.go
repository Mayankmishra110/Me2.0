// Package blog implements the blog pipeline into Mayank's canonical
// Mayankbuilt repo (ARCHITECTURE §3.2, M2-401, CONTEXT D19): blog.draft
// turns a backlog/manual topic into a full MDX post on a branch and
// requests approval; blog.merge (merge.go) fast-forwards that branch to
// main once approved. Claude (task "blog") is the one deliberate exception
// to "content tasks never use Claude" (CONTEXT D13, internal/llm router).
//
// # Real uncertainties this ticket flagged (tickets/M2-401.md Notes) and
// did NOT invent answers for:
//
//   - Mayankbuilt's repo URL / local clone location and remote name.
//   - The MDX posts directory and frontmatter field convention.
//   - How a Vercel preview URL is actually obtained (GitHub Deployment
//     Status API, a Vercel webhook, or polling the Vercel API).
//   - How the *approval decision* (content.ApprovalService.Decide) is
//     wired to enqueue blog.merge on approve, or blog.draft (with
//     redo_notes) on redo — Decide's current approve/redo branches
//     (internal/content/approval.go) are hardcoded to the video pipeline
//     (schedulePublications / script.write) and are outside this ticket's
//     touches (internal/blog/draft.go, internal/blog/merge.go only).
//
// All of the above are therefore configurable/injectable here rather than
// hardcoded or guessed, and blog.merge independently re-checks the F7
// approval gate every time it runs so it is safe to wire up from anywhere
// later.
package blog

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"time"

	"golang.org/x/oauth2"
	"gopkg.in/yaml.v3"

	"mayank2/internal/llm"
	"mayank2/internal/queue"
)

// Job types (SPEC §5).
const (
	JobDraft = "blog.draft"
	JobMerge = "blog.merge"
)

// Completer is the LLM surface Draft needs (satisfied by *llm.Router),
// mirroring internal/content's Completer pattern (research.go, script.go).
type Completer interface {
	Complete(ctx context.Context, task llm.Task, req llm.Request) (llm.Response, error)
}

// DB is the SQL surface this package needs, mirroring
// internal/content.ApprovalDB so *sql.DB (or a fake) satisfies it directly.
type DB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ApprovalStarter creates a pending approval and enqueues approval.request.
// *content.ApprovalService satisfies this exactly (approval.go's Start
// method) — passed in so this package does not import internal/content's
// video-scheduling logic, which blog posts don't use.
type ApprovalStarter interface {
	Start(ctx context.Context, contentID, kind, summary, previewPath string) (string, error)
}

// Config configures the Mayankbuilt repo location and MDX conventions.
// Every field with a "confirm with Mayank" note is a placeholder default,
// not a documented fact about his real repo (see package doc).
type Config struct {
	// RepoPath is the local clone of the Mayankbuilt repo. It must already
	// exist (this package does not clone from scratch) with a remote
	// configured. Location/URL unconfirmed.
	RepoPath string
	// PostsDir is the MDX posts directory inside the repo, e.g.
	// "content/blog". Convention unconfirmed; defaults to "content/blog".
	PostsDir string
	// BaseBranch is the branch merged into on approval. Default "main".
	BaseBranch string
	// Remote is the git remote name. Default "origin".
	Remote string
	// ChannelID is the channels.id row content_items.channel_id (NOT NULL
	// REFERENCES channels) points at for blog posts. The schema has no
	// "no channel" concept; blog content has no real channel. Mayank (or a
	// seed migration — out of this ticket's touches) must create a
	// channels row with this id. Default "blog".
	ChannelID string
	// SiteBaseURL is the public site base used only to compute the
	// canonical post URL recorded on the publications row, e.g.
	// "https://mayankbuilt.com". Optional.
	SiteBaseURL string
	// GitTimeout bounds each git child-process call. Default 2 minutes.
	GitTimeout time.Duration
	// FrontmatterExtra adds YAML frontmatter keys beyond the built-in
	// guess (title/date/slug/description/tags). The real required
	// frontmatter shape is unconfirmed (see package doc).
	FrontmatterExtra map[string]string
	// PreviewURLTemplate is a Go text/template (fields .Branch, .Slug)
	// used to GUESS a Vercel preview URL. Left empty by default because
	// the real mechanism is unconfirmed — when empty, blog.draft requests
	// approval with no preview link rather than a fabricated one.
	PreviewURLTemplate string
}

func (c Config) withDefaults() Config {
	if strings.TrimSpace(c.PostsDir) == "" {
		c.PostsDir = "content/blog"
	}
	if strings.TrimSpace(c.BaseBranch) == "" {
		c.BaseBranch = "main"
	}
	if strings.TrimSpace(c.Remote) == "" {
		c.Remote = "origin"
	}
	if strings.TrimSpace(c.ChannelID) == "" {
		c.ChannelID = "blog"
	}
	if c.GitTimeout <= 0 {
		c.GitTimeout = 2 * time.Minute
	}
	return c
}

// ---- Git operations, behind an interface (so tests use a temp git repo) ----

// GitRepo is the git surface Draft and Merge need against the local
// Mayankbuilt clone. Implementations must use fixed-argv child processes
// (never a shell string built from AI output — CLAUDE.md) and must never
// force-push.
type GitRepo interface {
	// Dir returns the local working tree path.
	Dir() string
	// Fetch updates remote-tracking refs (never touches local branches).
	Fetch(ctx context.Context) error
	// CreateBranch switches to branch, creating it from base (preferring
	// the remote-tracking ref after Fetch) if it doesn't exist yet. If the
	// branch already exists (a redo re-run), it switches onto it without
	// resetting history, so a later Push is always a fast-forward.
	CreateBranch(ctx context.Context, branch, base string) error
	// CommitAll stages paths (relative to Dir) and commits.
	CommitAll(ctx context.Context, paths []string, message string) error
	// Push pushes branch to the remote as branch:branch. Never forces.
	Push(ctx context.Context, branch string) error
	// AlreadyMerged reports whether branch is already an ancestor of base
	// (idempotency check for blog.merge).
	AlreadyMerged(ctx context.Context, branch, base string) (bool, error)
	// FastForwardMerge checks out base, fast-forwards it onto branch (git
	// merge --ff-only — refuses and errors on any non-fast-forward), and
	// pushes base. Never forces.
	FastForwardMerge(ctx context.Context, branch, base string) error
}

// GitRunner executes one git subprocess with a fixed argument list against
// dir. Tests inject a fake; production uses DefaultGitRunner
// (exec.CommandContext, never a shell).
type GitRunner func(ctx context.Context, dir string, args []string) (stdout, stderr []byte, err error)

// DefaultGitRunner is the production GitRunner.
func DefaultGitRunner(ctx context.Context, dir string, args []string) ([]byte, []byte, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil, fmt.Errorf("blog: git: empty working directory")
	}
	// Isolate from a global/shared core.hooksPath (this monorepo's hooks must
	// never run against the Mayankbuilt clone) and from GIT_DIR/GIT_WORK_TREE.
	fullArgs := append([]string{"-c", "core.hooksPath="}, args...)
	cmd := exec.CommandContext(ctx, "git", fullArgs...)
	cmd.Dir = dir
	cmd.Env = gitCleanEnv(os.Environ())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func gitCleanEnv(base []string) []string {
	out := make([]string, 0, len(base))
	for _, e := range base {
		upper := strings.ToUpper(e)
		if strings.HasPrefix(upper, "GIT_DIR=") ||
			strings.HasPrefix(upper, "GIT_WORK_TREE=") ||
			strings.HasPrefix(upper, "GIT_INDEX_FILE=") ||
			strings.HasPrefix(upper, "GIT_OBJECT_DIRECTORY=") ||
			strings.HasPrefix(upper, "GIT_ALTERNATE_OBJECT_DIRECTORIES=") {
			continue
		}
		out = append(out, e)
	}
	return out
}

// CredentialSource supplies the token used to authenticate git fetch/push
// to Mayankbuilt over HTTPS. The production implementation (SecretsToken)
// reads it from internal/secrets (DPAPI vault) — never .env plaintext —
// and this package never logs the value (see localGitRepo.exec).
type CredentialSource interface {
	Token(ctx context.Context) (string, error)
}

// NoCredential is for remotes that need no token injection: local/file
// remotes (tests) or an SSH remote authenticated outside this process.
type NoCredential struct{}

// Token implements CredentialSource.
func (NoCredential) Token(ctx context.Context) (string, error) { return "", nil }

// SecretsGetter is the internal/secrets.Store surface SecretsToken needs.
// Defined locally so this package does not otherwise depend on
// internal/secrets.
type SecretsGetter interface {
	Get(ctx context.Context, platform, account string) (*oauth2.Token, error)
}

// SecretsToken adapts an internal/secrets.Store-held token to a
// CredentialSource. Store the GitHub token the same way OAuth publisher
// tokens are stored (ARCHITECTURE §7's DPAPI-encrypted oauth_tokens table),
// e.g. secretsStore.Put(ctx, "mayankbuilt", "git", &oauth2.Token{AccessToken: pat}).
// This is not itself an OAuth flow — it reuses the same encrypted store for
// a plain personal access token.
type SecretsToken struct {
	Store    SecretsGetter
	Platform string // default "mayankbuilt"
	Account  string // default "git"
}

// Token implements CredentialSource.
func (s SecretsToken) Token(ctx context.Context) (string, error) {
	if s.Store == nil {
		return "", fmt.Errorf("blog: SecretsToken: Store is nil")
	}
	platform := orDefault(s.Platform, "mayankbuilt")
	account := orDefault(s.Account, "git")
	tok, err := s.Store.Get(ctx, platform, account)
	if err != nil {
		return "", fmt.Errorf("blog: load git credential %s/%s: %w", platform, account, err)
	}
	if tok == nil || tok.AccessToken == "" {
		return "", fmt.Errorf("blog: git credential %s/%s is empty", platform, account)
	}
	return tok.AccessToken, nil
}

// localGitRepo is the production GitRepo: a local clone driven by fixed
// git-argv child processes.
type localGitRepo struct {
	dir     string
	remote  string
	cred    CredentialSource
	run     GitRunner
	timeout time.Duration
}

// NewLocalGitRepo returns the production GitRepo for cfg.RepoPath. run and
// cred may be nil (defaults: DefaultGitRunner, NoCredential).
func NewLocalGitRepo(cfg Config, cred CredentialSource, run GitRunner) GitRepo {
	cfg = cfg.withDefaults()
	if strings.TrimSpace(cfg.RepoPath) == "" {
		// Never fall through to process cwd — that would let git ops touch
		// the Mayank2.0 checkout (seen under test pollution).
		panic("blog: NewLocalGitRepo: Config.RepoPath is required")
	}
	if run == nil {
		run = DefaultGitRunner
	}
	if cred == nil {
		cred = NoCredential{}
	}
	dir := cfg.RepoPath
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return &localGitRepo{dir: dir, remote: cfg.Remote, cred: cred, run: run, timeout: cfg.GitTimeout}
}

func (g *localGitRepo) Dir() string { return g.dir }

// runRaw runs git with no error wrapping/redaction — callers that need the
// raw *exec.ExitError (exit code) use this directly.
func (g *localGitRepo) runRaw(ctx context.Context, args ...string) (stdout, stderr []byte, err error) {
	cctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()
	return g.run(cctx, g.dir, args)
}

// exec runs git, wraps failures into an error, and redacts every value in
// secretsToRedact from both the rendered argv and the combined
// stdout+stderr before it ever reaches an error string, a log field, or an
// events row (CLAUDE.md: never log tokens).
func (g *localGitRepo) exec(ctx context.Context, secretsToRedact []string, args ...string) (string, error) {
	stdout, stderr, err := g.runRaw(ctx, args...)
	argsStr := strings.Join(args, " ")
	combined := strings.TrimSpace(string(stdout))
	if s := strings.TrimSpace(string(stderr)); s != "" {
		if combined != "" {
			combined += "\n"
		}
		combined += s
	}
	for _, secret := range secretsToRedact {
		if secret == "" {
			continue
		}
		argsStr = strings.ReplaceAll(argsStr, secret, "***")
		combined = strings.ReplaceAll(combined, secret, "***")
	}
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", argsStr, err, truncate(combined, 500))
	}
	return combined, nil
}

// authenticatedURL resolves the remote's URL and, for an http(s) remote,
// returns a copy with the credential token embedded (git accepts a URL as
// the push/fetch target directly, so the token is never written to
// .git/config). token is returned too so callers can pass it to exec's
// redaction list. A non-http(s) remote (ssh, file://, local path — tests)
// gets no credential.
func (g *localGitRepo) authenticatedURL(ctx context.Context) (rawURL, token string, err error) {
	base, err := g.exec(ctx, nil, "remote", "get-url", g.remote)
	if err != nil {
		return "", "", fmt.Errorf("blog: git remote get-url %s: %w", g.remote, err)
	}
	base = strings.TrimSpace(base)
	u, perr := url.Parse(base)
	if perr != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return base, "", nil
	}
	tok, err := g.cred.Token(ctx)
	if err != nil {
		return "", "", fmt.Errorf("blog: git credential: %w", err)
	}
	if tok == "" {
		return base, "", nil
	}
	u.User = url.UserPassword("x-access-token", tok)
	return u.String(), tok, nil
}

// Fetch implements GitRepo.
func (g *localGitRepo) Fetch(ctx context.Context) error {
	remoteURL, token, err := g.authenticatedURL(ctx)
	if err != nil {
		return fmt.Errorf("blog: git fetch: %w", err)
	}
	refspec := "+refs/heads/*:refs/remotes/" + g.remote + "/*"
	if _, err := g.exec(ctx, g.redactList(ctx, token), "fetch", remoteURL, refspec); err != nil {
		return fmt.Errorf("blog: git fetch: %w", err)
	}
	return nil
}

// CreateBranch implements GitRepo.
func (g *localGitRepo) CreateBranch(ctx context.Context, branch, base string) error {
	if _, _, err := g.runRaw(ctx, "rev-parse", "--verify", branch); err == nil {
		// Branch already exists locally (redo re-run): switch onto it
		// without resetting — history is preserved, so a later Push stays
		// a fast-forward and is never forced.
		if _, err := g.exec(ctx, nil, "checkout", branch); err != nil {
			return fmt.Errorf("blog: git checkout %s: %w", branch, err)
		}
		return nil
	}
	startPoint := g.remote + "/" + base
	if _, _, err := g.runRaw(ctx, "rev-parse", "--verify", startPoint); err != nil {
		startPoint = base
	}
	if _, err := g.exec(ctx, nil, "checkout", "-b", branch, startPoint); err != nil {
		return fmt.Errorf("blog: git checkout -b %s %s: %w", branch, startPoint, err)
	}
	return nil
}

// CommitAll implements GitRepo.
func (g *localGitRepo) CommitAll(ctx context.Context, paths []string, message string) error {
	if len(paths) == 0 {
		return fmt.Errorf("blog: git commit: no paths given")
	}
	addArgs := append([]string{"add", "--"}, paths...)
	if _, err := g.exec(ctx, nil, addArgs...); err != nil {
		return fmt.Errorf("blog: git add: %w", err)
	}
	// message may contain human-provided text (topic); exec.CommandContext
	// takes argv directly (no shell), so this is safe regardless of
	// content. The AI-generated post body itself only ever goes into the
	// file written by Draft.Run, never into a git argv.
	if _, err := g.exec(ctx, nil, "commit", "-m", message); err != nil {
		return fmt.Errorf("blog: git commit: %w", err)
	}
	return nil
}

// Push implements GitRepo.
func (g *localGitRepo) Push(ctx context.Context, branch string) error {
	remoteURL, token, err := g.authenticatedURL(ctx)
	if err != nil {
		return fmt.Errorf("blog: git push %s: %w", branch, err)
	}
	// Fixed refspec, no --force flag exists anywhere in this package.
	if _, err := g.exec(ctx, g.redactList(ctx, token), "push", remoteURL, branch+":"+branch); err != nil {
		return fmt.Errorf("blog: git push %s: %w", branch, err)
	}
	return nil
}

// redactList always includes any vault token so error strings never leak it,
// even when the remote is a local path / file:// (token unused for auth).
func (g *localGitRepo) redactList(ctx context.Context, already string) []string {
	out := make([]string, 0, 2)
	if already != "" {
		out = append(out, already)
	}
	if g.cred == nil {
		return out
	}
	tok, err := g.cred.Token(ctx)
	if err != nil || tok == "" || tok == already {
		return out
	}
	return append(out, tok)
}

// AlreadyMerged implements GitRepo.
func (g *localGitRepo) AlreadyMerged(ctx context.Context, branch, base string) (bool, error) {
	if _, _, err := g.runRaw(ctx, "rev-parse", "--verify", branch); err != nil {
		return false, nil // branch not known at all -> cannot be merged
	}
	baseRef := g.remote + "/" + base
	if _, _, err := g.runRaw(ctx, "rev-parse", "--verify", baseRef); err != nil {
		baseRef = base
	}
	_, stderr, err := g.runRaw(ctx, "merge-base", "--is-ancestor", branch, baseRef)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil // "not an ancestor" is a normal result, not an error
	}
	return false, fmt.Errorf("blog: git merge-base --is-ancestor %s %s: %w: %s", branch, baseRef, err, truncate(string(stderr), 300))
}

// FastForwardMerge implements GitRepo.
func (g *localGitRepo) FastForwardMerge(ctx context.Context, branch, base string) error {
	if _, err := g.exec(ctx, nil, "checkout", base); err != nil {
		return fmt.Errorf("blog: git checkout %s: %w", base, err)
	}
	// --ff-only refuses (errors, no partial state) on any non-fast-forward;
	// this package never passes --force to merge or push.
	if _, err := g.exec(ctx, nil, "merge", "--ff-only", branch); err != nil {
		return fmt.Errorf("blog: git merge --ff-only %s into %s: %w", branch, base, err)
	}
	if err := g.Push(ctx, base); err != nil {
		return err
	}
	return nil
}

// ---- Draft (blog.draft job) ----

// DraftOptions configures Draft. Completer, Git, DB, and Approvals are
// required.
type DraftOptions struct {
	Config    Config
	Completer Completer
	Git       GitRepo
	DB        DB
	Approvals ApprovalStarter
	Now       func() time.Time
	NewID     func() string
	Logger    *slog.Logger
	// MaxRedoAttempts caps approval-redo cycles before blog.draft
	// dead-letters (AC: "max retries before dead-letter"). Default 3.
	MaxRedoAttempts int
	// BacklogPath, when set, is read (read-only) for the next backlog
	// topic when a job payload has no Topic (config/blog-topics.md,
	// AC #1). One topic per line ("- topic" or plain text); lines starting
	// with "#" or already marked "[x]" are skipped. Marking a topic used
	// (removing/checking it off) is NOT implemented here — that would
	// require write access to a path outside this ticket's touches
	// (internal/blog/draft.go, internal/blog/merge.go only); see ticket
	// Notes.
	BacklogPath string
}

// Draft implements the blog.draft job (ARCHITECTURE §3.2).
type Draft struct {
	opts DraftOptions
	log  *slog.Logger
}

// NewDraft returns a Draft stage.
func NewDraft(opts DraftOptions) (*Draft, error) {
	if opts.Completer == nil {
		return nil, fmt.Errorf("blog: Draft: Completer is required")
	}
	if opts.Git == nil {
		return nil, fmt.Errorf("blog: Draft: Git is required")
	}
	if opts.DB == nil {
		return nil, fmt.Errorf("blog: Draft: DB is required")
	}
	if opts.Approvals == nil {
		return nil, fmt.Errorf("blog: Draft: Approvals is required")
	}
	opts.Config = opts.Config.withDefaults()
	if opts.MaxRedoAttempts <= 0 {
		opts.MaxRedoAttempts = 3
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Draft{opts: opts, log: log}, nil
}

func (d *Draft) now() time.Time {
	if d.opts.Now != nil {
		return d.opts.Now().UTC()
	}
	return time.Now().UTC()
}

func (d *Draft) newID() string {
	if d.opts.NewID != nil {
		return d.opts.NewID()
	}
	return newULID(d.now)
}

// DraftPayload is the blog.draft job payload.
type DraftPayload struct {
	// ContentID, when set, means this is a redo re-run of an existing
	// draft (approval.go's redo pattern: content_id + redo_notes +
	// approval_id — see package doc on Decide wiring).
	ContentID string `json:"content_id,omitempty"`
	// Topic is a manual "/blog <topic>" from Telegram. Empty -> read the
	// next backlog topic.
	Topic string `json:"topic,omitempty"`
	// TopicsSource records provenance ("manual" or "backlog") on the
	// content_items row; defaults are inferred from Topic when empty.
	TopicsSource string `json:"topics_source,omitempty"`
	RedoNotes    string `json:"redo_notes,omitempty"`
	RedoCount    int    `json:"redo_count,omitempty"`
	ApprovalID   string `json:"approval_id,omitempty"`
}

// DraftResult is the blog.draft job result payload.
type DraftResult struct {
	ContentID  string `json:"content_id"`
	ApprovalID string `json:"approval_id"`
	Branch     string `json:"branch"`
	Slug       string `json:"slug"`
	PreviewURL string `json:"preview_url,omitempty"`
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model,omitempty"`
}

// Handler adapts Draft to queue.Handler for Queue.Register(JobDraft, ...).
func (d *Draft) Handler() queue.Handler {
	return func(ctx context.Context, job queue.Job) (json.RawMessage, error) {
		var p DraftPayload
		if len(job.Payload) > 0 && string(job.Payload) != "{}" {
			if err := json.Unmarshal(job.Payload, &p); err != nil {
				return nil, queue.Permanent(fmt.Errorf("blog: draft: bad payload: %w", err))
			}
		}
		res, err := d.Run(ctx, p)
		if err != nil {
			return nil, err
		}
		out, err := json.Marshal(res)
		if err != nil {
			return nil, fmt.Errorf("blog: draft: marshal result: %w", err)
		}
		return out, nil
	}
}

var blogSystemPrompt = strings.TrimSpace(`
You are ghostwriting a post for Mayank's personal tech blog in his voice:
direct, technical, first-person, no fluff or filler, no fake enthusiasm.
Write the full post body only, as MDX prose (headings, paragraphs, code
blocks where useful). Do not include frontmatter and do not wrap the
output in a markdown code fence.
`)

// Run executes one blog.draft attempt: generate MDX via Claude (task
// "blog"), write it to a branch in the local Mayankbuilt clone, push, and
// request approval.
func (d *Draft) Run(ctx context.Context, p DraftPayload) (*DraftResult, error) {
	if p.RedoCount >= d.opts.MaxRedoAttempts {
		return nil, queue.Permanent(fmt.Errorf("blog: draft: redo attempts exhausted (%d/%d) for content %q",
			p.RedoCount, d.opts.MaxRedoAttempts, p.ContentID))
	}

	topic := strings.TrimSpace(p.Topic)
	source := strings.TrimSpace(p.TopicsSource)
	if topic == "" && strings.TrimSpace(p.ContentID) != "" {
		// Redo re-run: approval.go's redo pattern only carries content_id +
		// redo_notes + approval_id (script.write's payload shape), so pull
		// the original topic back from the content_items row this draft
		// already created.
		t, s, err := d.loadTopic(ctx, p.ContentID)
		if err != nil {
			return nil, fmt.Errorf("blog: draft: load topic for redo: %w", err)
		}
		topic = t
		if source == "" {
			source = s
		}
	}
	if topic == "" {
		t, err := d.nextBacklogTopic()
		if err != nil {
			return nil, fmt.Errorf("blog: draft: backlog: %w", err)
		}
		if t == "" {
			return nil, queue.Permanent(fmt.Errorf("blog: draft: no topic given and backlog is empty"))
		}
		topic = t
		if source == "" {
			source = "backlog"
		}
	} else if source == "" {
		source = "manual"
	}

	slug := slugify(topic)
	if slug == "" {
		return nil, queue.Permanent(fmt.Errorf("blog: draft: topic %q produced an empty slug", topic))
	}
	branch := "blog/" + slug

	body, resp, err := d.generate(ctx, topic, p.RedoNotes)
	if err != nil {
		return nil, err
	}

	now := d.now()
	fmYAML, title := buildFrontmatterYAML(d.opts.Config, topic, slug, now)
	doc := "---\n" + fmYAML + "---\n\n" + strings.TrimSpace(body) + "\n"

	contentID := strings.TrimSpace(p.ContentID)
	if contentID == "" {
		contentID, err = d.ensureContentItem(ctx, topic, source, now)
		if err != nil {
			return nil, fmt.Errorf("blog: draft: %w", err)
		}
	}

	if err := d.opts.Git.Fetch(ctx); err != nil {
		return nil, fmt.Errorf("blog: draft: %w", err)
	}
	if err := d.opts.Git.CreateBranch(ctx, branch, d.opts.Config.BaseBranch); err != nil {
		return nil, fmt.Errorf("blog: draft: %w", err)
	}

	relPath := filepath.ToSlash(filepath.Join(d.opts.Config.PostsDir, slug+".mdx"))
	absPath := filepath.Join(d.opts.Git.Dir(), filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return nil, fmt.Errorf("blog: draft: mkdir: %w", err)
	}
	// The AI-generated content (doc) is only ever written to a file, never
	// passed into a git (or any) command argv.
	if err := os.WriteFile(absPath, []byte(doc), 0o644); err != nil {
		return nil, fmt.Errorf("blog: draft: write mdx: %w", err)
	}

	commitMsg := "blog: " + title
	if strings.TrimSpace(p.RedoNotes) != "" {
		commitMsg = "blog: " + title + " (redo)"
	}
	if err := d.opts.Git.CommitAll(ctx, []string{relPath}, commitMsg); err != nil {
		return nil, fmt.Errorf("blog: draft: %w", err)
	}
	if err := d.opts.Git.Push(ctx, branch); err != nil {
		return nil, fmt.Errorf("blog: draft: %w", err)
	}

	if err := d.recordMDXAsset(ctx, contentID, relPath); err != nil {
		d.log.Warn("blog: draft: record mdx asset failed (non-fatal)", "content_id", contentID, "error", err.Error())
	}

	preview := previewURL(d.opts.Config.PreviewURLTemplate, branch, slug)
	summary := fmt.Sprintf("Blog draft %q ready on branch %s", title, branch)
	approvalID, err := d.opts.Approvals.Start(ctx, contentID, "blog", summary, preview)
	if err != nil {
		return nil, fmt.Errorf("blog: draft: approval: %w", err)
	}

	return &DraftResult{
		ContentID:  contentID,
		ApprovalID: approvalID,
		Branch:     branch,
		Slug:       slug,
		PreviewURL: preview,
		Provider:   resp.Provider,
		Model:      resp.Model,
	}, nil
}

func (d *Draft) generate(ctx context.Context, topic, redoNotes string) (string, llm.Response, error) {
	var user strings.Builder
	fmt.Fprintf(&user, "Topic: %s\n\nWrite the full blog post body now.\n", topic)
	if notes := strings.TrimSpace(redoNotes); notes != "" {
		fmt.Fprintf(&user, "\nApply these reviewer redo notes (address all of them):\n%s\n", notes)
	}
	resp, err := d.opts.Completer.Complete(ctx, llm.TaskBlog, llm.Request{
		System:    blogSystemPrompt,
		Messages:  []llm.Message{{Role: "user", Content: user.String()}},
		MaxTokens: 8192,
	})
	if err != nil {
		return "", llm.Response{}, fmt.Errorf("blog: draft: llm: %w", err)
	}
	body := stripFence(strings.TrimSpace(resp.Text))
	if body == "" {
		return "", llm.Response{}, queue.Permanent(fmt.Errorf("blog: draft: llm returned an empty post"))
	}
	return body, resp, nil
}

func (d *Draft) ensureContentItem(ctx context.Context, topic, source string, now time.Time) (string, error) {
	id := d.newID()
	script, _ := json.Marshal(map[string]any{"topic": topic, "topics_source": source})
	_, err := d.opts.DB.ExecContext(ctx, `
INSERT INTO content_items (id, channel_id, kind, format, language, stage, script, created_at)
VALUES (?, ?, 'blog', 'post', 'en', 'drafted', ?, ?)`,
		id, d.opts.Config.ChannelID, string(script), now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return "", fmt.Errorf("insert content_items (channel_id=%q must exist — see ticket Notes on the blog/channel FK): %w",
			d.opts.Config.ChannelID, err)
	}
	return id, nil
}

func (d *Draft) recordMDXAsset(ctx context.Context, contentID, relPath string) error {
	id := d.newID()
	// Absolute path so M2-402 LoadSourcePost can os.ReadFile it (publications
	// has no body column; the mdx asset is the canonical body source).
	absPath := filepath.Join(d.opts.Git.Dir(), filepath.FromSlash(relPath))
	_, err := d.opts.DB.ExecContext(ctx, `
INSERT INTO assets (id, content_id, kind, path) VALUES (?, ?, 'mdx', ?)`,
		id, contentID, absPath,
	)
	return err
}

// loadTopic recovers the topic/source stored by ensureContentItem for a
// prior draft attempt, so a redo re-run doesn't need the caller to resend
// the topic (only content_id + redo_notes, matching script.write's redo
// payload shape).
func (d *Draft) loadTopic(ctx context.Context, contentID string) (topic, source string, err error) {
	var raw sql.NullString
	err = d.opts.DB.QueryRowContext(ctx, `SELECT script FROM content_items WHERE id=?`, contentID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", fmt.Errorf("content_items %s not found", contentID)
	}
	if err != nil {
		return "", "", err
	}
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return "", "", fmt.Errorf("content_items %s has no stored topic", contentID)
	}
	var parsed struct {
		Topic        string `json:"topic"`
		TopicsSource string `json:"topics_source"`
	}
	if err := json.Unmarshal([]byte(raw.String), &parsed); err != nil {
		return "", "", fmt.Errorf("content_items %s: parse stored topic: %w", contentID, err)
	}
	if strings.TrimSpace(parsed.Topic) == "" {
		return "", "", fmt.Errorf("content_items %s: stored topic is empty", contentID)
	}
	return parsed.Topic, parsed.TopicsSource, nil
}

func (d *Draft) nextBacklogTopic() (string, error) {
	path := strings.TrimSpace(d.opts.BacklogPath)
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read backlog %s: %w", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimPrefix(line, "* ")
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "[x]") {
			continue
		}
		return line, nil
	}
	return "", nil
}

// ---- MDX frontmatter (best-effort default shape; see package doc) ----

type frontmatter struct {
	Title       string   `yaml:"title"`
	Date        string   `yaml:"date"`
	Slug        string   `yaml:"slug"`
	Description string   `yaml:"description"`
	Tags        []string `yaml:"tags,omitempty"`
}

func buildFrontmatterYAML(cfg Config, topic, slug string, now time.Time) (yamlBlock, title string) {
	fm := frontmatter{
		Title: topic,
		Date:  now.Format("2006-01-02"),
		Slug:  slug,
	}
	base, err := yaml.Marshal(fm)
	if err != nil {
		// yaml.Marshal on a plain struct of strings/[]string cannot
		// realistically fail; fall back to a minimal hand-built block
		// rather than losing the post.
		base = []byte(fmt.Sprintf("title: %q\ndate: %q\nslug: %q\n", fm.Title, fm.Date, fm.Slug))
	}
	var b strings.Builder
	b.Write(base)
	if len(cfg.FrontmatterExtra) > 0 {
		keys := make([]string, 0, len(cfg.FrontmatterExtra))
		for k := range cfg.FrontmatterExtra {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s: %q\n", k, cfg.FrontmatterExtra[k])
		}
	}
	return b.String(), fm.Title
}

func previewURL(tmpl, branch, slug string) string {
	tmpl = strings.TrimSpace(tmpl)
	if tmpl == "" {
		return ""
	}
	t, err := template.New("preview").Parse(tmpl)
	if err != nil {
		return ""
	}
	var b strings.Builder
	data := struct{ Branch, Slug string }{Branch: branch, Slug: slug}
	if err := t.Execute(&b, data); err != nil {
		return ""
	}
	return b.String()
}

// ---- small shared helpers ----

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func stripFence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) < 2 {
		return s
	}
	lines = lines[1:]
	if n := len(lines); n > 0 && strings.HasPrefix(strings.TrimSpace(lines[n-1]), "```") {
		lines = lines[:n-1]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// newULID is a package-local ULID generator (mirrors the same small,
// dependency-free pattern used by internal/content/approval.go and
// internal/events/id.go — this codebase intentionally has no shared ulid
// package).
func newULID(now func() time.Time) string {
	const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	ms := uint64(now().UnixMilli())
	var buf [26]byte
	for i := 9; i >= 0; i-- {
		buf[i] = crockford[ms&31]
		ms >>= 5
	}
	var r [16]byte
	_, _ = rand.Read(r[:])
	for i := 10; i < 26; i++ {
		buf[i] = crockford[int(r[i-10])%32]
	}
	return string(buf[:])
}
