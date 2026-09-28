package builder

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mayank2/internal/config"
	"mayank2/internal/events"
	"mayank2/internal/queue"
)

func TestAuditorDisabledNoOp(t *testing.T) {
	au, err := NewAuditor(AuditorOptions{
		Enabled: false,
		DataDir: t.TempDir(),
		DB:      openBuilderDB(t),
		Events:  &fakeEvents{},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := au.Run(context.Background(), AuditPayload{Repo: "anything"})
	if err != nil {
		t.Fatalf("disabled must no-op: %v", err)
	}
	if res == nil || !res.Skipped || res.Outcome != OutcomeSkipped {
		t.Fatalf("want Skipped/OutcomeSkipped, got %+v", res)
	}
}

func TestAuditorRepoNotListed(t *testing.T) {
	au, err := NewAuditor(AuditorOptions{
		Enabled: true,
		Repos:   []config.RepoConfig{{Name: "allowed", Path: t.TempDir()}},
		DataDir: t.TempDir(),
		DB:      openBuilderDB(t),
		Events:  &fakeEvents{},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = au.Run(context.Background(), AuditPayload{Repo: "other", Subphase: "1.1"})
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

// --- Subscribe (triggered by subphase.done) ---

type fakeBus struct {
	ch chan events.Event
}

func (f *fakeBus) Subscribe(ctx context.Context) (<-chan events.Event, func()) {
	return f.ch, func() {}
}

type enqueueCall struct {
	JobType string
	Payload any
}

type fakeEnqueuer struct {
	mu    sync.Mutex
	calls []enqueueCall
	err   error
}

func (f *fakeEnqueuer) Enqueue(ctx context.Context, jobType string, payload any, opts ...queue.EnqueueOpt) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	f.calls = append(f.calls, enqueueCall{JobType: jobType, Payload: payload})
	return "job1", nil
}

func (f *fakeEnqueuer) snapshot() []enqueueCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]enqueueCall(nil), f.calls...)
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("condition not met before timeout")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAuditorSubscribeTriggeredBySubphaseDone(t *testing.T) {
	au, err := NewAuditor(AuditorOptions{
		Enabled: true,
		DataDir: t.TempDir(),
		DB:      openBuilderDB(t),
		Events:  &fakeEvents{},
	})
	if err != nil {
		t.Fatal(err)
	}
	bus := &fakeBus{ch: make(chan events.Event, 4)}
	enq := &fakeEnqueuer{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	au.Subscribe(ctx, bus, enq)

	// Unrelated event kind must be ignored.
	bus.ch <- events.Event{Kind: "job.updated"}

	data, err := json.Marshal(subphaseDoneData{
		Repo: "demo", Phase: "1", Subphase: "1.1",
		PlanPath: "plans/phase-1/1.1-x.md", Branch: "build/phase-1",
		Worktree: "/somewhere", BuildID: "b1",
	})
	if err != nil {
		t.Fatal(err)
	}
	bus.ch <- events.Event{Kind: KindSubphaseDone, Ref: "b1", Data: data}

	waitFor(t, 2*time.Second, func() bool { return len(enq.snapshot()) >= 1 })

	calls := enq.snapshot()
	if len(calls) != 1 {
		t.Fatalf("want exactly 1 enqueue (unrelated kind ignored), got %d: %+v", len(calls), calls)
	}
	if calls[0].JobType != JobAudit {
		t.Fatalf("job type = %q, want %q", calls[0].JobType, JobAudit)
	}
	payload, ok := calls[0].Payload.(AuditPayload)
	if !ok {
		t.Fatalf("payload type = %T, want AuditPayload", calls[0].Payload)
	}
	if payload.Repo != "demo" || payload.Subphase != "1.1" || payload.Branch != "build/phase-1" || payload.BuildID != "b1" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

// --- git fixtures ---

func gitEnv() []string {
	return append(scrubGitEnv(os.Environ()),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=t@example.com")
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v (dir=%s): %v (%s)", args, dir, err, out)
	}
	return string(out)
}

// setupBuildBranch creates branch off main with one subphase commit
// ("N.M: demo change") containing files, via a scratch worktree that is
// removed afterward — mirroring the Implementer's real commit shape
// (implementer.go's commitMsg format) without leaving repoDir itself on a
// non-main branch.
func setupBuildBranch(t *testing.T, repoDir, branch, subphase string, files map[string]string) {
	t.Helper()
	scratch := filepath.Join(t.TempDir(), "scratch")
	runGit(t, repoDir, "worktree", "add", "-b", branch, scratch, "main")
	for path, content := range files {
		mustWrite(t, filepath.Join(scratch, path), content)
		runGit(t, scratch, "add", path)
	}
	runGit(t, scratch, "commit", "-m", subphase+": demo change")
	runGit(t, repoDir, "worktree", "remove", scratch, "--force")
}

func verdictStdout(verdict string) string {
	return "Reviewed the diff.\n```json\n{\"verdict\": \"" + verdict + "\", \"findings\": []}\n```\n"
}

// --- commits only tests + audit, never touches main ---

func TestAuditorCommitsOnlyTestsAndAudit(t *testing.T) {
	repoDir := initBareishRepo(t)
	planRel := "plans/phase-1/1.1-demo.md"
	mustWrite(t, filepath.Join(repoDir, planRel), "# 1.1 demo\n\n## Goal\nok\n")
	setupBuildBranch(t, repoDir, "build/phase-1", "1.1", map[string]string{"src/prod.go": "package src\n"})
	mainBefore := gitRevParse(t, repoDir, "main")
	branchBefore := gitRevParse(t, repoDir, "build/phase-1")

	dataDir := t.TempDir()
	rec := &RecordingGit{Inner: defaultImplementerGitRunner(exec.LookPath)}
	au, err := NewAuditor(AuditorOptions{
		Enabled:      true,
		Repos:        []config.RepoConfig{{Name: "demo", Path: repoDir}},
		DataDir:      dataDir,
		AuditorModel: "claude-sonnet-5",
		RunTimeout:   time.Minute,
		DB:           openBuilderDB(t),
		Events:       &fakeEvents{},
		Git:          rec.Run,
		Claude: func(ctx context.Context, opts ClaudeSessionOpts) (ClaudeSessionResult, error) {
			// Writes an allowed test file AND a disallowed source edit —
			// the Auditor must refuse to commit either, discard both, and
			// never land anything on the build branch.
			if err := os.WriteFile(filepath.Join(opts.WorkDir, "src", "prod_test.go"), []byte("package src\n"), 0o644); err != nil {
				return ClaudeSessionResult{}, err
			}
			if err := os.WriteFile(filepath.Join(opts.WorkDir, "src", "prod.go"), []byte("package src\n// oops, edited source\n"), 0o644); err != nil {
				return ClaudeSessionResult{}, err
			}
			return ClaudeSessionResult{Outcome: OutcomeOK, Stdout: verdictStdout("fail")}, nil
		},
		Playwright: func(ctx context.Context, opts PlaywrightOpts) (PlaywrightResult, error) {
			return PlaywrightResult{Skipped: true}, nil
		},
		NewID: seqIDs("a"),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = au.Run(context.Background(), AuditPayload{
		Repo: "demo", Phase: "1", Subphase: "1.1", Branch: "build/phase-1", PlanFile: planRel,
	})
	if err == nil {
		t.Fatal("want refusal to commit non-test/non-audit changes")
	}
	if !queue.IsPermanent(err) {
		t.Fatalf("want permanent, got %v", err)
	}
	if !strings.Contains(err.Error(), "src/prod.go") {
		t.Fatalf("error should name the disallowed path, got: %v", err)
	}

	if got := gitRevParse(t, repoDir, "main"); got != mainBefore {
		t.Fatalf("main moved: before=%s after=%s", mainBefore, got)
	}
	if got := gitRevParse(t, repoDir, "build/phase-1"); got != branchBefore {
		t.Fatalf("build branch moved despite refusal: before=%s after=%s", branchBefore, got)
	}
	for _, c := range rec.Log {
		if err := rejectForbiddenGitArgs(c.Args); err != nil {
			t.Fatalf("unsafe git argv recorded: %v (%v)", c.Args, err)
		}
	}
}

func TestAuditorWritesAuditFileAndLandsOnBranch(t *testing.T) {
	repoDir := initBareishRepo(t)
	planRel := "plans/phase-1/1.1-demo.md"
	mustWrite(t, filepath.Join(repoDir, planRel), "# 1.1 demo\n\n## Goal\nok\n")
	setupBuildBranch(t, repoDir, "build/phase-1", "1.1", map[string]string{"src/prod.go": "package src\n"})
	mainBefore := gitRevParse(t, repoDir, "main")

	dataDir := t.TempDir()
	rec := &RecordingGit{Inner: defaultImplementerGitRunner(exec.LookPath)}
	au, err := NewAuditor(AuditorOptions{
		Enabled:      true,
		Repos:        []config.RepoConfig{{Name: "demo", Path: repoDir}},
		DataDir:      dataDir,
		AuditorModel: "claude-sonnet-5",
		RunTimeout:   time.Minute,
		DB:           openBuilderDB(t),
		Events:       &fakeEvents{},
		Git:          rec.Run,
		Claude: func(ctx context.Context, opts ClaudeSessionOpts) (ClaudeSessionResult, error) {
			if err := os.WriteFile(filepath.Join(opts.WorkDir, "src", "prod_test.go"), []byte("package src\nfunc TestX(t *testing.T){}\n"), 0o644); err != nil {
				return ClaudeSessionResult{}, err
			}
			return ClaudeSessionResult{Outcome: OutcomeOK, Stdout: verdictStdout("pass")}, nil
		},
		Playwright: func(ctx context.Context, opts PlaywrightOpts) (PlaywrightResult, error) {
			return PlaywrightResult{Skipped: true}, nil
		},
		NewID: seqIDs("a"),
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := au.Run(context.Background(), AuditPayload{
		Repo: "demo", Phase: "1", Subphase: "1.1", Branch: "build/phase-1", PlanFile: planRel,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Outcome != OutcomeOK {
		t.Fatalf("outcome=%q want ok", res.Outcome)
	}
	if res.Verdict != VerdictPass {
		t.Fatalf("verdict=%q want pass", res.Verdict)
	}
	if res.AuditPath != "audit/1.1.md" {
		t.Fatalf("audit path=%q want audit/1.1.md", res.AuditPath)
	}
	if res.CommitSHA == "" {
		t.Fatal("want a commit sha")
	}

	raw, err := os.ReadFile(filepath.Join(res.Worktree, res.AuditPath))
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	body := string(raw)
	if !strings.HasPrefix(body, "---\n") || !strings.Contains(body, "verdict: pass") || !strings.Contains(body, "subphase: \"1.1\"") && !strings.Contains(body, "subphase: 1.1") {
		t.Fatalf("audit file missing expected frontmatter shape:\n%s", body)
	}

	// Landed on the shared build branch (ARCHITECTURE §3.3).
	branchTip := gitRevParse(t, repoDir, "build/phase-1")
	if branchTip != res.CommitSHA {
		t.Fatalf("build branch tip=%s want auditor commit=%s", branchTip, res.CommitSHA)
	}
	onBranch := runGit(t, repoDir, "show", branchTip+":audit/1.1.md")
	if !strings.Contains(onBranch, "verdict: pass") {
		t.Fatalf("audit file not present on build branch:\n%s", onBranch)
	}

	if got := gitRevParse(t, repoDir, "main"); got != mainBefore {
		t.Fatalf("main moved: before=%s after=%s", mainBefore, got)
	}
	for _, c := range rec.Log {
		if err := rejectForbiddenGitArgs(c.Args); err != nil {
			t.Fatalf("unsafe git argv recorded: %v (%v)", c.Args, err)
		}
		if len(c.Args) > 0 && c.Args[0] == "push" {
			t.Fatalf("push in argv: %v", c.Args)
		}
	}
}

func TestAuditorNeverTouchesMain(t *testing.T) {
	repoDir := initBareishRepo(t)
	planRel := "plans/phase-1/1.1-x.md"
	mustWrite(t, filepath.Join(repoDir, planRel), "# plan\n")
	setupBuildBranch(t, repoDir, "build/phase-1", "1.1", map[string]string{"src/prod.go": "package src\n"})
	mainBefore := gitRevParse(t, repoDir, "main")

	rec := &RecordingGit{Inner: defaultImplementerGitRunner(exec.LookPath)}
	au, err := NewAuditor(AuditorOptions{
		Enabled:    true,
		Repos:      []config.RepoConfig{{Name: "demo", Path: repoDir}},
		DataDir:    t.TempDir(),
		RunTimeout: time.Minute,
		DB:         openBuilderDB(t),
		Events:     &fakeEvents{},
		Git:        rec.Run,
		Claude: func(ctx context.Context, opts ClaudeSessionOpts) (ClaudeSessionResult, error) {
			if err := os.WriteFile(filepath.Join(opts.WorkDir, "src", "prod_test.go"), []byte("package src\n"), 0o644); err != nil {
				return ClaudeSessionResult{}, err
			}
			return ClaudeSessionResult{Outcome: OutcomeOK, Stdout: verdictStdout("pass")}, nil
		},
		Playwright: func(ctx context.Context, opts PlaywrightOpts) (PlaywrightResult, error) {
			return PlaywrightResult{Skipped: true}, nil
		},
		NewID: seqIDs("a"),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = au.Run(context.Background(), AuditPayload{
		Repo: "demo", Subphase: "1.1", Branch: "build/phase-1", PlanFile: planRel,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := gitRevParse(t, repoDir, "main"); got != mainBefore {
		t.Fatalf("main moved: before=%s after=%s", mainBefore, got)
	}
	for _, c := range rec.Log {
		if err := rejectForbiddenGitArgs(c.Args); err != nil {
			t.Fatalf("unsafe git argv: %v (%v)", c.Args, err)
		}
		if len(c.Args) >= 2 && (c.Args[0] == "checkout" || c.Args[0] == "switch") {
			for _, a := range c.Args[1:] {
				if a == "main" || a == "master" {
					t.Fatalf("checkout/switch main: %v", c.Args)
				}
			}
		}
	}
	if err := rejectForbiddenGitArgs([]string{"push", "origin", "main"}); err == nil {
		t.Fatal("want refuse push")
	}
	if err := rejectForbiddenGitArgs([]string{"update-ref", "refs/heads/main", "deadbeef"}); err == nil {
		t.Fatal("want refuse update-ref main")
	}
}

// --- timeout / usage-limit: same distinguishable outcomes as Implementer ---

func TestAuditorTimeoutAndUsageLimitOutcomes(t *testing.T) {
	cases := []struct {
		name      string
		claudeRes ClaudeSessionResult
		claudeErr error
		wantOut   string
		wantErr   error
	}{
		{
			name:      "timeout",
			claudeRes: ClaudeSessionResult{Outcome: OutcomeTimeout},
			claudeErr: ErrRunTimeout,
			wantOut:   OutcomeTimeout,
			wantErr:   ErrRunTimeout,
		},
		{
			name:      "usage_limit",
			claudeRes: ClaudeSessionResult{Outcome: OutcomeUsageLimit},
			claudeErr: ErrUsageLimit,
			wantOut:   OutcomeUsageLimit,
			wantErr:   ErrUsageLimit,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repoDir := initBareishRepo(t)
			planRel := "plans/phase-1/1.1-x.md"
			mustWrite(t, filepath.Join(repoDir, planRel), "# plan\n")
			setupBuildBranch(t, repoDir, "build/phase-1", "1.1", map[string]string{"src/prod.go": "package src\n"})

			au, err := NewAuditor(AuditorOptions{
				Enabled:    true,
				Repos:      []config.RepoConfig{{Name: "demo", Path: repoDir}},
				DataDir:    t.TempDir(),
				RunTimeout: time.Minute,
				DB:         openBuilderDB(t),
				Events:     &fakeEvents{},
				Claude: func(ctx context.Context, opts ClaudeSessionOpts) (ClaudeSessionResult, error) {
					return tc.claudeRes, tc.claudeErr
				},
				NewID: seqIDs("a"),
			})
			if err != nil {
				t.Fatal(err)
			}
			res, err := au.Run(context.Background(), AuditPayload{
				Repo: "demo", Subphase: "1.1", Branch: "build/phase-1", PlanFile: planRel,
			})
			if res == nil || res.Outcome != tc.wantOut {
				t.Fatalf("outcome = %+v, want %q", res, tc.wantOut)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want errors.Is %v", err, tc.wantErr)
			}
		})
	}
}

func TestIsAuditOrTestPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"audit/1.1.md", true},
		{"audit/1.1.txt", false},
		{"internal/builder/auditor_test.go", true},
		{"internal/builder/auditor.go", false},
		{"web/src/App.test.tsx", true},
		{"web/src/App.spec.ts", true},
		{"web/src/App.tsx", false},
		{"media-tools/tests/test_tts.py", true},
		{"media-tools/mediatools/tts_test.py", true},
		{"media-tools/mediatools/tts.py", false},
		{"src/__tests__/x.js", true},
	}
	for _, c := range cases {
		if got := isAuditOrTestPath(c.path); got != c.want {
			t.Errorf("isAuditOrTestPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestParseAuditVerdict(t *testing.T) {
	t.Run("valid pass", func(t *testing.T) {
		r := parseAuditVerdict(verdictStdout("pass"), "1.1")
		if r.Verdict != VerdictPass || len(r.Findings) != 0 {
			t.Fatalf("got %+v", r)
		}
	})
	t.Run("valid fail with findings", func(t *testing.T) {
		stdout := "notes\n```json\n{\"verdict\":\"fail\",\"findings\":[{\"severity\":\"major\",\"file\":\"x.go\",\"summary\":\"missing test\"}]}\n```\n"
		r := parseAuditVerdict(stdout, "1.2")
		if r.Verdict != VerdictFail || len(r.Findings) != 1 || r.Findings[0].Severity != "major" {
			t.Fatalf("got %+v", r)
		}
	})
	t.Run("no verdict block defaults to fail", func(t *testing.T) {
		r := parseAuditVerdict("just some prose, no json", "1.3")
		if r.Verdict != VerdictFail || len(r.Findings) == 0 {
			t.Fatalf("want default-fail with a finding, got %+v", r)
		}
	})
	t.Run("unparseable block defaults to fail", func(t *testing.T) {
		r := parseAuditVerdict("```json\n{not json}\n```", "1.4")
		if r.Verdict != VerdictFail || len(r.Findings) == 0 {
			t.Fatalf("want default-fail with a finding, got %+v", r)
		}
	})
}
