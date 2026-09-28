package blog

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mayank2/internal/llm"
)

const sourceBody = `This is the canonical Mayankbuilt post about shipping a Go daemon on a
16 GB laptop. It walks through the queue design, the SQLite WAL setup, and
why a single binary beats a pile of services when nobody is around to
babysit it. There are code samples, a section on backpressure, and a long
closing riff on why boring technology wins in the end. The article runs
several paragraphs longer than any single LinkedIn post should ever be, on
purpose, because it is written for readers who want the whole story, not a
teaser.`

const sourceURL = "https://mayank.dev/blog/go-daemon-16gb-laptop"

// fakeCompleter returns a canned response, or an error, per call.
type fakeLICompleter struct {
	responses []string
	errs      []error
	calls     []llm.Request
}

func (f *fakeLICompleter) Complete(ctx context.Context, task llm.Task, req llm.Request) (llm.Response, error) {
	f.calls = append(f.calls, req)
	i := len(f.calls) - 1
	if i < len(f.errs) && f.errs[i] != nil {
		return llm.Response{}, f.errs[i]
	}
	if i >= len(f.responses) {
		i = len(f.responses) - 1
	}
	if task != llm.TaskBlog {
		return llm.Response{}, errFakeWrongTask
	}
	return llm.Response{Text: f.responses[i], Provider: "claude", Model: "claude-test"}, nil
}

var errFakeWrongTask = errors.New("fake: expected llm.TaskBlog")

// fakeApprover records Start() calls instead of hitting a real DB/queue.
type fakeApprover struct {
	gotContentID string
	gotKind      string
	gotSummary   string
	returnID     string
	err          error
	calls        int
}

func (f *fakeApprover) Start(ctx context.Context, contentID, kind, summary, previewPath string) (string, error) {
	f.calls++
	f.gotContentID = contentID
	f.gotKind = kind
	f.gotSummary = summary
	if f.err != nil {
		return "", f.err
	}
	if f.returnID == "" {
		return "ap-1", nil
	}
	return f.returnID, nil
}

func newSource() SourcePost {
	return SourcePost{ContentID: "c1", URL: sourceURL, Body: sourceBody}
}

