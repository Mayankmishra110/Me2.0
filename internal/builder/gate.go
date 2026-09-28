// Gate (M2-504): builder.gate is the phase-gate decision job that runs after
// each builder.audit completes (Auditor's KindAuditDone event) and decides
// whether the plan proceeds to the next subphase, or whether the audit's
// findings go back to the Implementer as a bounded fix-and-retry loop
// (config.builder.max_fix_attempts). It also owns the two cross-cutting
// concerns of the whole Builder pipeline (ARCHITECTURE §3.3):
//   - a bare Implementer/Auditor run_timeout is treated as a bounded-retry
//     failure, same budget as an audit "fail" verdict;
//   - a detected Claude Pro usage-limit hit pauses BOTH threads for the
//     whole plan (config.builder.limit_pause), scoped to this repo+phase via
//     a `settings` row (ARCHITECTURE §5 "pause flags" — same table, a
//     narrower key than queue.Queue's PauseAll/PauseResource, which are
//     deliberately not used here per the ticket: those are blanket/
//     resource-class pauses, not plan-scoped).
//
// Signal reuse (ticket instruction: "consume that signal here rather than
// inventing a new mechanism"): Outcome values (OutcomeOK/OutcomeTimeout/
// OutcomeUsageLimit) are implementer.go's, reused verbatim — no new outcome
// vocabulary. GatePayload.Outcome carries whichever of those the triggering
// context observed. The normal path — Subscribe, wired to KindAuditDone — always
// sets Outcome=OutcomeOK, because that event only fires once Auditor.Run has
// actually produced a verdict (a timeout/usage-limit return in Auditor.Run
// or Implementer.Run happens BEFORE any builds row or event is emitted, see
// those files' Run — there is nothing yet to subscribe to on that path). A
// daemon-wiring layer that observes ErrRunTimeout/ErrUsageLimit directly
// from Implementer.Run/Auditor.Run (that wiring does not exist yet — no
// builder job type is registered anywhere in this repo; grep confirms
// cmd/mayank2 does not reference JobImplement/JobAudit/JobGate) is expected
// to enqueue builder.gate with Outcome set accordingly instead of retrying
// blindly itself, keeping exactly one place (this file) owning the
// retry/pause decision, per the ticket's "Also owns cross-cutting
// timeout/pause behavior" framing. That wiring is out of this ticket's
// touches (internal/builder/gate.go only); this file's tests exercise the
// decision logic directly against GatePayload, per the ticket Notes
// ("tests should exercise the gate logic against a fake/temp builds row
// rather than any real repo").
//
// Consolidated decision — non-fast-forward branch-landing conflicts
// (docs/CONTEXT.md §5, previously open question left by M2-503, referencing
// M2-502's related restoreMainRef pattern): Gate does NOT retry those.
// auditor.go's fastForwardBranch already documents that a real concurrent
// branch move (not a rebase mechanic) returns a plain retryable error from
// the *queue's* perspective — i.e. the builder.audit job itself gets
// re-run by the queue's ordinary backoff, using its own worktree's rebase
// machinery, which is the only place with the local, already-rebased state
// needed to retry safely. Gate only ever observes an audit AFTER it has
// already landed (KindAuditDone / a readable audit/N.M.md on the branch);
// a landing conflict is resolved or re-attempted entirely inside
// auditor.go/implementer.go before Gate is ever invoked, so it never
// reaches Gate as a distinguishable outcome — there is nothing for this
// job to retry that the queue isn't already retrying. Centralizing that
// retry in Gate instead would require Gate to hold a worktree and drive
// git itself, duplicating auditor.go's mechanism for no benefit. This
// resolves both CONTEXT.md §5 "open question" rows about it (they are
// consolidated into one, marked resolved, in the same commit as this file).
package builder

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"mayank2/internal/config"
	"mayank2/internal/events"
	"mayank2/internal/queue"
)

// JobGate is the SPEC §5 job type for one phase-gate decision run.
const JobGate = "builder.gate"

// KindSubphaseGated is emitted when a subphase's audit verdict is "pass"
// and the subphase is marked gated/complete. M2-505's PR step subscribes to
// this (or polls builds.status='gated') to know when every subphase in a
// plan is done.
const KindSubphaseGated = "subphase.gated"

