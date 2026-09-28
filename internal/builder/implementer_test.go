package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mayank2/internal/config"
	"mayank2/internal/events"
	"mayank2/internal/queue"
)

func TestImplementerDisabledNoOp(t *testing.T) {
	im, err := NewImplementer(ImplementerOptions{
		Enabled: false,
		DataDir: t.TempDir(),
		DB:      openBuilderDB(t),
		Events:  &fakeEvents{},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := im.Run(context.Background(), ImplementPayload{Repo: "anything"})
	if err != nil {
		t.Fatalf("disabled must no-op: %v", err)
	}
	if res == nil || !res.Skipped || res.Outcome != OutcomeSkipped {
		t.Fatalf("want Skipped/OutcomeSkipped, got %+v", res)
	}
}

func TestImplementerRepoNotListed(t *testing.T) {
	im, err := NewImplementer(ImplementerOptions{
		Enabled: true,
		Repos:   []config.RepoConfig{{Name: "allowed", Path: t.TempDir()}},
		DataDir: t.TempDir(),
		DB:      openBuilderDB(t),
		Events:  &fakeEvents{},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = im.Run(context.Background(), ImplementPayload{
		Repo: "other", Subphase: "1.1", ContentID: "c1",
	})
	if err == nil {
		t.Fatal("want error for repo not in list")
	}
	if !queue.IsPermanent(err) {
		t.Fatalf("want permanent, got %v", err)
	}
	if !strings.Contains(err.Error(), "not in config.builder.repos") {
		t.Fatalf("want repos rejection, got %v", err)
	}
}

func TestImplementerWorktreeCreateThenReuse(t *testing.T) {
	repoDir := initBareishRepo(t)
	dataDir := t.TempDir()
	planRel := "plans/phase-1/1.1-demo.md"
	mustWrite(t, filepath.Join(repoDir, planRel), "# 1.1 demo\n\n## Goal\nok\n")
	sqlDB := openBuilderDB(t)
	seedApprovedPlan(t, sqlDB, "content1", "appr1")

	rec := &RecordingGit{Inner: defaultGitRunner(exec.LookPath)}
	claudeCalls := 0
	im, err := NewImplementer(ImplementerOptions{
		Enabled:          true,
		Repos:            []config.RepoConfig{{Name: "demo", Path: repoDir, BaseBranch: "main"}},
		DataDir:          dataDir,
		ImplementerModel: "claude-opus-5-5",
		RunTimeout:       time.Minute,
		DB:               sqlDB,
		Events:           &fakeEvents{},
		Git:              rec.Run,
		Claude: func(ctx context.Context, opts ClaudeSessionOpts) (ClaudeSessionResult, error) {
			claudeCalls++
			if err := os.WriteFile(filepath.Join(opts.WorkDir, "feat.txt"), []byte(fmt.Sprintf("n=%d\n", claudeCalls)), 0o644); err != nil {
				return ClaudeSessionResult{}, err
			}
			return ClaudeSessionResult{Outcome: OutcomeOK, Summary: "wrote feat", Argv: []string{"claude", "-p"}}, nil
		},
		NewID: seqIDs("b"),
	})
	if err != nil {
		t.Fatal(err)
	}

	res1, err := im.Run(context.Background(), ImplementPayload{
		Repo: "demo", Phase: "1", Subphase: "1.1", PlanFile: planRel,
		ContentID: "content1", ApprovalID: "appr1", Summary: "first",
	})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	wt1 := res1.Worktree

	mustWrite(t, filepath.Join(repoDir, "plans/phase-1/1.2-demo.md"), "# 1.2\n")
	res2, err := im.Run(context.Background(), ImplementPayload{
		Repo: "demo", Phase: "1", Subphase: "1.2", PlanFile: "plans/phase-1/1.2-demo.md",
		ContentID: "content1", ApprovalID: "appr1", Summary: "second", BuildID: res1.BuildID,
	})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if res2.Worktree != wt1 {
		t.Fatalf("want reused worktree %s, got %s", wt1, res2.Worktree)
	}
	if claudeCalls != 2 {
		t.Fatalf("claudeCalls=%d want 2", claudeCalls)
	}
	adds := 0
	for _, c := range rec.Log {
		if len(c.Args) >= 2 && c.Args[0] == "worktree" && c.Args[1] == "add" {
			adds++
		}
	}
	if adds != 1 {
		t.Fatalf("worktree add count=%d want 1; log=%v", adds, rec.Log)
	}
}

func TestImplementerNeverTouchesMain(t *testing.T) {
	repoDir := initBareishRepo(t)
	mainBefore := gitRevParse(t, repoDir, "main")
	dataDir := t.TempDir()
	planRel := "plans/phase-1/1.1-x.md"
	mustWrite(t, filepath.Join(repoDir, planRel), "# plan\n")
	sqlDB := openBuilderDB(t)
	seedApprovedPlan(t, sqlDB, "c1", "a1")

	rec := &RecordingGit{Inner: defaultGitRunner(exec.LookPath)}
	im, err := NewImplementer(ImplementerOptions{
		Enabled:    true,
		Repos:      []config.RepoConfig{{Name: "demo", Path: repoDir, BaseBranch: "main"}},
		DataDir:    dataDir,
		RunTimeout: time.Minute,
		DB:         sqlDB,
		Events:     &fakeEvents{},
		Git:        rec.Run,
		Claude: func(ctx context.Context, opts ClaudeSessionOpts) (ClaudeSessionResult, error) {
			if err := os.WriteFile(filepath.Join(opts.WorkDir, "out.go"), []byte("package out\n"), 0o644); err != nil {
				return ClaudeSessionResult{}, err
			}
			return ClaudeSessionResult{Outcome: OutcomeOK, Summary: "safe"}, nil
		},
		NewID: seqIDs("b"),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = im.Run(context.Background(), ImplementPayload{
		Repo: "demo", Subphase: "1.1", PlanFile: planRel, ContentID: "c1", ApprovalID: "a1", Summary: "safe",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	mainAfter := gitRevParse(t, repoDir, "main")
	if mainBefore != mainAfter {
		t.Fatalf("main moved: before=%s after=%s", mainBefore, mainAfter)
	}
	for _, c := range rec.Log {
		if err := rejectForbiddenGitArgs(c.Args); err != nil {
			t.Fatalf("unsafe git argv: %v (%v)", c.Args, err)
		}
		flat := strings.ToLower(strings.Join(c.Args, " "))
		if strings.HasPrefix(flat, "push") || flat == "push" || (len(c.Args) > 0 && c.Args[0] == "push") {
			t.Fatalf("push in argv: %v", c.Args)
		}
		if len(c.Args) >= 2 && (c.Args[0] == "checkout" || c.Args[0] == "switch") {
			for _, a := range c.Args[1:] {
				if a == "main" || a == "master" {
					t.Fatalf("checkout/switch main: %v", c.Args)
				}
			}
		}
		if len(c.Args) > 0 && c.Args[0] == "merge" {
			for _, a := range c.Args[1:] {
				if a == "main" || a == "master" {
					t.Fatalf("merge main: %v", c.Args)
				}
			}
		}
	}

	if err := rejectForbiddenGitArgs([]string{"push", "origin", "main"}); err == nil {
		t.Fatal("want refuse push")
	}
	if err := rejectForbiddenGitArgs([]string{"checkout", "main"}); err == nil {
		t.Fatal("want refuse checkout main")
	}
	if err := rejectForbiddenGitArgs([]string{"merge", "main"}); err == nil {
		t.Fatal("want refuse merge main")
	}
	if err := rejectForbiddenGitArgs([]string{"reset", "--hard", "HEAD~1"}); err == nil {
		t.Fatal("want refuse reset --hard")
	}
}

func TestImplementerTimeoutDistinguishable(t *testing.T) {
	repoDir := initBareishRepo(t)
	dataDir := t.TempDir()
	planRel := "plans/phase-1/1.1-x.md"
	mustWrite(t, filepath.Join(repoDir, planRel), "# plan\n")
	sqlDB := openBuilderDB(t)
	seedApprovedPlan(t, sqlDB, "c1", "a1")

	im, err := NewImplementer(ImplementerOptions{
		Enabled:    true,
		Repos:      []config.RepoConfig{{Name: "demo", Path: repoDir}},
		DataDir:    dataDir,
		RunTimeout: 50 * time.Millisecond,
		DB:         sqlDB,
		Events:     &fakeEvents{},
		Git:        defaultGitRunner(exec.LookPath),
		Claude: func(ctx context.Context, opts ClaudeSessionOpts) (ClaudeSessionResult, error) {
			return ClaudeSessionResult{Outcome: OutcomeTimeout}, ErrRunTimeout
		},
		NewID: seqIDs("b"),
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := im.Run(context.Background(), ImplementPayload{
		Repo: "demo", Subphase: "1.1", PlanFile: planRel, ContentID: "c1", ApprovalID: "a1",
	})
	if !errors.Is(err, ErrRunTimeout) {
		t.Fatalf("want ErrRunTimeout, got %v", err)
	}
	if res == nil || res.Outcome != OutcomeTimeout {
		t.Fatalf("want OutcomeTimeout, got %+v", res)
	}
}

func TestImplementerUsageLimitDistinguishable(t *testing.T) {
	repoDir := initBareishRepo(t)
	dataDir := t.TempDir()
	planRel := "plans/phase-1/1.1-x.md"
	mustWrite(t, filepath.Join(repoDir, planRel), "# plan\n")
	sqlDB := openBuilderDB(t)
	seedApprovedPlan(t, sqlDB, "c1", "a1")

	im, err := NewImplementer(ImplementerOptions{
		Enabled:    true,
		Repos:      []config.RepoConfig{{Name: "demo", Path: repoDir}},
		DataDir:    dataDir,
		RunTimeout: time.Minute,
		DB:         sqlDB,
		Events:     &fakeEvents{},
		Git:        defaultGitRunner(exec.LookPath),
		Claude: func(ctx context.Context, opts ClaudeSessionOpts) (ClaudeSessionResult, error) {
			return ClaudeSessionResult{Outcome: OutcomeUsageLimit, Stderr: "usage limit"}, ErrUsageLimit
		},
		NewID: seqIDs("b"),
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := im.Run(context.Background(), ImplementPayload{
		Repo: "demo", Subphase: "1.1", PlanFile: planRel, ContentID: "c1", ApprovalID: "a1",
	})
	if !errors.Is(err, ErrUsageLimit) {
		t.Fatalf("want ErrUsageLimit, got %v", err)
	}
	if res == nil || res.Outcome != OutcomeUsageLimit {
		t.Fatalf("want OutcomeUsageLimit, got %+v", res)
	}
}

func TestImplementerEmitsSubphaseDone(t *testing.T) {
	repoDir := initBareishRepo(t)
	dataDir := t.TempDir()
	planRel := "plans/phase-1/1.1-x.md"
	mustWrite(t, filepath.Join(repoDir, planRel), "# plan\n")
	sqlDB := openBuilderDB(t)
	seedApprovedPlan(t, sqlDB, "c1", "a1")
	ev := &fakeEvents{}

	im, err := NewImplementer(ImplementerOptions{
		Enabled:    true,
		Repos:      []config.RepoConfig{{Name: "demo", Path: repoDir}},
		DataDir:    dataDir,
		RunTimeout: time.Minute,
		DB:         sqlDB,
		Events:     ev,
		Git:        defaultGitRunner(exec.LookPath),
		Claude: func(ctx context.Context, opts ClaudeSessionOpts) (ClaudeSessionResult, error) {
			_ = os.WriteFile(filepath.Join(opts.WorkDir, "x.txt"), []byte("x"), 0o644)
			argv := []string{"claude", "-p", "--disallowed-tools", "Bash(git push*)", "--model", opts.Model}
			return ClaudeSessionResult{Outcome: OutcomeOK, Summary: "emit me", Argv: argv}, nil
		},
		NewID: seqIDs("b"),
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := im.Run(context.Background(), ImplementPayload{
		Repo: "demo", Subphase: "1.2", PlanFile: planRel, ContentID: "c1", ApprovalID: "a1", Summary: "emit me",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ev.calls) != 1 {
		t.Fatalf("emit calls=%d want 1", len(ev.calls))
	}
	c := ev.calls[0]
	if c.Kind != KindSubphaseDone {
		t.Fatalf("kind=%q want %q", c.Kind, KindSubphaseDone)
	}
	if c.Actor != events.ActorAgent {
		t.Fatalf("actor=%q", c.Actor)
	}
	raw, _ := json.Marshal(c.Data)
	if !strings.Contains(string(raw), `"repo":"demo"`) || !strings.Contains(string(raw), `"subphase":"1.2"`) {
		t.Fatalf("payload data=%s", raw)
	}
	if res.CommitMsg != "1.2: emit me" {
		t.Fatalf("commit msg=%q", res.CommitMsg)
	}
	if res.Outcome != OutcomeOK {
		t.Fatalf("outcome=%q", res.Outcome)
	}
}

func TestImplementerHandlerDisabled(t *testing.T) {
	im, err := NewImplementer(ImplementerOptions{
		Enabled: false,
		DataDir: t.TempDir(),
		DB:      openBuilderDB(t),
		Events:  &fakeEvents{},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := im.Handler()(context.Background(), queue.Job{Payload: json.RawMessage(`{"repo":"x"}`)})
	if err != nil {
		t.Fatal(err)
	}
	var res ImplementResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatal(err)
	}
	if !res.Skipped {
		t.Fatalf("want skipped, got %+v", res)
	}
}

func TestRejectForbiddenGitArgs(t *testing.T) {
	cases := []struct {
		args []string
		ok   bool
	}{
		{[]string{"status"}, true},
		{[]string{"worktree", "add", "-b", "build/phase-1", "/tmp/wt", "main"}, true},
		{[]string{"push", "origin", "build/phase-1"}, false},
		{[]string{"checkout", "main"}, false},
		{[]string{"reset", "--hard", "HEAD"}, false},
		{[]string{"merge", "main"}, false},
	}
	for _, tc := range cases {
		err := rejectForbiddenGitArgs(tc.args)
		if tc.ok && err != nil {
			t.Fatalf("args=%v: unexpected err %v", tc.args, err)
		}
		if !tc.ok && err == nil {
			t.Fatalf("args=%v: want error", tc.args)
		}
	}
}

// --- helpers ---

type fakeEvents struct {
	calls []struct {
		Actor   events.Actor
		Kind    string
		Ref     string
		Message string
		Data    any
	}
}

func (f *fakeEvents) Emit(ctx context.Context, actor events.Actor, kind, ref, message string, data any) (events.Event, error) {
	f.calls = append(f.calls, struct {
		Actor   events.Actor
		Kind    string
		Ref     string
		Message string
		Data    any
	}{actor, kind, ref, message, data})
	return events.Event{ID: "evt", Kind: kind, Actor: actor, Ref: ref, Message: message}, nil
}

func seedApprovedPlan(t *testing.T, db DB, contentID, approvalID string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.ExecContext(context.Background(), `
INSERT INTO content_items (id, channel_id, kind, format, language, stage, created_at)
VALUES (?, 'builder', 'post', 'builder_plan', 'en', 'planned', ?)`, contentID, now); err != nil {
		t.Fatalf("seed content: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), `
INSERT INTO approvals (id, content_id, kind, summary, status, nonce, decided_at)
VALUES (?, ?, 'plan', 'ok', 'approved', 'nonce', ?)`, approvalID, contentID, now); err != nil {
		t.Fatalf("seed approval: %v", err)
	}
}

func initBareishRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(scrubGitEnv(os.Environ()),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "test")
	mustWrite(t, filepath.Join(dir, "README.md"), "seed\n")
	run("add", "README.md")
	run("commit", "-m", "seed")
	return dir
}

func gitRevParse(t *testing.T, dir, ref string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", ref)
	cmd.Dir = dir
	cmd.Env = scrubGitEnv(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse %s: %v", ref, err)
	}
	return strings.TrimSpace(string(out))
}
