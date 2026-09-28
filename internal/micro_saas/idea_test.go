package micro_saas

import (
	"context"
	"database/sql"
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

type fakeLLM struct {
	task llm.Task
	text string
	n    int
}

func (f *fakeLLM) Complete(_ context.Context, task llm.Task, req llm.Request) (llm.Response, error) {
	f.task = task
	f.n++
	if f.text != "" {
		return llm.Response{Text: f.text}, nil
	}
	body := "# ARCHITECTURE.md\narch\n# SPEC.md\nspec"
	for _, m := range req.Messages {
		if strings.Contains(m.Content, "Redo notes") {
			body = "# ARCHITECTURE.md\narch-redo\n# SPEC.md\nspec-redo"
		}
	}
	return llm.Response{Text: body}, nil
}

type fakeApproval struct {
	calls int
	kind  string
}

func (f *fakeApproval) Start(_ context.Context, _, kind, _, _ string) (string, error) {
	f.calls++
	f.kind = kind
	return "ap1", nil
}

type fakeQ struct{ types []string }

func (f *fakeQ) Enqueue(_ context.Context, jobType string, _ any, _ ...queue.EnqueueOpt) (string, error) {
	f.types = append(f.types, jobType)
	return "j1", nil
}

func openMigrated(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	sqlH, err := db.Open(ctx, filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlH.Close() })
	if _, err := db.Migrate(ctx, sqlH); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlH.Exec(`INSERT INTO channels (id, platform, handle, language, niche, account_ref, status)
VALUES ('builder','builder','','en','builder','','active')`); err != nil {
		t.Fatal(err)
	}
	return sqlH
}

