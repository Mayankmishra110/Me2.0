package tickets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTicket(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantID  string
		wantErr bool
	}{
		{"lf", "---\nid: A-1\ntitle: \"x: y\"\nstatus: Ready\n---\nbody", "A-1", false},
		{"crlf", "---\r\nid: A-2\r\nstatus: ready\r\n---\r\nbody", "A-2", false},
		{"no frontmatter", "# just markdown", "", true},
		{"no id", "---\nstatus: ready\n---\n", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := writeTicket(t, t.TempDir(), "t.md", tc.content)
			got, err := Parse(p)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil && got.ID != tc.wantID {
				t.Fatalf("ID = %q, want %q", got.ID, tc.wantID)
			}
			if err == nil && got.Status != "ready" {
				t.Fatalf("Status = %q, want normalized %q", got.Status, "ready")
			}
		})
	}
}

func TestScanOrdersByPriorityAndSkipsDocs(t *testing.T) {
	dir := t.TempDir()
	writeTicket(t, dir, "b.md", "---\nid: B\nstatus: ready\npriority: 2\n---\n")
	writeTicket(t, dir, "a.md", "---\nid: A\nstatus: ready\n---\n")
	writeTicket(t, dir, "c.md", "---\nid: C\nstatus: ready\npriority: 1\n---\n")
	writeTicket(t, dir, "TEMPLATE.md", "---\nid: M2-XXX\n---\n")
	writeTicket(t, dir, "bad.md", "no frontmatter")

	got, errs := Scan(dir)
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want 1 parse error", errs)
	}
	var ids []string
	for _, tk := range got {
		ids = append(ids, tk.ID)
	}
	if strings.Join(ids, ",") != "C,B,A" {
		t.Fatalf("order = %v, want C,B,A (unset priority last)", ids)
	}
}

func TestSetStatusOnlyTouchesFrontmatter(t *testing.T) {
	content := "---\r\nid: A\r\nstatus: ready   # comment\r\n---\r\nstatus: keep me\r\n"
	p := writeTicket(t, t.TempDir(), "t.md", content)
	if err := SetStatus(p, "in-review"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	want := "---\r\nid: A\r\nstatus: in-review\r\n---\r\nstatus: keep me\r\n"
	if string(raw) != want {
		t.Fatalf("got %q\nwant %q", raw, want)
	}
}

// The repo's own tickets must always parse, or parallel threads can't pick work.
func TestRepoTicketsParse(t *testing.T) {
	got, errs := Scan(filepath.Join("..", "..", "tickets"))
	for _, err := range errs {
		t.Error(err)
	}
	if len(got) == 0 {
		t.Fatal("no tickets found in repo tickets/")
	}
}