// KindSubphaseRetry is emitted each time Gate sends a subphase back to the
// Implementer (or Auditor, for a timed-out audit) for another attempt.
const KindSubphaseRetry = "subphase.retry"

// KindPlanPaused is emitted when a usage-limit hit pauses a repo+phase.
const KindPlanPaused = "build.paused"

// Gate decision outcomes (GateResult.Decision).
const (
	DecisionGated   = "gated"
	DecisionRetry   = "retry"
	DecisionDead    = "dead"
	DecisionPaused  = "paused"
	DecisionSkipped = "skipped"
)

// builds.status values this job writes (in addition to "implemented" /
// "audited" written by implementer.go / auditor.go).
const (
	gateStatusGated    = "gated"
	gateStatusFixRetry = "fix_retry"
	gateStatusDead     = "dead"
)

// GatePayload is the builder.gate job payload. The normal trigger
// (Subscribe, off KindAuditDone) always sets Outcome=OutcomeOK and Verdict;
// a timeout/usage-limit-triggered payload sets Outcome instead and leaves
// Verdict empty (see package doc comment above).
type GatePayload struct {
	Repo      string `json:"repo"`
	Phase     string `json:"phase,omitempty"`
	Subphase  string `json:"subphase"`
	Branch    string `json:"branch,omitempty"`
	BuildID   string `json:"build_id,omitempty"`
	Thread    string `json:"thread,omitempty"`  // "implementer" | "auditor" - which run this decision is about
	Outcome   string `json:"outcome,omitempty"` // OutcomeOK (default) | OutcomeTimeout | OutcomeUsageLimit
	Verdict   string `json:"verdict,omitempty"` // audit verdict, only meaningful when Outcome==OutcomeOK
	AuditPath string `json:"audit_path,omitempty"`
}

