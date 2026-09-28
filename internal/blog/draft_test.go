package blog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mayank2/internal/db"
	"mayank2/internal/llm"
	"mayank2/internal/queue"
)

// ---- shared test helpers (used by draft_test.go and merge_test.go) ----

// newTestMayankbuiltRepo creates a bare "remote" repo and a working clone
// with one commit on main, standing in for Mayank's real Mayankbuilt repo
// (whose actual location/URL is unconfirmed — see package doc). All git
// operations here are local filesystem only, no network.
func newTestMayankbuiltRepo(t *testing.T) (remoteDir, cloneDir string) {
	t.Helper()
	remoteDir = filepath.Join(t.TempDir(), "remote.git")
	cloneDir = filepath.Join(t.TempDir(), "clone")
	runGitT(t, "", "init", "--bare", remoteDir)
	runGitT(t, "", "clone", remoteDir, cloneDir)
	runGitT(t, cloneDir, "config", "user.email", "test@example.test")
	runGitT(t, cloneDir, "config", "user.name", "Test")
	// Force HEAD to refs/heads/main regardless of this git install's
	// default branch name, so the tests don't depend on that config.
	runGitT(t, cloneDir, "symbolic-ref", "HEAD", "refs/heads/main")
	mustWriteFile(t, filepath.Join(cloneDir, "README.md"), "# Mayankbuilt\n")
	runGitT(t, cloneDir, "add", "README.md")
	runGitT(t, cloneDir, "commit", "-m", "init")
	runGitT(t, cloneDir, "push", "-u", "origin", "main")
	// The bare remote's own HEAD symref may still point at whatever
	// init.defaultBranch this git install used (not necessarily "main"),
	// which would leave a fresh clone of remoteDir with no local branch
	// checked out at all. Point it at main explicitly.
	runGitT(t, remoteDir, "symbolic-ref", "HEAD", "refs/heads/main")
	return remoteDir, cloneDir
}

func runGitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s (dir=%s): %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func gitShow(dir, ref string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "show", ref).CombinedOutput()
	return string(out), err
}

func mustWriteFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func openBlogDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	sqlDB, err := db.Open(ctx, filepath.Join(t.TempDir(), "blog.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := db.Migrate(ctx, sqlDB); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	// content_items.channel_id is NOT NULL REFERENCES channels(id); blog
	// posts have no real channel (see Config.ChannelID doc). Seed the
	// sentinel row the default Config.ChannelID ("blog") points at.
	if _, err := sqlDB.Exec(`
INSERT INTO channels (id, platform, handle, language, niche, account_ref, status)
VALUES ('blog', 'mayankbuilt', '', 'en', 'personal_blog', '', 'active')`); err != nil {
		t.Fatalf("seed blog channel: %v", err)
	}
	return sqlDB
}

func seqIDs(prefix string) func() string {
	n := 0
	return func() string {
		n++
		return fmt.Sprintf("%s%d", prefix, n)
	}
}

func fixedNow(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func seedBlogContentItem(t *testing.T, sqlDB *sql.DB, id string) {
	t.Helper()
	_, err := sqlDB.Exec(`
INSERT INTO content_items (id, channel_id, kind, format, language, stage, created_at)
VALUES (?, 'blog', 'blog', 'post', 'en', 'drafted', ?)`,
		id, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatalf("seed content_items: %v", err)
	}
}

func seedApproval(t *testing.T, sqlDB *sql.DB, id, contentID, status string) {
	t.Helper()
	_, err := sqlDB.Exec(`
INSERT INTO approvals (id, content_id, kind, summary, status, nonce, decided_at)
VALUES (?, ?, 'blog', 's', ?, 'n', ?)`,
		id, contentID, status, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatalf("seed approval: %v", err)
	}
}

// fakeCompleter is a Completer test double; no real Claude/LLM calls.
type fakeCompleter struct {
	body     string
	err      error
	calls    int
	lastTask llm.Task
	lastReq  llm.Request
}

func (f *fakeCompleter) Complete(ctx context.Context, task llm.Task, req llm.Request) (llm.Response, error) {
	f.calls++
	f.lastTask = task
	f.lastReq = req
	if f.err != nil {
		return llm.Response{}, f.err
	}
	return llm.Response{Text: f.body, Provider: "claude", Model: "claude-sonnet-5"}, nil
}

// fakeApproval is an ApprovalStarter test double.
type fakeApproval struct {
	calls  []struct{ ContentID, Kind, Summary, Preview string }
	nextID func() string
	err    error
}

func (f *fakeApproval) Start(ctx context.Context, contentID, kind, summary, previewPath string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.calls = append(f.calls, struct{ ContentID, Kind, Summary, Preview string }{contentID, kind, summary, previewPath})
	id := "appr"
	if f.nextID != nil {
		id = f.nextID()
	}
	return id, nil
}

// fakeGitRepo is a GitRepo spy/stub for tests that must prove a code path
// never touches git (e.g. the approval gate short-circuiting before any
// merge attempt).
type fakeGitRepo struct {
	dir string

	fetchCalls, createBranchCalls, commitCalls, pushCalls, alreadyMergedCalls, ffMergeCalls int

	alreadyMerged    bool
	alreadyMergedErr error
	ffMergeErr       error
	fetchErr         error
	createBranchErr  error
	commitErr        error
	pushErr          error
}

func (f *fakeGitRepo) Dir() string { return f.dir }
func (f *fakeGitRepo) Fetch(ctx context.Context) error {
	f.fetchCalls++
	return f.fetchErr
}
func (f *fakeGitRepo) CreateBranch(ctx context.Context, branch, base string) error {
	f.createBranchCalls++
	return f.createBranchErr
}
func (f *fakeGitRepo) CommitAll(ctx context.Context, paths []string, message string) error {
	f.commitCalls++
	return f.commitErr
}
func (f *fakeGitRepo) Push(ctx context.Context, branch string) error {
	f.pushCalls++
	return f.pushErr
}
func (f *fakeGitRepo) AlreadyMerged(ctx context.Context, branch, base string) (bool, error) {
	f.alreadyMergedCalls++
	return f.alreadyMerged, f.alreadyMergedErr
}
func (f *fakeGitRepo) FastForwardMerge(ctx context.Context, branch, base string) error {
	f.ffMergeCalls++
	return f.ffMergeErr
}

// stubCred is a CredentialSource test double.
type stubCred struct {
	token string
	err   error
}

func (s stubCred) Token(ctx context.Context) (string, error) { return s.token, s.err }

// ---- pure-function tests ----

func TestSlugify(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Why Go Channels Are Underrated", "why-go-channels-are-underrated"},
		{"  leading/trailing spaces  ", "leading-trailing-spaces"},
		{"C++ vs Rust: 2026", "c-vs-rust-2026"},
		{"日本語 topic", "topic"},
		{"", ""},
		{"!!!", ""},
	}
	for _, c := range cases {
		if got := slugify(c.in); got != c.want {
			t.Errorf("slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPreviewURL(t *testing.T) {
	if got := previewURL("", "blog/x", "x"); got != "" {
		t.Errorf("empty template = %q, want empty", got)
	}
	if got := previewURL("{{.Nope", "blog/x", "x"); got != "" {
		t.Errorf("bad template = %q, want empty (parse error swallowed)", got)
	}
	got := previewURL("https://mayankbuilt-git-{{.Slug}}.vercel.app", "blog/my-slug", "my-slug")
	want := "https://mayankbuilt-git-my-slug.vercel.app"
	if got != want {
		t.Errorf("previewURL = %q, want %q", got, want)
	}
}

// ---- credential redaction (no real git binary, no real network) ----

func TestLocalGitRepo_PushRedactsToken(t *testing.T) {
	const token = "ghp_supersecrettoken1234567890abcdef"
	runner := func(ctx context.Context, dir string, args []string) ([]byte, []byte, error) {
		if len(args) >= 2 && args[0] == "remote" && args[1] == "get-url" {
			return []byte("https://github.com/mayank/mayankbuilt.git\n"), nil, nil
		}
		if len(args) > 0 && args[0] == "push" {
			// Simulate git echoing the (token-bearing) URL back into its
			// own error output, as some TLS/auth failures do.
			joined := strings.Join(args, " ")
			return nil, []byte("fatal: unable to access '" + joined + "': TLS error"), fmt.Errorf("exit status 128")
		}
		return nil, nil, nil
	}
	repo := NewLocalGitRepo(Config{RepoPath: "unused", Remote: "origin"}, stubCred{token: token}, runner)
	err := repo.Push(context.Background(), "blog/my-post")
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("token leaked into returned error: %v", err)
	}
}

// ---- Draft.Run: real local git repo + real SQLite, no network, no real LLM ----

func TestDraft_RunHappyPath(t *testing.T) {
	_, cloneDir := newTestMayankbuiltRepo(t)
	cfg := Config{RepoPath: cloneDir, BaseBranch: "main", Remote: "origin", PostsDir: "content/blog", SiteBaseURL: "https://example.test"}
	git := NewLocalGitRepo(cfg, nil, nil)
	completer := &fakeCompleter{body: "This is the full post body about Go channels."}
	approvals := &fakeApproval{nextID: seqIDs("appr-")}
	sqlDB := openBlogDB(t)

	draft, err := NewDraft(DraftOptions{
		Config: cfg, Completer: completer, Git: git, DB: sqlDB, Approvals: approvals,
		Now:   fixedNow(time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)),
		NewID: seqIDs("ID"),
	})
	if err != nil {
		t.Fatalf("NewDraft: %v", err)
	}

	res, err := draft.Run(context.Background(), DraftPayload{Topic: "Why Go Channels Are Underrated", TopicsSource: "manual"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	wantSlug := "why-go-channels-are-underrated"
	if res.Slug != wantSlug {
		t.Errorf("slug = %q, want %q", res.Slug, wantSlug)
	}
	if res.Branch != "blog/"+wantSlug {
		t.Errorf("branch = %q", res.Branch)
	}
	if res.ApprovalID == "" {
		t.Error("empty approval id")
	}
	if res.ContentID == "" {
		t.Error("empty content id")
	}
	if res.PreviewURL != "" {
		t.Errorf("preview url = %q, want empty (no template configured — mechanism unconfirmed)", res.PreviewURL)
	}

	if len(completer.lastReq.Messages) == 0 || !strings.Contains(completer.lastReq.Messages[0].Content, "Why Go Channels Are Underrated") {
		t.Errorf("prompt missing topic: %+v", completer.lastReq.Messages)
	}
	if completer.lastTask != llm.TaskBlog {
		t.Errorf("task = %q, want %q", completer.lastTask, llm.TaskBlog)
	}

	relPath := "content/blog/" + wantSlug + ".mdx"
	got := runGitT(t, cloneDir, "show", res.Branch+":"+relPath)
	if !strings.Contains(got, "This is the full post body about Go channels.") {
		t.Errorf("pushed mdx missing body:\n%s", got)
	}
	if !strings.HasPrefix(got, "---\ntitle:") {
		t.Errorf("pushed mdx missing frontmatter:\n%s", got)
	}
	if _, err := gitShow(cloneDir, "main:"+relPath); err == nil {
		t.Error("post must not exist on main before approval/merge")
	}

	if len(approvals.calls) != 1 {
		t.Fatalf("approval calls = %d, want 1", len(approvals.calls))
	}
	if approvals.calls[0].ContentID != res.ContentID || approvals.calls[0].Kind != "blog" {
		t.Errorf("approval call = %+v", approvals.calls[0])
	}

	var kind, channelID string
	if err := sqlDB.QueryRow(`SELECT kind, channel_id FROM content_items WHERE id=?`, res.ContentID).Scan(&kind, &channelID); err != nil {
		t.Fatalf("query content_items: %v", err)
	}
	if kind != "blog" || channelID != "blog" {
		t.Errorf("content_items kind/channel = %q/%q", kind, channelID)
	}
	var assetPath string
	if err := sqlDB.QueryRow(`SELECT path FROM assets WHERE content_id=? AND kind='mdx'`, res.ContentID).Scan(&assetPath); err != nil {
		t.Fatalf("query assets: %v", err)
	}
	wantAsset := filepath.Join(cloneDir, filepath.FromSlash(relPath))
	if assetPath != wantAsset {
		t.Errorf("asset path = %q, want absolute %q", assetPath, wantAsset)
	}
}

func TestDraft_RunRedoAppendsNotesKeepsHistory(t *testing.T) {
	_, cloneDir := newTestMayankbuiltRepo(t)
	cfg := Config{RepoPath: cloneDir, BaseBranch: "main", Remote: "origin", PostsDir: "content/blog"}
	git := NewLocalGitRepo(cfg, nil, nil)
	completer := &fakeCompleter{body: "First draft body."}
	approvals := &fakeApproval{nextID: seqIDs("appr-")}
	sqlDB := openBlogDB(t)

	draft, err := NewDraft(DraftOptions{Config: cfg, Completer: completer, Git: git, DB: sqlDB, Approvals: approvals, NewID: seqIDs("ID")})
	if err != nil {
		t.Fatalf("NewDraft: %v", err)
	}

	res1, err := draft.Run(context.Background(), DraftPayload{Topic: "Redo Test Topic", TopicsSource: "manual"})
	if err != nil {
		t.Fatalf("Run 1: %v", err)
	}

	completer.body = "Second draft body, funnier now."
	res2, err := draft.Run(context.Background(), DraftPayload{
		ContentID: res1.ContentID, RedoNotes: "make it funnier", RedoCount: 1,
	})
	if err != nil {
		t.Fatalf("Run 2 (redo): %v", err)
	}

	if res2.Branch != res1.Branch || res2.Slug != res1.Slug {
		t.Fatalf("redo changed branch/slug: %+v vs %+v", res1, res2)
	}
	if res2.ContentID != res1.ContentID {
		t.Errorf("redo content id changed: %q vs %q", res2.ContentID, res1.ContentID)
	}
	if !strings.Contains(completer.lastReq.Messages[0].Content, "make it funnier") {
		t.Errorf("redo prompt missing notes: %q", completer.lastReq.Messages[0].Content)
	}

	log := runGitT(t, cloneDir, "log", "--oneline", res1.Branch)
	lines := strings.Split(strings.TrimSpace(log), "\n")
	if len(lines) < 3 { // init + draft 1 + draft 2 (redo)
		t.Fatalf("branch log too short (%d lines), history was reset instead of appended to:\n%s", len(lines), log)
	}
	if !strings.Contains(log, "(redo)") {
		t.Errorf("second commit missing redo marker:\n%s", log)
	}
	if len(approvals.calls) != 2 {
		t.Fatalf("approval calls = %d, want 2", len(approvals.calls))
	}
}

func TestDraft_RunRedoExhaustsMaxAttempts(t *testing.T) {
	completer := &fakeCompleter{body: "unused"}
	git := &fakeGitRepo{}
	approvals := &fakeApproval{}
	sqlDB := openBlogDB(t)

	draft, err := NewDraft(DraftOptions{
		Config: Config{RepoPath: "unused"}, Completer: completer, Git: git, DB: sqlDB, Approvals: approvals,
		MaxRedoAttempts: 2,
	})
	if err != nil {
		t.Fatalf("NewDraft: %v", err)
	}

	_, err = draft.Run(context.Background(), DraftPayload{ContentID: "c1", RedoCount: 2, RedoNotes: "n"})
	if err == nil {
		t.Fatal("want error")
	}
	if !queue.IsPermanent(err) {
		t.Errorf("want a Permanent (dead-letter) error, got %v", err)
	}
	if completer.calls != 0 || git.fetchCalls != 0 || len(approvals.calls) != 0 {
		t.Errorf("dependencies were called despite exhausted redo cap: completer=%d fetch=%d approvals=%d",
			completer.calls, git.fetchCalls, len(approvals.calls))
	}
}

func TestDraft_RunNoTopicNoBacklogIsPermanent(t *testing.T) {
	completer := &fakeCompleter{body: "unused"}
	git := &fakeGitRepo{}
	approvals := &fakeApproval{}
	sqlDB := openBlogDB(t)
	draft, err := NewDraft(DraftOptions{Config: Config{RepoPath: "unused"}, Completer: completer, Git: git, DB: sqlDB, Approvals: approvals})
	if err != nil {
		t.Fatalf("NewDraft: %v", err)
	}
	_, err = draft.Run(context.Background(), DraftPayload{})
	if !queue.IsPermanent(err) {
		t.Errorf("want a Permanent error for no topic + empty backlog, got %v", err)
	}
}

func TestDraft_HandlerAdaptsToQueue(t *testing.T) {
	_, cloneDir := newTestMayankbuiltRepo(t)
	cfg := Config{RepoPath: cloneDir, PostsDir: "content/blog"}
	git := NewLocalGitRepo(cfg, nil, nil)
	completer := &fakeCompleter{body: "Handler test body."}
	approvals := &fakeApproval{nextID: seqIDs("appr-")}
	sqlDB := openBlogDB(t)

	draft, err := NewDraft(DraftOptions{Config: cfg, Completer: completer, Git: git, DB: sqlDB, Approvals: approvals, NewID: seqIDs("ID")})
	if err != nil {
		t.Fatalf("NewDraft: %v", err)
	}

	payload, _ := json.Marshal(DraftPayload{Topic: "Handler Topic", TopicsSource: "manual"})
	job := queue.Job{ID: "job1", Type: JobDraft, Payload: payload}
	out, err := draft.Handler()(context.Background(), job)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	var res DraftResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if res.Branch == "" {
		t.Error("empty branch in handler result")
	}
}

func TestDraft_HandlerBadPayload(t *testing.T) {
	git := &fakeGitRepo{}
	draft, err := NewDraft(DraftOptions{Config: Config{RepoPath: "unused"}, Completer: &fakeCompleter{}, Git: git, DB: openBlogDB(t), Approvals: &fakeApproval{}})
	if err != nil {
		t.Fatalf("NewDraft: %v", err)
	}
	job := queue.Job{ID: "job1", Type: JobDraft, Payload: json.RawMessage(`not json`)}
	_, err = draft.Handler()(context.Background(), job)
	if !queue.IsPermanent(err) {
		t.Errorf("want a Permanent error for bad payload, got %v", err)
	}
}