func TestRepurposer_Run_HappyPath(t *testing.T) {
	draft := "Most 'production-ready' setups I see are five services held together by hope.\n\n" +
		"Mine is one Go binary and SQLite. Here is why that held up fine on a laptop with 16 GB of RAM.\n\n" +
		"Full writeup: " + sourceURL
	comp := &fakeLICompleter{responses: []string{draft}}
	appr := &fakeApprover{returnID: "ap-42"}
	r, err := NewRepurposer(Options{Completer: comp, Approver: appr})
	if err != nil {
		t.Fatalf("NewRepurposer: %v", err)
	}

	got, err := r.Run(context.Background(), RunInput{Source: newSource()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.ApprovalID != "ap-42" {
		t.Fatalf("ApprovalID = %q, want ap-42", got.ApprovalID)
	}
	if !strings.Contains(got.Text, sourceURL) {
		t.Fatalf("draft missing source link: %q", got.Text)
	}
	if countWords(got.Text) >= countWords(sourceBody) {
		t.Fatalf("draft (%d words) not shorter than source (%d words)", countWords(got.Text), countWords(sourceBody))
	}
	if normalizeLine(firstLine(got.Text)) == normalizeLine(firstLine(sourceBody)) {
		t.Fatalf("draft opening line copies the source")
	}
	if appr.calls != 1 {
		t.Fatalf("approver called %d times, want 1", appr.calls)
	}
	if appr.gotContentID != "c1" {
		t.Fatalf("approval content_id = %q, want c1", appr.gotContentID)
	}
	if appr.gotKind != approvalKindLinkedIn {
		t.Fatalf("approval kind = %q, want %q", appr.gotKind, approvalKindLinkedIn)
	}
	if !strings.Contains(appr.gotSummary, got.Text) || !strings.Contains(appr.gotSummary, sourceURL) {
		t.Fatalf("approval summary missing draft text or source link: %q", appr.gotSummary)
	}
	if len(comp.calls) != 1 {
		t.Fatalf("llm called %d times, want 1", len(comp.calls))
	}
}

// TestRepurposer_Run_NotCopyPaste is the AC2 test: a raw truncation of the
// source (same opening line, same length ballpark, no real rewrite) must be
// rejected, not passed straight to approval.
func TestRepurposer_Run_NotCopyPaste(t *testing.T) {
	// First reply: a copy-paste truncation (same opening line as source,
	// shares a long run verbatim). Second reply: a genuine rewrite.
	copyPaste := strings.Join(strings.Fields(sourceBody)[:20], " ")
	rewrite := "Nobody warns you that a single Go binary beats five microservices when you're the only one on call.\n\n" +
		"Wrote up the whole approach here: " + sourceURL
	comp := &fakeLICompleter{responses: []string{copyPaste, rewrite}}
	appr := &fakeApprover{}
	r, err := NewRepurposer(Options{Completer: comp, Approver: appr})
	if err != nil {
		t.Fatalf("NewRepurposer: %v", err)
	}

	got, err := r.Run(context.Background(), RunInput{Source: newSource()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(comp.calls) != 2 {
		t.Fatalf("llm called %d times, want 2 (one retry)", len(comp.calls))
	}
	if got.Text != rewrite {
		t.Fatalf("final draft = %q, want %q", got.Text, rewrite)
	}
	// The retry message must reference why the first attempt failed.
	if len(comp.calls[1].Messages) < 2 {
		t.Fatalf("retry prompt missing correction message: %+v", comp.calls[1].Messages)
	}
}

// TestRepurposer_Run_AlwaysCopyPaste_PermanentError covers the case where
// every attempt fails originality — must be a queue.Permanent error (dead
// letter, not endless retry) wrapping ErrDraftNotOriginal.
func TestRepurposer_Run_AlwaysCopyPaste_PermanentError(t *testing.T) {
	copyPaste := strings.Join(strings.Fields(sourceBody)[:20], " ")
	comp := &fakeLICompleter{responses: []string{copyPaste, copyPaste}}
	appr := &fakeApprover{}
	r, err := NewRepurposer(Options{Completer: comp, Approver: appr})
	if err != nil {
		t.Fatalf("NewRepurposer: %v", err)
	}

	_, err = r.Run(context.Background(), RunInput{Source: newSource()})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, ErrDraftNotOriginal) {
		t.Fatalf("err = %v, want wrapping ErrDraftNotOriginal", err)
	}
	if appr.calls != 0 {
		t.Fatalf("approver must not be called when draft never passes, got %d calls", appr.calls)
	}
}

func TestRepurposer_Run_LLMError(t *testing.T) {
	comp := &fakeLICompleter{errs: []error{errors.New("provider down")}}
	appr := &fakeApprover{}
	r, err := NewRepurposer(Options{Completer: comp, Approver: appr})
	if err != nil {
		t.Fatalf("NewRepurposer: %v", err)
	}
	_, err = r.Run(context.Background(), RunInput{Source: newSource()})
	if err == nil || !strings.Contains(err.Error(), "provider down") {
		t.Fatalf("err = %v, want wrapping 'provider down'", err)
	}
	if appr.calls != 0 {
		t.Fatal("approver must not be called on llm error")
	}
}

func TestRepurposer_Run_RedoNotesInPrompt(t *testing.T) {
	rewrite := "A sharper hook this time, per the note.\n\nMore: " + sourceURL
	comp := &fakeLICompleter{responses: []string{rewrite}}
	appr := &fakeApprover{}
	r, err := NewRepurposer(Options{Completer: comp, Approver: appr})
	if err != nil {
		t.Fatalf("NewRepurposer: %v", err)
	}
	_, err = r.Run(context.Background(), RunInput{Source: newSource(), RedoNotes: "make the hook punchier"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(comp.calls) != 1 {
		t.Fatalf("llm called %d times, want 1", len(comp.calls))
	}
	found := false
	for _, m := range comp.calls[0].Messages {
		if strings.Contains(m.Content, "make the hook punchier") {
			found = true
		}
	}
	if !found {
		t.Fatalf("redo notes not in prompt: %+v", comp.calls[0].Messages)
	}
}

func TestRepurposer_Run_MissingSourceFields(t *testing.T) {
	comp := &fakeLICompleter{responses: []string{"x " + sourceURL}}
	appr := &fakeApprover{}
	r, err := NewRepurposer(Options{Completer: comp, Approver: appr})
	if err != nil {
		t.Fatalf("NewRepurposer: %v", err)
	}
	cases := []RunInput{
		{Source: SourcePost{URL: sourceURL, Body: sourceBody}},  // missing content_id
		{Source: SourcePost{ContentID: "c1", Body: sourceBody}}, // missing url
		{Source: SourcePost{ContentID: "c1", URL: sourceURL}},   // missing body
	}
	for i, in := range cases {
		if _, err := r.Run(context.Background(), in); err == nil {
			t.Fatalf("case %d: want error, got nil", i)
		}
	}
}

func TestNewRepurposer_RequiresDeps(t *testing.T) {
	if _, err := NewRepurposer(Options{Approver: &fakeApprover{}}); err == nil {
		t.Fatal("want error without Completer")
	}
	if _, err := NewRepurposer(Options{Completer: &fakeLICompleter{}}); err == nil {
		t.Fatal("want error without Approver")
	}
}

func TestValidateDraft(t *testing.T) {
	tests := []struct {
		name    string
		draft   string
		wantErr bool
	}{
		{"empty", "", true},
		{"missing link", "A short original take on the topic that is definitely shorter.", true},
		{"too long", sourceBody + " " + sourceURL, true},
		{"same opening line", firstLine(sourceBody) + ". " + sourceURL, true},
		{"good", "A punchy new take nobody asked for. " + sourceURL, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDraft(tt.draft, sourceBody, sourceURL)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateDraft(%q) err=%v, wantErr=%v", tt.draft, err, tt.wantErr)
			}
		})
	}
}

func TestNormalizeDraft_AddsLinkAndStripsFence(t *testing.T) {
	got := normalizeDraft("```\nSome draft text\n```", sourceURL)
	if strings.Contains(got, "```") {
		t.Fatalf("fence not stripped: %q", got)
	}
	if !strings.Contains(got, sourceURL) {
		t.Fatalf("link not added: %q", got)
	}

	got2 := normalizeDraft("Already has the link: "+sourceURL, sourceURL)
	if strings.Count(got2, sourceURL) != 1 {
		t.Fatalf("link duplicated: %q", got2)
	}
}

func TestStripMDXFrontmatter(t *testing.T) {
	raw := "---\ntitle: Hello\nslug: hello\n---\n\nActual body text here.\n"
	got := stripMDXFrontmatter(raw)
	if strings.Contains(got, "title:") {
		t.Fatalf("frontmatter not stripped: %q", got)
	}
	if !strings.Contains(got, "Actual body text here.") {
		t.Fatalf("body lost: %q", got)
	}

	noFrontmatter := "Just a plain body, no frontmatter."
	if got := stripMDXFrontmatter(noFrontmatter); got != noFrontmatter {
		t.Fatalf("no-frontmatter case altered: %q", got)
	}
}

func TestLongestSharedRun(t *testing.T) {
	a := wordsOf("the quick brown fox jumps over the lazy dog")
	b := wordsOf("a quick brown fox jumps over a lazy cat")
	if got := longestSharedRun(a, b); got != 5 { // "quick brown fox jumps over"
		t.Fatalf("longestSharedRun = %d, want 5", got)
	}
}

// --- LoadSourcePost (DB) -----------------------------------------------

func mustExecBlog(t *testing.T, sqlDB *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := sqlDB.Exec(q, args...); err != nil {
		t.Fatalf("exec: %v\n%s", err, q)
	}
}

func TestLoadSourcePost(t *testing.T) {
	sqlDB := openBlogDB(t)
	mustExecBlog(t, sqlDB, `
INSERT INTO channels (id, platform, handle, language, niche, account_ref, status)
VALUES ('blog-ch', 'mayankbuilt', '', 'en', 'blog', '', 'active')`)
	mustExecBlog(t, sqlDB, `
INSERT INTO content_items (id, channel_id, kind, language, stage, created_at)
VALUES ('c1', 'blog-ch', 'blog', 'en', 'live', ?)`, time.Now().UTC().Format(time.RFC3339Nano))
	mustExecBlog(t, sqlDB, `
INSERT INTO publications (id, content_id, platform, account, status, url, idempotency_key, published_at)
VALUES ('pub1', 'c1', 'mayankbuilt', 'mayankbuilt', 'published', ?, 'c1:mayankbuilt', ?)`,
		sourceURL, time.Now().UTC().Format(time.RFC3339Nano))

	mdxPath := filepath.Join(t.TempDir(), "post.mdx")
	mdxContent := "---\ntitle: Go Daemon\n---\n\n" + sourceBody
	if err := os.WriteFile(mdxPath, []byte(mdxContent), 0o644); err != nil {
		t.Fatalf("write mdx: %v", err)
	}
	mustExecBlog(t, sqlDB, `
INSERT INTO assets (id, content_id, kind, path) VALUES ('a1', 'c1', 'mdx', ?)`, mdxPath)

	got, err := LoadSourcePost(context.Background(), sqlDB, "c1")
	if err != nil {
		t.Fatalf("LoadSourcePost: %v", err)
	}
	if got.URL != sourceURL {
		t.Fatalf("URL = %q, want %q", got.URL, sourceURL)
	}
	if !strings.Contains(got.Body, "canonical Mayankbuilt post") || strings.Contains(got.Body, "title:") {
		t.Fatalf("Body not loaded/stripped correctly: %q", got.Body)
	}
}

func TestLoadSourcePost_NoPublication(t *testing.T) {
	sqlDB := openBlogDB(t)
	_, err := LoadSourcePost(context.Background(), sqlDB, "missing")
	if err == nil || !strings.Contains(err.Error(), "no live mayankbuilt publication") {
		t.Fatalf("err = %v, want 'no live mayankbuilt publication'", err)
	}
}
