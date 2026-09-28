package content

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mayank2/internal/db"
	"mayank2/internal/llm"
)

type fakeAffLLM struct {
	task llm.Task
}

func (f *fakeAffLLM) Complete(_ context.Context, task llm.Task, _ llm.Request) (llm.Response, error) {
	f.task = task
	if task == llm.TaskBlog || task == llm.TaskBuilder {
		return llm.Response{}, errStr("claude forbidden")
	}
	return llm.Response{Text: `{"title":"Best budget mic","description":"Great for shorts."}`}, nil
}

type errStr string

func (e errStr) Error() string { return string(e) }

type fakeAffApproval struct{ n int }

func (f *fakeAffApproval) Start(_ context.Context, _, _, _, _ string) (string, error) {
	f.n++
	return "ap-pin", nil
}

func TestAffiliatePrepareDisclosureNoCloakNoDupNoClaude(t *testing.T) {
	ctx := context.Background()
	sqlH, err := db.Open(ctx, filepath.Join(t.TempDir(), "aff.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlH.Close() })
	if _, err := db.Migrate(ctx, sqlH); err != nil {
		t.Fatal(err)
	}

	fl := &fakeAffLLM{}
	fa := &fakeAffApproval{}
	svc := &AffiliatePins{
		DB: sqlH, LLM: fl, Approvals: fa,
		Cfg: AffiliateConfig{
			Programs: []struct {
				ID         string `yaml:"id"`
				Name       string `yaml:"name"`
				Disclosure string `yaml:"disclosure"`
			}{{ID: "amazon", Name: "Amazon", Disclosure: DefaultDisclosure}},
		},
		Now:   func() time.Time { return time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC) },
		NewID: seqPin("P"),
	}

	link := "https://www.amazon.com/dp/B00TEST?tag=mayank-20"
	res, err := svc.Prepare(ctx, AffiliatePinRequest{
		ProductTitle: "Budget mic", Destination: link, BoardID: "board1", ProgramID: "amazon",
	})
	if err != nil {
		t.Fatal(err)
	}
	if fl.task != llm.TaskMetadata {
		t.Fatalf("task=%q", fl.task)
	}
	if res.Link != link {
		t.Fatalf("link mutated: %q", res.Link)
	}
	if !strings.Contains(res.Description, "#ad") {
		t.Fatalf("missing disclosure: %q", res.Description)
	}
	if fa.n != 1 {
		t.Fatal("expected approval")
	}

	// Same-day duplicate skipped.
	_, err = svc.Prepare(ctx, AffiliatePinRequest{
		ProductTitle: "Budget mic", Destination: link, BoardID: "board1", ProgramID: "amazon",
	})
	if err == nil || !strings.Contains(err.Error(), "already used today") {
		t.Fatalf("want dup skip, got %v", err)
	}

	// Cloaked URL rejected.
	_, err = svc.Prepare(ctx, AffiliatePinRequest{
		ProductTitle: "x", Destination: "https://bit.ly/abc", BoardID: "b", ProgramID: "amazon",
	})
	if err == nil || !strings.Contains(err.Error(), "cloaked") {
		t.Fatalf("want cloak reject, got %v", err)
	}
}

func seqPin(prefix string) func() string {
	n := 0
	return func() string {
		n++
		return prefix + string(rune('A'+n-1))
	}
}
