package agency

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mayank2/internal/db"
	"mayank2/internal/llm"
)

type fakeLLM struct {
	task llm.Task
}

func (f *fakeLLM) Complete(_ context.Context, task llm.Task, _ llm.Request) (llm.Response, error) {
	f.task = task
	if task == llm.TaskBlog || task == llm.TaskBuilder {
		return llm.Response{}, errorsNew("must not use Claude tasks")
	}
	return llm.Response{Text: "Hello {{company}}, here is a proposal."}, nil
}

func errorsNew(s string) error { return &simpleErr{s} }

type simpleErr struct{ s string }

func (e *simpleErr) Error() string { return e.s }

type fakeApproval struct{ kind string }

func (f *fakeApproval) Start(_ context.Context, _, kind, _, _ string) (string, error) {
	f.kind = kind
	return "ap-agency", nil
}

func TestLeadCRUDAndDraftUsesScriptNotClaude(t *testing.T) {
	ctx := context.Background()
	sqlH, err := db.Open(ctx, filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlH.Close() })
	if _, err := db.Migrate(ctx, sqlH); err != nil {
		t.Fatal(err)
	}

	fl := &fakeLLM{}
	fa := &fakeApproval{}
	svc := &Service{
		DB: sqlH, LLM: fl, Approvals: fa,
		Now:   func() time.Time { return time.Date(2026, 9, 29, 1, 0, 0, 0, time.UTC) },
		NewID: seq("L"),
	}

	lead, err := svc.CreateLead(ctx, "Acme Dental", "sam@acme.test", "scheduling automation", "manual")
	if err != nil {
		t.Fatal(err)
	}
	list, err := svc.ListLeads(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%v err=%v", list, err)
	}

	pid, err := svc.DraftProposal(ctx, lead.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if fl.task != llm.TaskScript {
		t.Fatalf("task=%q want script", fl.task)
	}
	if fa.kind != "agency_proposal" {
		t.Fatalf("kind=%q", fa.kind)
	}
	if err := svc.MarkApproved(ctx, pid); err != nil {
		t.Fatal(err)
	}
	var st string
	_ = sqlH.QueryRow(`SELECT status FROM agency_proposals WHERE id=?`, pid).Scan(&st)
	if st != PropApproved {
		t.Fatalf("status=%s", st)
	}

	nudge, err := svc.SetLeadStatus(ctx, lead.ID, StatusWon)
	if err != nil || !strings.Contains(nudge, "Revenue") {
		t.Fatalf("nudge=%q err=%v", nudge, err)
	}
}

func TestNoSendClientsImported(t *testing.T) {
	// Static guard: agency package source must not import net/smtp, mail APIs, etc.
	src, err := os.ReadFile("agency.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "agency.go", src, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{"net/smtp", "net/mail", "github.com/sendgrid", "google.golang.org/api/gmail"}
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		for _, bad := range forbidden {
			if path == bad || strings.HasPrefix(path, bad+"/") {
				t.Fatalf("forbidden import %s — no automated sending", path)
			}
		}
	}
}

func seq(prefix string) func() string {
	n := 0
	return func() string {
		n++
		return fmt.Sprintf("%s%d", prefix, n)
	}
}