// GateResult is the builder.gate job result.
type GateResult struct {
	Repo        string `json:"repo"`
	Phase       string `json:"phase"`
	Subphase    string `json:"subphase"`
	Decision    string `json:"decision"`
	Attempts    int    `json:"attempts,omitempty"`
	MaxAttempts int    `json:"max_attempts,omitempty"`
	Verdict     string `json:"verdict,omitempty"`
	PausedUntil string `json:"paused_until,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Skipped     bool   `json:"skipped,omitempty"`
	EventID     string `json:"event_id,omitempty"`
}

// GateOptions configures Gate.
type GateOptions struct {
	Enabled        bool
	Repos          []config.RepoConfig
	MaxFixAttempts int
	RunTimeout     time.Duration
	LimitPause     time.Duration
	DB             DB
	Events         EventEmitter
	Enqueue        Enqueuer
	Now            func() time.Time
	NewID          func() string
	Logger         *slog.Logger
	Git            GitRunner
	// LookPath resolves binaries (default exec.LookPath), used only to build
	// the default GitRunner.
	LookPath func(file string) (string, error)
}

// Gate implements the builder.gate job.
type Gate struct {
	opts GateOptions
	log  *slog.Logger
}

// NewGate returns a Gate. DB/Events/Enqueue are required even when Enabled
// is false, matching implementer.go/auditor.go's honesty-in-wiring
// convention.
func NewGate(opts GateOptions) (*Gate, error) {
	if opts.DB == nil {
		return nil, fmt.Errorf("builder: Gate: DB is required")
	}
	if opts.Events == nil {
		return nil, fmt.Errorf("builder: Gate: Events is required")
	}
	if opts.Enqueue == nil {
		return nil, fmt.Errorf("builder: Gate: Enqueue is required")
	}
	if opts.MaxFixAttempts <= 0 {
		opts.MaxFixAttempts = 3
	}
	if opts.RunTimeout <= 0 {
		opts.RunTimeout = 60 * time.Minute
	}
	if opts.LimitPause <= 0 {
		opts.LimitPause = 30 * time.Minute
	}
	if opts.LookPath == nil {
		opts.LookPath = exec.LookPath
	}
	if opts.Git == nil {
		opts.Git = defaultImplementerGitRunner(opts.LookPath)
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Gate{opts: opts, log: log}, nil
}

func (g *Gate) now() time.Time {
	if g.opts.Now != nil {
		return g.opts.Now().UTC()
	}
	return time.Now().UTC()
}

func (g *Gate) newID() string {
	if g.opts.NewID != nil {
		return g.opts.NewID()
	}
	return newULID(g.now)
}

// Handler adapts Gate to queue.Handler.
func (g *Gate) Handler() queue.Handler {
	return func(ctx context.Context, job queue.Job) (json.RawMessage, error) {
		var payload GatePayload
		if len(job.Payload) > 0 && string(job.Payload) != "{}" {
			if err := json.Unmarshal(job.Payload, &payload); err != nil {
				return nil, queue.Permanent(fmt.Errorf("builder: gate: bad payload: %w", err))
			}
		}
		res, err := g.Run(ctx, payload)
		if res == nil && err != nil {
			return nil, err
		}
		if res == nil {
			res = &GateResult{Decision: DecisionSkipped, Skipped: true}
		}
		out, mErr := json.Marshal(res)
		if mErr != nil {
			return nil, fmt.Errorf("builder: gate: marshal result: %w", mErr)
		}
		if err != nil {
			return out, err
		}
		return out, nil
	}
}

// auditDoneData mirrors the "data" map auditor.go's Run passes to
// Events.Emit for KindAuditDone.
type auditDoneData struct {
	Repo      string `json:"repo"`
	Phase     string `json:"phase"`
	Subphase  string `json:"subphase"`
	Branch    string `json:"branch"`
	Worktree  string `json:"worktree"`
	BuildID   string `json:"build_id"`
	Verdict   string `json:"verdict"`
	AuditPath string `json:"audit_path"`
}

// Subscribe listens on bus for audit.done events and enqueues one
// builder.gate job per event, so Gate is triggered by the Auditor's
// completed runs rather than polled (same pattern as auditor.go's
// Subscribe off subphase.done).
func (g *Gate) Subscribe(ctx context.Context, bus EventSubscriber, enqueue Enqueuer) {
	ch, cancel := bus.Subscribe(ctx)
	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				if ev.Kind != KindAuditDone {
					continue
				}
				var data auditDoneData
				if err := json.Unmarshal(ev.Data, &data); err != nil {
					g.log.Warn("builder: gate: audit.done event has unparseable data", "err", err)
					continue
				}
				payload := GatePayload{
					Repo:      data.Repo,
					Phase:     data.Phase,
					Subphase:  data.Subphase,
					Branch:    data.Branch,
					BuildID:   data.BuildID,
					Thread:    "auditor",
					Outcome:   OutcomeOK,
					Verdict:   data.Verdict,
					AuditPath: data.AuditPath,
				}
				if _, err := enqueue.Enqueue(ctx, JobGate, payload); err != nil {
					g.log.Error("builder: gate: enqueue from audit.done failed", "err", err)
				}
			}
		}
	}()
}

// Run executes builder.gate for one decision.
func (g *Gate) Run(ctx context.Context, payload GatePayload) (*GateResult, error) {
	if !g.opts.Enabled {
		g.log.Info("builder: gate: skipped (builder.enabled=false)")
		return &GateResult{Decision: DecisionSkipped, Skipped: true}, nil
	}

	repoName := strings.TrimSpace(payload.Repo)
	if repoName == "" {
		return nil, queue.Permanent(fmt.Errorf("builder: gate: repo is required (must match config.builder.repos[].name)"))
	}
	repo, ok := g.findRepo(repoName)
	if !ok {
		return nil, queue.Permanent(fmt.Errorf("builder: gate: repo %q is not in config.builder.repos", repoName))
	}

	subphase := strings.TrimSpace(payload.Subphase)
	if subphase == "" {
		return nil, queue.Permanent(fmt.Errorf("builder: gate: subphase is required (e.g. \"1.1\")"))
	}
	phase := strings.TrimSpace(payload.Phase)
	if phase == "" {
		phase = strings.SplitN(subphase, ".", 2)[0]
	}
	if !phaseNumRe.MatchString(phase) {
		return nil, queue.Permanent(fmt.Errorf("builder: gate: invalid phase %q (want digits)", phase))
	}
	branch := strings.TrimSpace(payload.Branch)
	if branch == "" {
		branch = "build/phase-" + phase
	}

	outcome := strings.TrimSpace(payload.Outcome)
	if outcome == "" {
		outcome = OutcomeOK
	}

	switch outcome {
	case OutcomeUsageLimit:
		return g.handleUsageLimit(ctx, repo, phase, subphase, branch, payload)
	case OutcomeTimeout:
		reason := fmt.Sprintf("run_timeout exceeded (%s)", g.opts.RunTimeout)
		return g.handleFailure(ctx, repo, phase, subphase, branch, payload, reason)
	case OutcomeOK:
		return g.handleAuditVerdict(ctx, repo, phase, subphase, branch, payload)
	default:
		return nil, queue.Permanent(fmt.Errorf("builder: gate: unknown outcome %q", outcome))
	}
}

func (g *Gate) findRepo(name string) (config.RepoConfig, bool) {
	for _, r := range g.opts.Repos {
		if r.Name == name {
			return r, true
		}
	}
	return config.RepoConfig{}, false
}

// handleAuditVerdict is the normal path: read the authoritative verdict
// from audit/N.M.md (AC: "Reads the audit verdict from audit/N.M.md"),
// falling back to payload.Verdict only when the file can't be read (e.g. a
// test driving Gate directly without a git repo behind it).
func (g *Gate) handleAuditVerdict(ctx context.Context, repo config.RepoConfig, phase, subphase, branch string, payload GatePayload) (*GateResult, error) {
	report, err := g.readAuditReport(ctx, repo, branch, payload.AuditPath, subphase)
	if err != nil {
		v := strings.ToLower(strings.TrimSpace(payload.Verdict))
		if v == "" {
			return nil, fmt.Errorf("builder: gate: read audit report and no payload.verdict fallback: %w", err)
		}
		report = AuditReport{Subphase: subphase, Verdict: v}
	}

	if report.Verdict == VerdictPass {
		return g.markGated(ctx, repo.Name, phase, subphase, payload.BuildID)
	}

	reason := findingsSummary(report.Findings)
	if reason == "" {
		reason = "audit verdict: fail"
	}
	res, err := g.handleFailure(ctx, repo, phase, subphase, branch, payload, reason)
	if err != nil {
		return nil, err
	}
	res.Verdict = VerdictFail
	return res, nil
}

// readAuditReport reads audit/N.M.md straight from the build branch via
// `git show <branch>:<path>` — a read-only blob read, no checkout, using
// the same fixed-argv GitRunner (and rejectForbiddenGitArgs gate) as
// implementer.go/auditor.go. Gate never holds its own worktree.
func (g *Gate) readAuditReport(ctx context.Context, repo config.RepoConfig, branch, auditPath, subphase string) (AuditReport, error) {
	path := strings.TrimSpace(auditPath)
	if path == "" {
		path = "audit/" + subphase + ".md"
	}
	out, err := g.opts.Git(ctx, repo.Path, "show", branch+":"+path)
	if err != nil {
		return AuditReport{}, fmt.Errorf("git show %s:%s: %w", branch, path, err)
	}
	return parseAuditReport(out)
}

// parseAuditReport extracts the YAML frontmatter renderAuditFile (auditor.go)
// writes: "---\n<yaml>\n---\n<body>".
func parseAuditReport(body string) (AuditReport, error) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	if !strings.HasPrefix(body, "---\n") {
		return AuditReport{}, fmt.Errorf("audit file missing frontmatter")
	}
	rest := body[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return AuditReport{}, fmt.Errorf("audit file frontmatter not closed")
	}
	var report AuditReport
	if err := yaml.Unmarshal([]byte(rest[:end]), &report); err != nil {
		return AuditReport{}, fmt.Errorf("parse frontmatter: %w", err)
	}
	report.Verdict = strings.ToLower(strings.TrimSpace(report.Verdict))
	return report, nil
}

func findingsSummary(findings []AuditFinding) string {
	if len(findings) == 0 {
		return ""
	}
	var b strings.Builder
	for _, f := range findings {
		if strings.TrimSpace(f.File) != "" {
			fmt.Fprintf(&b, "- [%s] %s (%s)\n", f.Severity, f.Summary, f.File)
		} else {
			fmt.Fprintf(&b, "- [%s] %s\n", f.Severity, f.Summary)
		}
	}
	return truncateRunes(b.String(), 12_000)
}

// markGated records the subphase as gated/complete and emits
// KindSubphaseGated. A fresh builds row (thread='auditor', matching the
// CHECK constraint) is appended rather than mutating an existing row: the
// implementer/auditor insert path already appends one row per run, and
// gate.go follows the same append-only convention so it never has to guess
// which of several prior rows for this (repo, phase, subphase) "is" the
// canonical one.
func (g *Gate) markGated(ctx context.Context, repoName, phase, subphase, buildID string) (*GateResult, error) {
	if err := g.insertBuildsRow(ctx, repoName, phase, subphase, "auditor", gateStatusGated, 0, ""); err != nil {
		return nil, err
	}
	ev, err := g.opts.Events.Emit(ctx, events.ActorAgent, KindSubphaseGated, buildID,
		fmt.Sprintf("subphase %s gated pass on %s", subphase, repoName),
		map[string]any{
			"repo": repoName, "phase": phase, "subphase": subphase, "build_id": buildID,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("builder: gate: emit subphase.gated: %w", err)
	}
	return &GateResult{
		Repo: repoName, Phase: phase, Subphase: subphase,
		Decision: DecisionGated, Verdict: VerdictPass, EventID: ev.ID,
	}, nil
}

// handleFailure is the shared bounded-retry path for both an audit "fail"
// verdict and a run_timeout outcome (AC: "a run that merely timed out is
// not automatically a plan-ending failure on its first occurrence" — same
// budget as an audit fail). It also honors an in-progress plan-level pause
// (checkPause) before spending an attempt or re-enqueueing anything, so an
// unrelated subphase's retry never races ahead of a paused plan (D7).
func (g *Gate) handleFailure(ctx context.Context, repo config.RepoConfig, phase, subphase, branch string, payload GatePayload, reason string) (*GateResult, error) {
	if until, paused, err := g.checkPause(ctx, repo.Name, phase); err != nil {
		return nil, fmt.Errorf("builder: gate: check pause: %w", err)
	} else if paused {
		thread := deriveThread(payload.Thread)
		if _, err := g.reEnqueue(ctx, thread, repo.Name, phase, subphase, branch, payload.BuildID, reason, &until); err != nil {
			return nil, fmt.Errorf("builder: gate: defer during pause: %w", err)
		}
		return &GateResult{
			Repo: repo.Name, Phase: phase, Subphase: subphase,
			Decision: DecisionPaused, PausedUntil: until.Format(time.RFC3339Nano), Reason: reason,
		}, nil
	}

	prior, err := g.maxAttempts(ctx, repo.Name, phase, subphase)
	if err != nil {
		return nil, fmt.Errorf("builder: gate: read attempts: %w", err)
	}
	attempt := prior + 1
	thread := deriveThread(payload.Thread)

	if attempt > g.opts.MaxFixAttempts {
		if err := g.insertBuildsRow(ctx, repo.Name, phase, subphase, thread, gateStatusDead, attempt, reason); err != nil {
			return nil, err
		}
		if err := g.emitDeadAlert(ctx, repo.Name, phase, subphase, attempt, reason); err != nil {
			return nil, err
		}
		return &GateResult{
			Repo: repo.Name, Phase: phase, Subphase: subphase,
			Decision: DecisionDead, Attempts: attempt, MaxAttempts: g.opts.MaxFixAttempts, Reason: reason,
		}, nil
	}

	if err := g.insertBuildsRow(ctx, repo.Name, phase, subphase, thread, gateStatusFixRetry, attempt, reason); err != nil {
		return nil, err
	}
	jobID, err := g.reEnqueue(ctx, thread, repo.Name, phase, subphase, branch, payload.BuildID, reason, nil)
	if err != nil {
		return nil, fmt.Errorf("builder: gate: re-enqueue: %w", err)
	}
	ev, err := g.opts.Events.Emit(ctx, events.ActorAgent, KindSubphaseRetry, payload.BuildID,
		fmt.Sprintf("subphase %s retry %d/%d on %s: %s", subphase, attempt, g.opts.MaxFixAttempts, repo.Name, reason),
		map[string]any{
			"repo": repo.Name, "phase": phase, "subphase": subphase,
			"attempt": attempt, "job_id": jobID, "reason": reason, "thread": thread,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("builder: gate: emit subphase.retry: %w", err)
	}
	return &GateResult{
		Repo: repo.Name, Phase: phase, Subphase: subphase,
		Decision: DecisionRetry, Attempts: attempt, MaxAttempts: g.opts.MaxFixAttempts,
		Reason: reason, EventID: ev.ID,
	}, nil
}

// handleUsageLimit pauses the whole repo+phase (not just the thread that
// hit the limit - D7 parallelism means the other thread must also stop
// rather than race ahead unaudited) and schedules the paused run to resume
// automatically: it re-enqueues the same work with RunAt(until), so the
// queue's own run_at scheduling (ARCHITECTURE §5 claim query,
// "run_at<=now") does the "resume" for free once the window passes. A
// usage limit is not the implementation's fault, so it does not consume a
// fix-attempt.
func (g *Gate) handleUsageLimit(ctx context.Context, repo config.RepoConfig, phase, subphase, branch string, payload GatePayload) (*GateResult, error) {
	until := g.now().Add(g.opts.LimitPause)
	if err := g.setPause(ctx, repo.Name, phase, until); err != nil {
		return nil, fmt.Errorf("builder: gate: set pause: %w", err)
	}
	thread := deriveThread(payload.Thread)
	jobID, err := g.reEnqueue(ctx, thread, repo.Name, phase, subphase, branch, payload.BuildID, "", &until)
	if err != nil {
		return nil, fmt.Errorf("builder: gate: re-enqueue after pause: %w", err)
	}
	ev, err := g.opts.Events.Emit(ctx, events.ActorSystem, KindPlanPaused, repo.Name+":"+phase,
		fmt.Sprintf("builder: usage limit hit on %s (%s); pausing %s phase %s until %s",
			repo.Name, thread, repo.Name, phase, until.Format(time.RFC3339)),
		map[string]any{
			"repo": repo.Name, "phase": phase, "subphase": subphase,
			"until": until.Format(time.RFC3339Nano), "job_id": jobID, "thread": thread,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("builder: gate: emit build.paused: %w", err)
	}
	return &GateResult{
		Repo: repo.Name, Phase: phase, Subphase: subphase,
		Decision: DecisionPaused, PausedUntil: until.Format(time.RFC3339Nano), EventID: ev.ID,
	}, nil
}

// deriveThread validates payload.Thread against the builds.thread CHECK
// constraint ('implementer' | 'auditor'); an unset or unrecognized value
// defaults to 'implementer' since that is the usual retry target (the
// Implementer is who acts on fix findings).
func deriveThread(t string) string {
	switch t {
	case "implementer", "auditor":
		return t
	default:
		return "implementer"
	}
}

// maxAttempts returns the highest attempts value any builds row for
// (repo, phase, subphase) carries. Only Gate ever writes attempts > 0
// (implementer.go/auditor.go always insert with attempts=0 and never
// update it), so this is a safe, append-only counter: each retry inserts a
// new row with attempts = prior max + 1, with no need to identify or
// mutate "the" earlier row.
func (g *Gate) maxAttempts(ctx context.Context, repo, phase, subphase string) (int, error) {
	var max sql.NullInt64
	err := g.opts.DB.QueryRowContext(ctx, `
SELECT MAX(attempts) FROM builds WHERE repo = ? AND phase = ? AND subphase = ?`,
		repo, phase, subphase,
	).Scan(&max)
	if err != nil {
		return 0, err
	}
	if !max.Valid {
		return 0, nil
	}
	return int(max.Int64), nil
}

func (g *Gate) insertBuildsRow(ctx context.Context, repo, phase, subphase, thread, status string, attempts int, lastError string) error {
	var le any
	if strings.TrimSpace(lastError) != "" {
		le = lastError
	}
	_, err := g.opts.DB.ExecContext(ctx, `
INSERT INTO builds (id, repo, plan_path, phase, subphase, thread, status, branch, worktree, attempts, last_error)
VALUES (?, ?, '', ?, ?, ?, ?, '', '', ?, ?)`,
		g.newID(), repo, phase, subphase, thread, status, attempts, le,
	)
	if err != nil {
		return fmt.Errorf("builder: gate: insert builds (%s): %w", status, err)
	}
	return nil
}

// reEnqueue re-enqueues the failed/paused run's job for another attempt:
// builder.implement for thread="implementer", builder.audit for
// thread="auditor". runAt nil means "as soon as claimable" (ordinary
// retry); non-nil defers it (the pause path).
func (g *Gate) reEnqueue(ctx context.Context, thread, repo, phase, subphase, branch, buildID, findings string, runAt *time.Time) (string, error) {
	var opts []queue.EnqueueOpt
	if runAt != nil {
		opts = append(opts, queue.RunAt(*runAt))
	}
	if thread == "auditor" {
		payload := AuditPayload{Repo: repo, Phase: phase, Subphase: subphase, Branch: branch, BuildID: buildID}
		return g.opts.Enqueue.Enqueue(ctx, JobAudit, payload, opts...)
	}
	payload := ImplementPayload{Repo: repo, Phase: phase, Subphase: subphase, BuildID: buildID, Findings: findings}
	return g.opts.Enqueue.Enqueue(ctx, JobImplement, payload, opts...)
}

func (g *Gate) emitDeadAlert(ctx context.Context, repo, phase, subphase string, attempts int, reason string) error {
	_, err := g.opts.Events.Emit(ctx, events.ActorSystem, events.KindAlert, subphase,
		fmt.Sprintf("builder: subphase %s on %s exhausted %d fix attempts and is dead (%s)", subphase, repo, attempts, reason),
		map[string]any{
			"repo": repo, "phase": phase, "subphase": subphase,
			"attempts": attempts, "max_fix_attempts": g.opts.MaxFixAttempts, "reason": reason,
		},
	)
	if err != nil {
		return fmt.Errorf("builder: gate: emit alert: %w", err)
	}
	return nil
}

// pauseSettingKey scopes the pause flag to one repo+phase (a "plan"),
// deliberately narrower than queue.Queue's pause:all / pause:resource:<res>
// keys (queue/pause.go) so a usage-limit hit on one plan never stalls
// unrelated Builder plans or other job types sharing the same resource
// pool.
func pauseSettingKey(repo, phase string) string {
	return fmt.Sprintf("builder:pause:%s:%s", repo, phase)
}

type pauseSetting struct {
	Until string `json:"until"`
}

// checkPause reports whether repo+phase is currently paused. An expired
// pause row is lazily deleted and reported as not-paused, so callers never
// have to special-case "the timestamp is in the past".
func (g *Gate) checkPause(ctx context.Context, repo, phase string) (time.Time, bool, error) {
	var value string
	err := g.opts.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, pauseSettingKey(repo, phase)).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("read pause setting: %w", err)
	}
	var data pauseSetting
	if err := json.Unmarshal([]byte(value), &data); err != nil {
		return time.Time{}, false, fmt.Errorf("parse pause setting: %w", err)
	}
	until, err := time.Parse(time.RFC3339Nano, data.Until)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parse pause until: %w", err)
	}
	if !g.now().Before(until) {
		if _, err := g.opts.DB.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, pauseSettingKey(repo, phase)); err != nil {
			g.log.Warn("builder: gate: clear expired pause failed (non-fatal)", "repo", repo, "phase", phase, "err", err)
		}
		return time.Time{}, false, nil
	}
	return until, true, nil
}

func (g *Gate) setPause(ctx context.Context, repo, phase string, until time.Time) error {
	data, err := json.Marshal(pauseSetting{Until: until.Format(time.RFC3339Nano)})
	if err != nil {
		return err
	}
	_, err = g.opts.DB.ExecContext(ctx, `
INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		pauseSettingKey(repo, phase), string(data),
	)
	if err != nil {
		return fmt.Errorf("write pause setting: %w", err)
	}
	return nil
}
