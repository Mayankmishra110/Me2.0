package builder

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mayank2/internal/config"
	"mayank2/internal/db"
	"mayank2/internal/llm"
	"mayank2/internal/queue"
)

func TestPlannerDisabledNoOp(t *testing.T) {
	p, err := NewPlanner(PlannerOptions{
		Enabled:   false,
		Completer: &fakeCompleter{body: `{}`},
		DB:        openBuilderDB(t),
		Approvals: &fakeApproval{},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Run(context.Background(), PlanPayload{Repo: "anything"})
	if err != nil {
		t.Fatalf("disabled must no-op without error: %v", err)
	}
	if res == nil || !res.Skipped {
		t.Fatalf("want Skipped=true, got %+v", res)
	}
}

func TestPlannerRepoNotListed(t *testing.T) {
	p, err := NewPlanner(PlannerOptions{
		Enabled:   true,
		Repos:     []config.RepoConfig{{Name: "allowed", Path: t.TempDir()}},
		Completer: &fakeCompleter{body: `{}`},
		DB:        openBuilderDB(t),
		Approvals: &fakeApproval{},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Run(context.Background(), PlanPayload{Repo: "other"})
	if err == nil {
		t.Fatal("want error for repo not in list")
	}
	if !queue.IsPermanent(err) {
		t.Fatalf("want permanent error, got %v", err)
	}
	if !strings.Contains(err.Error(), "not in config.builder.repos") {
		t.Fatalf("want repos rejection message, got %v", err)
	}
}

func TestPlannerHappyPathWritesPlanAndApproval(t *testing.T) {
	repoDir := newFakeTargetRepo(t)
	sqlDB := openBuilderDB(t)
	fc := &fakeCompleter{body: samplePlanJSON()}
	fa := &fakeApproval{nextID: seqIDs("appr")}
	p, err := NewPlanner(PlannerOptions{
		Enabled:   true,
		Repos:     []config.RepoConfig{{Name: "demo", Path: repoDir, TicketsDir: "tickets"}},
		Completer: fc,
		DB:        sqlDB,
		Approvals: fa,
		Now:       fixedNow(time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)),
		NewID:     seqIDs("id"),
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := p.Run(context.Background(), PlanPayload{Repo: "demo", Phase: "1"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fc.calls != 1 || fc.lastTask != llm.TaskBuilder {
		t.Fatalf("completer calls=%d task=%q", fc.calls, fc.lastTask)
	}
	if len(fa.calls) != 1 || fa.calls[0].Kind != approvalKindPlan {
		t.Fatalf("approval calls=%+v", fa.calls)
	}
	if res.ContentID == "" || res.ApprovalID == "" || res.BuildID == "" {
		t.Fatalf("missing ids: %+v", res)
	}
	if len(res.Files) != 2 {
		t.Fatalf("files=%v", res.Files)
	}

	for _, rel := range res.Files {
		path := filepath.Join(repoDir, filepath.FromSlash(rel))
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read plan file %s: %v", path, err)
		}
		if !strings.Contains(string(raw), "## Acceptance criteria") {
			t.Fatalf("plan file missing AC section: %s\n%s", path, raw)
		}
	}

	var kind, format, stage string
	if err := sqlDB.QueryRow(`SELECT kind, format, stage FROM content_items WHERE id = ?`, res.ContentID).
		Scan(&kind, &format, &stage); err != nil {
		t.Fatalf("content_items: %v", err)
	}
	if kind != contentKindPlan || format != contentFormatPlan || stage != "planned" {
		t.Fatalf("content_items kind/format/stage = %s/%s/%s", kind, format, stage)
	}

	var status, planPath, thread string
	if err := sqlDB.QueryRow(`SELECT status, plan_path, thread FROM builds WHERE id = ?`, res.BuildID).
		Scan(&status, &planPath, &thread); err != nil {
		t.Fatalf("builds: %v", err)
	}
	if status != buildStatusPlanned || planPath != "plans/phase-1" || thread != "implementer" {
		t.Fatalf("builds status/path/thread = %s/%s/%s", status, planPath, thread)
	}
}

func TestPlannerRedoWithNoteReplans(t *testing.T) {
	repoDir := newFakeTargetRepo(t)
	sqlDB := openBuilderDB(t)
	fc := &fakeCompleter{body: samplePlanJSON()}
	fa := &fakeApproval{nextID: seqIDs("appr")}
	ids := seqIDs("id")
	p, err := NewPlanner(PlannerOptions{
		Enabled:   true,
		Repos:     []config.RepoConfig{{Name: "demo", Path: repoDir}},
		Completer: fc,
		DB:        sqlDB,
		Approvals: fa,
		NewID:     ids,
		Now:       fixedNow(time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := p.Run(context.Background(), PlanPayload{Repo: "demo", Phase: "1"})
	if err != nil {
		t.Fatal(err)
	}

	fc.body = `{
  "phase": "1",
  "subphases": [
    {"index": 1, "slug": "revised-scaffold", "goal": "Revised after note", "acceptance_criteria": ["tests pass"]},
    {"index": 2, "slug": "revised-api", "goal": "API after note", "acceptance_criteria": ["handler covered"]}
  ]
}`
	second, err := p.Run(context.Background(), PlanPayload{
		ContentID: first.ContentID,
		RedoNotes: "split scaffold from API",
	})
	if err != nil {
		t.Fatalf("redo: %v", err)
	}
	if second.ContentID != first.ContentID {
		t.Fatalf("redo should reuse content id: %s vs %s", second.ContentID, first.ContentID)
	}
	if fc.calls != 2 {
		t.Fatalf("want 2 LLM calls, got %d", fc.calls)
	}
	if !strings.Contains(fc.lastReq.Messages[0].Content, "split scaffold from API") {
		t.Fatalf("redo notes missing from prompt: %q", fc.lastReq.Messages[0].Content)
	}
	raw, err := os.ReadFile(filepath.Join(repoDir, "plans", "phase-1", "1.1-revised-scaffold.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Revised after note") {
		t.Fatalf("revised plan not written: %s", raw)
	}
	if len(fa.calls) != 2 {
		t.Fatalf("want 2 approval starts, got %d", len(fa.calls))
	}
}

func TestPlannerHandlerDisabled(t *testing.T) {
	p, err := NewPlanner(PlannerOptions{
		Enabled:   false,
		Completer: &fakeCompleter{body: `{}`},
		DB:        openBuilderDB(t),
		Approvals: &fakeApproval{},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.Handler()(context.Background(), queue.Job{
		Type:    JobPlan,
		Payload: json.RawMessage(`{"repo":"x"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var res PlanResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatal(err)
	}
	if !res.Skipped {
		t.Fatalf("want skipped, got %+v", res)
	}
}

func newFakeTargetRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "ARCHITECTURE.md"), "# Architecture\n\nBuilder plans worktrees.\n")
	mustWrite(t, filepath.Join(dir, "SPEC.md"), "# Spec\n\nPhase 1: scaffold.\n")
	mustWrite(t, filepath.Join(dir, "designs", "home.md"), "# Home screen\n")
	mustWrite(t, filepath.Join(dir, "tickets", "T-1.md"), "---\nid: T-1\ntitle: Scaffold\nstatus: ready\npriority: 1\n---\n## Goal\nScaffold.\n")
	return dir
}

func samplePlanJSON() string {
	return `{
  "phase": "1",
  "subphases": [
    {"index": 1, "slug": "scaffold", "goal": "Create package skeleton", "acceptance_criteria": ["go test ./internal/builder passes"]},
    {"index": 2, "slug": "api-wire", "goal": "Wire job handler", "acceptance_criteria": ["handler registered", "disabled gate tested"]}
  ]
}`
}

func openBuilderDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	sqlDB, err := db.Open(ctx, filepath.Join(t.TempDir(), "builder.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := db.Migrate(ctx, sqlDB); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	if _, err := sqlDB.Exec(`
INSERT INTO channels (id, platform, handle, language, niche, account_ref, status)
VALUES ('builder', 'builder', '', 'en', 'builder', '', 'active')`); err != nil {
		t.Fatalf("seed builder channel: %v", err)
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

func mustWrite(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

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
	return llm.Response{Text: f.body, Provider: "claude", Model: "claude-opus-5-5"}, nil
}

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

func TestIsPermanentHelperUsed(t *testing.T) {
	// Sanity: queue.IsPermanent must exist for the rejection test above.
	err := queue.Permanent(errors.New("x"))
	if !queue.IsPermanent(err) {
		t.Fatal("expected permanent")
	}
}
