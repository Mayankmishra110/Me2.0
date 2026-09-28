package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"mayank2/internal/config"
	"mayank2/internal/events"
	"mayank2/internal/queue"
)

func TestGateDisabledNoOp(t *testing.T) {
	g, err := NewGate(GateOptions{
		Enabled: false,
		DB:      openBuilderDB(t),
		Events:  &fakeEvents{},
		Enqueue: &fakeEnqueuer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := g.Run(context.Background(), GatePayload{Repo: "anything"})
	if err != nil {
		t.Fatalf("disabled must no-op: %v", err)
	}
	if res == nil || !res.Skipped || res.Decision != DecisionSkipped {
		t.Fatalf("want Skipped/DecisionSkipped, got %+v", res)
	}
}

func TestGateRepoNotListed(t *testing.T) {
	g, err := NewGate(GateOptions{
		Enabled: true,
		Repos:   []config.RepoConfig{{Name: "allowed", Path: t.TempDir()}},
		DB:      openBuilderDB(t),
		Events:  &fakeEvents{},
		Enqueue: &fakeEnqueuer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = g.Run(context.Background(), GatePayload{Repo: "other", Subphase: "1.1"})
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

// fakeGitShow returns a GitRunner that answers `git show <branch>:<path>`
// with a fixed body, regardless of dir, and errors on any other invocation
// -- Gate is never expected to run any other git subcommand.
func fakeGitShow(body string) GitRunner {
	return func(ctx context.Context, dir string, args ...string) (string, error) {
		if len(args) == 2 && args[0] == "show" {
			return body, nil
		}
		return "", fmt.Errorf("fakeGitShow: unexpected git args %v", args)
	}
}

func TestGatePassMarksGated(t *testing.T) {
	_, body, err := renderAuditFile(config.RepoConfig{}, AuditReport{
		Subphase: "1.1", Verdict: VerdictPass, Findings: []AuditFinding{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ev := &fakeEvents{}
	db := openBuilderDB(t)
	g, err := NewGate(GateOptions{
		Enabled: true,
		Repos:   []config.RepoConfig{{Name: "repo1", Path: t.TempDir()}},
		DB:      db,
		Events:  ev,
		Enqueue: &fakeEnqueuer{},
		Git:     fakeGitShow(body),
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := g.Run(context.Background(), GatePayload{
		Repo: "repo1", Phase: "1", Subphase: "1.1", Branch: "build/phase-1", BuildID: "b1",
		Outcome: OutcomeOK, Verdict: VerdictPass,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Decision != DecisionGated {
		t.Fatalf("want gated, got %+v", res)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM builds WHERE repo='repo1' AND subphase='1.1' ORDER BY rowid DESC LIMIT 1`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != gateStatusGated {
		t.Fatalf("want builds.status=gated, got %q", status)
	}
	if len(ev.calls) != 1 || ev.calls[0].Kind != KindSubphaseGated {
		t.Fatalf("want KindSubphaseGated event, got %+v", ev.calls)
	}
}

func TestGateFailRetriesWithIncrementedAttempt(t *testing.T) {
	_, body, err := renderAuditFile(config.RepoConfig{}, AuditReport{
		Subphase: "1.2", Verdict: VerdictFail,
		Findings: []AuditFinding{{Severity: "blocker", Summary: "missing test", File: "foo.go"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	enq := &fakeEnqueuer{}
	ev := &fakeEvents{}
	g, err := NewGate(GateOptions{
		Enabled: true, MaxFixAttempts: 3,
		Repos:   []config.RepoConfig{{Name: "repo1", Path: t.TempDir()}},
		DB:      openBuilderDB(t),
		Events:  ev,
		Enqueue: enq,
		Git:     fakeGitShow(body),
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := g.Run(context.Background(), GatePayload{
		Repo: "repo1", Phase: "1", Subphase: "1.2", Branch: "build/phase-1", BuildID: "b2",
		Outcome: OutcomeOK, Verdict: VerdictFail,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Decision != DecisionRetry || res.Attempts != 1 {
		t.Fatalf("want retry attempt 1, got %+v", res)
	}
	calls := enq.snapshot()
	if len(calls) != 1 || calls[0].JobType != JobImplement {
		t.Fatalf("want one builder.implement re-enqueue, got %+v", calls)
	}
	ip, ok := calls[0].Payload.(ImplementPayload)
	if !ok {
		t.Fatalf("payload type = %T", calls[0].Payload)
	}
	if !strings.Contains(ip.Findings, "missing test") {
		t.Fatalf("want findings text to include summary, got %q", ip.Findings)
	}
	if len(ev.calls) != 1 || ev.calls[0].Kind != KindSubphaseRetry {
		t.Fatalf("want KindSubphaseRetry event, got %+v", ev.calls)
	}
}

func TestGateRetriesExhaustedDeadAndAlert(t *testing.T) {
	db := openBuilderDB(t)
	// Seed 3 prior fix_retry attempts (as Gate itself would have written on
	// three earlier failed audits for this subphase).
	if _, err := db.Exec(`
INSERT INTO builds (id, repo, phase, subphase, thread, status, attempts)
VALUES ('seed1', 'repo1', '1', '1.3', 'implementer', 'fix_retry', 3)`); err != nil {
		t.Fatal(err)
	}
	_, body, err := renderAuditFile(config.RepoConfig{}, AuditReport{Subphase: "1.3", Verdict: VerdictFail})
	if err != nil {
		t.Fatal(err)
	}
	enq := &fakeEnqueuer{}
	ev := &fakeEvents{}
	g, err := NewGate(GateOptions{
		Enabled: true, MaxFixAttempts: 3,
		Repos:   []config.RepoConfig{{Name: "repo1", Path: t.TempDir()}},
		DB:      db,
		Events:  ev,
		Enqueue: enq,
		Git:     fakeGitShow(body),
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := g.Run(context.Background(), GatePayload{
		Repo: "repo1", Phase: "1", Subphase: "1.3", Branch: "build/phase-1",
		Outcome: OutcomeOK, Verdict: VerdictFail,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Decision != DecisionDead || res.Attempts != 4 {
		t.Fatalf("want dead at attempt 4 (exceeds max_fix_attempts=3), got %+v", res)
	}
	if calls := enq.snapshot(); len(calls) != 0 {
		t.Fatalf("must not re-enqueue once dead, got %+v", calls)
	}
	found := false
	for _, c := range ev.calls {
		if c.Kind == events.KindAlert {
			found = true
		}
	}
	if !found {
		t.Fatalf("want an %q event, got %+v", events.KindAlert, ev.calls)
	}
}

func TestGateTimeoutBoundedRetry(t *testing.T) {
	enq := &fakeEnqueuer{}
	ev := &fakeEvents{}
	g, err := NewGate(GateOptions{
		Enabled: true, MaxFixAttempts: 2, RunTimeout: 45 * time.Minute,
		Repos:   []config.RepoConfig{{Name: "repo1", Path: t.TempDir()}},
		DB:      openBuilderDB(t),
		Events:  ev,
		Enqueue: enq,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Timeout never reaches the audit-file read path.
	res, err := g.Run(context.Background(), GatePayload{
		Repo: "repo1", Phase: "1", Subphase: "1.4", Branch: "build/phase-1",
		Thread: "implementer", Outcome: OutcomeTimeout,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Decision != DecisionRetry || res.Attempts != 1 {
		t.Fatalf("want bounded retry attempt 1 on timeout, got %+v", res)
	}
	if !strings.Contains(res.Reason, "run_timeout") {
		t.Fatalf("want reason to mention run_timeout, got %q", res.Reason)
	}
	calls := enq.snapshot()
	if len(calls) != 1 || calls[0].JobType != JobImplement {
		t.Fatalf("want builder.implement re-enqueue, got %+v", calls)
	}

	// A second, third... timeout still retries up to MaxFixAttempts, then dies.
	res2, err := g.Run(context.Background(), GatePayload{
		Repo: "repo1", Phase: "1", Subphase: "1.4", Branch: "build/phase-1",
		Thread: "implementer", Outcome: OutcomeTimeout,
	})
	if err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	if res2.Decision != DecisionRetry || res2.Attempts != 2 {
		t.Fatalf("want retry attempt 2, got %+v", res2)
	}
	res3, err := g.Run(context.Background(), GatePayload{
		Repo: "repo1", Phase: "1", Subphase: "1.4", Branch: "build/phase-1",
		Thread: "implementer", Outcome: OutcomeTimeout,
	})
	if err != nil {
		t.Fatalf("Run 3: %v", err)
	}
	if res3.Decision != DecisionDead || res3.Attempts != 3 {
		t.Fatalf("want dead once timeouts exceed max_fix_attempts=2, got %+v", res3)
	}
}

func TestGateUsageLimitPausesAndResumes(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	enq := &fakeEnqueuer{}
	ev := &fakeEvents{}
	db := openBuilderDB(t)
	g, err := NewGate(GateOptions{
		Enabled: true, MaxFixAttempts: 3, LimitPause: 30 * time.Minute,
		Repos:   []config.RepoConfig{{Name: "repo1", Path: t.TempDir()}},
		DB:      db,
		Events:  ev,
		Enqueue: enq,
		Now:     fixedNow(now),
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := g.Run(context.Background(), GatePayload{
		Repo: "repo1", Phase: "1", Subphase: "1.5", Branch: "build/phase-1",
		Thread: "auditor", Outcome: OutcomeUsageLimit,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Decision != DecisionPaused {
		t.Fatalf("want paused, got %+v", res)
	}
	wantUntil := now.Add(30 * time.Minute).Format(time.RFC3339Nano)
	if res.PausedUntil != wantUntil {
		t.Fatalf("want paused_until %s, got %s", wantUntil, res.PausedUntil)
	}
	calls := enq.snapshot()
	if len(calls) != 1 || calls[0].JobType != JobAudit {
		t.Fatalf("want deferred builder.audit re-enqueue (thread=auditor hit the limit), got %+v", calls)
	}

	// A second, unrelated subphase failing DURING the pause window must
	// defer (both threads pause for the whole plan - D7) rather than spend
	// a fix attempt.
	_, body, err := renderAuditFile(config.RepoConfig{}, AuditReport{Subphase: "1.6", Verdict: VerdictFail})
	if err != nil {
		t.Fatal(err)
	}
	g.opts.Git = fakeGitShow(body)
	res2, err := g.Run(context.Background(), GatePayload{
		Repo: "repo1", Phase: "1", Subphase: "1.6", Branch: "build/phase-1",
		Outcome: OutcomeOK, Verdict: VerdictFail,
	})
	if err != nil {
		t.Fatalf("Run (during pause): %v", err)
	}
	if res2.Decision != DecisionPaused {
		t.Fatalf("want deferred subphase to also report paused, got %+v", res2)
	}
	n, err := g.maxAttempts(context.Background(), "repo1", "1", "1.6")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("paused defer must not spend a fix attempt, got attempts=%d", n)
	}

	// After the pause window elapses, the same subphase failing proceeds
	// normally (spends attempt 1) instead of deferring again.
	g.opts.Now = fixedNow(now.Add(31 * time.Minute))
	res3, err := g.Run(context.Background(), GatePayload{
		Repo: "repo1", Phase: "1", Subphase: "1.6", Branch: "build/phase-1",
		Outcome: OutcomeOK, Verdict: VerdictFail,
	})
	if err != nil {
		t.Fatalf("Run (after pause): %v", err)
	}
	if res3.Decision != DecisionRetry || res3.Attempts != 1 {
		t.Fatalf("want normal retry once pause has resumed, got %+v", res3)
	}
}

func TestGateSubscribeTriggeredByAuditDone(t *testing.T) {
	g, err := NewGate(GateOptions{
		Enabled: true,
		DB:      openBuilderDB(t),
		Events:  &fakeEvents{},
		Enqueue: &fakeEnqueuer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan events.Event, 1)
	bus := &fakeBus{ch: ch}
	enq := &fakeEnqueuer{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g.Subscribe(ctx, bus, enq)

	data, err := json.Marshal(auditDoneData{
		Repo: "repo1", Phase: "1", Subphase: "1.1", Branch: "build/phase-1",
		BuildID: "b1", Verdict: VerdictPass, AuditPath: "audit/1.1.md",
	})
	if err != nil {
		t.Fatal(err)
	}
	ch <- events.Event{Kind: KindAuditDone, Data: data}

	waitFor(t, time.Second, func() bool { return len(enq.snapshot()) == 1 })
	calls := enq.snapshot()
	if calls[0].JobType != JobGate {
		t.Fatalf("want builder.gate enqueue, got %+v", calls[0])
	}
	gp, ok := calls[0].Payload.(GatePayload)
	if !ok {
		t.Fatalf("payload type = %T", calls[0].Payload)
	}
	if gp.Repo != "repo1" || gp.Verdict != VerdictPass || gp.Outcome != OutcomeOK {
		t.Fatalf("unexpected payload: %+v", gp)
	}
}

func TestGateInvalidOutcomeRejected(t *testing.T) {
	g, err := NewGate(GateOptions{
		Enabled: true,
		Repos:   []config.RepoConfig{{Name: "repo1", Path: t.TempDir()}},
		DB:      openBuilderDB(t),
		Events:  &fakeEvents{},
		Enqueue: &fakeEnqueuer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = g.Run(context.Background(), GatePayload{
		Repo: "repo1", Subphase: "1.1", Outcome: "bogus",
	})
	if err == nil || !queue.IsPermanent(err) {
		t.Fatalf("want permanent error for unknown outcome, got %v", err)
	}
}