func TestSubmitDraftHandOffAndRefuseBadRepo(t *testing.T) {
	ctx := context.Background()
	sqlH := openMigrated(t)
	repoDir := t.TempDir()
	fq := &fakeQ{}
	fa := &fakeApproval{}
	fl := &fakeLLM{text: "# ARCHITECTURE.md\narch body\n# SPEC.md\nspec body\n"}
	svc := &Service{
		DB: sqlH, LLM: fl, Approvals: fa, Queue: fq,
		Enabled: true,
		Repos:   []config.RepoConfig{{Name: "clipper", Path: repoDir}},
		Now:     func() time.Time { return time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC) },
		NewID:   func() string { return "idea1" },
	}

	_, err := svc.Submit(ctx, IdeaInput{Title: "Clipper", Description: "d", TargetRepo: "not-listed"})
	if err == nil {
		t.Fatal("want refuse unlisted repo")
	}

	id, err := svc.Submit(ctx, IdeaInput{
		Title: "Clipper", Description: "chrome ext", TargetUsers: "creators", TargetRepo: "clipper",
	})
	if err != nil || id != "idea1" {
		t.Fatalf("submit id=%s err=%v", id, err)
	}
	if len(fq.types) != 1 || fq.types[0] != JobIdeaDraft {
		t.Fatalf("enqueue=%v", fq.types)
	}
	var stage string
	if err := sqlH.QueryRow(`SELECT stage FROM content_items WHERE id=?`, id).Scan(&stage); err != nil || stage != StageSubmitted {
		t.Fatalf("stage=%q err=%v", stage, err)
	}

	if err := svc.Draft(ctx, id); err != nil {
		t.Fatal(err)
	}
	if fl.task != llm.TaskBuilder {
		t.Fatalf("task=%q want builder", fl.task)
	}
	if fa.calls != 1 || fa.kind != approvalKindSpec {
		t.Fatalf("approval calls=%d kind=%s", fa.calls, fa.kind)
	}
	if err := sqlH.QueryRow(`SELECT stage FROM content_items WHERE id=?`, id).Scan(&stage); err != nil || stage != StageSpecDrafted {
		t.Fatalf("stage=%q err=%v", stage, err)
	}

	if err := svc.HandOff(ctx, id); err == nil {
		t.Fatal("want not approved")
	}

	if _, err := sqlH.Exec(`INSERT INTO approvals (id, content_id, kind, summary, status, nonce)
VALUES ('ap1', ?, 'micro_saas_spec', 's', 'approved', 'n')`, id); err != nil {
		t.Fatal(err)
	}
	fq.types = nil
	if err := svc.HandOff(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := sqlH.QueryRow(`SELECT stage FROM content_items WHERE id=?`, id).Scan(&stage); err != nil || stage != StageHandedToBuilder {
		t.Fatalf("stage=%q err=%v", stage, err)
	}
	if _, err := os.Stat(filepath.Join(repoDir, "ARCHITECTURE.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repoDir, "SPEC.md")); err != nil {
		t.Fatal(err)
	}
	if len(fq.types) != 1 || fq.types[0] != "builder.plan" {
		t.Fatalf("want builder.plan enqueue, got %v", fq.types)
	}
	nudge := RevenueNudge("Clipper")
	if !strings.Contains(nudge, "Revenue") {
		t.Fatalf("nudge=%q", nudge)
	}
}

func TestRedoWithNoteRedrafts(t *testing.T) {
	ctx := context.Background()
	sqlH := openMigrated(t)
	repoDir := t.TempDir()
	fq := &fakeQ{}
	fa := &fakeApproval{}
	fl := &fakeLLM{}
	svc := &Service{
		DB: sqlH, LLM: fl, Approvals: fa, Queue: fq,
		Enabled: true,
		Repos:   []config.RepoConfig{{Name: "clipper", Path: repoDir}},
		Now:     func() time.Time { return time.Date(2026, 9, 29, 1, 0, 0, 0, time.UTC) },
		NewID:   func() string { return "idea2" },
	}
	id, err := svc.Submit(ctx, IdeaInput{Title: "Clipper", TargetRepo: "clipper"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Draft(ctx, id); err != nil {
		t.Fatal(err)
	}
	nDrafts := fl.n
	fq.types = nil
	id2, err := svc.Submit(ctx, IdeaInput{ContentID: id, RedoNotes: "focus on chrome MV3"})
	if err != nil || id2 != id {
		t.Fatalf("redo id=%s err=%v", id2, err)
	}
	if len(fq.types) != 1 || fq.types[0] != JobIdeaDraft {
		t.Fatalf("redo enqueue=%v", fq.types)
	}
	var stage string
	_ = sqlH.QueryRow(`SELECT stage FROM content_items WHERE id=?`, id).Scan(&stage)
	if stage != StageSubmitted {
		t.Fatalf("redo stage=%q", stage)
	}
	if err := svc.Draft(ctx, id); err != nil {
		t.Fatal(err)
	}
	if fl.n != nDrafts+1 {
		t.Fatalf("want re-draft, n=%d", fl.n)
	}
	var script string
	_ = sqlH.QueryRow(`SELECT script FROM content_items WHERE id=?`, id).Scan(&script)
	if !strings.Contains(script, "arch-redo") {
		t.Fatalf("spec_draft missing redo content: %s", script)
	}
}

func TestDisabledNoOp(t *testing.T) {
	ctx := context.Background()
	sqlH := openMigrated(t)
	svc := &Service{DB: sqlH, Enabled: false, Repos: []config.RepoConfig{{Name: "x", Path: "/tmp/x"}}}
	id, err := svc.Submit(ctx, IdeaInput{Title: "t", TargetRepo: "x"})
	if err != nil || id != "" {
		t.Fatalf("disabled submit id=%q err=%v", id, err)
	}
}

func TestHandOffMissingRepoPath(t *testing.T) {
	ctx := context.Background()
	sqlH := openMigrated(t)
	svc := &Service{
		DB: sqlH, Enabled: true,
		Repos: []config.RepoConfig{{Name: "ghost", Path: filepath.Join(t.TempDir(), "does-not-exist")}},
		NewID: func() string { return "idea3" },
	}
	id, err := svc.Submit(ctx, IdeaInput{Title: "Ghost", TargetRepo: "ghost"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlH.Exec(`UPDATE content_items SET stage=?, script=? WHERE id=?`,
		StageSpecDrafted,
		`{"title":"Ghost","target_repo":"ghost","spec_draft":"# ARCHITECTURE.md\na\n# SPEC.md\nb"}`,
		id); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlH.Exec(`INSERT INTO approvals (id, content_id, kind, summary, status, nonce)
VALUES ('ap3', ?, 'micro_saas_spec', 's', 'approved', 'n')`, id); err != nil {
		t.Fatal(err)
	}
	if err := svc.HandOff(ctx, id); err == nil {
		t.Fatal("want missing path refusal")
	}
}

func TestHandOffLatestApprovalOnly(t *testing.T) {
	ctx := context.Background()
	sqlH := openMigrated(t)
	repoDir := t.TempDir()
	svc := &Service{
		DB: sqlH, Enabled: true,
		Repos: []config.RepoConfig{{Name: "clipper", Path: repoDir}},
		NewID: func() string { return "idea4" },
	}
	id, err := svc.Submit(ctx, IdeaInput{Title: "Clipper", TargetRepo: "clipper"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlH.Exec(`UPDATE content_items SET stage=?, script=? WHERE id=?`,
		StageSpecDrafted,
		`{"title":"Clipper","target_repo":"clipper","spec_draft":"# ARCHITECTURE.md\na\n# SPEC.md\nb"}`,
		id); err != nil {
		t.Fatal(err)
	}
	// Stale approve must not unlock hand-off when a newer pending redo exists.
	if _, err := sqlH.Exec(`INSERT INTO approvals (id, content_id, kind, summary, status, nonce, decided_at)
VALUES ('ap-old', ?, 'micro_saas_spec', 's', 'approved', 'n1', '2026-09-28T00:00:00Z')`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlH.Exec(`INSERT INTO approvals (id, content_id, kind, summary, status, nonce)
VALUES ('ap-new', ?, 'micro_saas_spec', 's', 'pending', 'n2')`, id); err != nil {
		t.Fatal(err)
	}
	if err := svc.HandOff(ctx, id); err == nil {
		t.Fatal("want refuse when latest approval is pending")
	}
	// Wrong kind must be ignored even if approved.
	if _, err := sqlH.Exec(`DELETE FROM approvals WHERE content_id=?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlH.Exec(`INSERT INTO approvals (id, content_id, kind, summary, status, nonce, decided_at)
VALUES ('ap-plan', ?, 'plan', 's', 'approved', 'n3', '2026-09-29T12:00:00Z')`, id); err != nil {
		t.Fatal(err)
	}
	if err := svc.HandOff(ctx, id); err == nil {
		t.Fatal("want refuse when only wrong-kind approval exists")
	}
}
