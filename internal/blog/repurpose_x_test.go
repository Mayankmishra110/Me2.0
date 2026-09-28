package blog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mayank2/internal/db"
	"mayank2/internal/llm"
)

func openBlogDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	sqlDB, err := db.Open(ctx, filepath.Join(t.TempDir(), "b.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := db.Migrate(ctx, sqlDB); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	// content_items.channel_id is NOT NULL REFERENCES channels(id); seed the
	// "blog" pseudo-channel this repurposer uses by default. A real seed for
	// this row is assumed to land via M2-401 or config/channels; tests own
	// it here since that seed doesn't exist yet.
	if _, err := sqlDB.Exec(`
INSERT INTO channels (id, platform, handle, language, niche, account_ref, status)
VALUES ('blog', 'blog', '', 'en', 'blog', '', 'active')`); err != nil {
		t.Fatalf("seed blog channel: %v", err)
	}
	return sqlDB
}

type fakeCompleter struct {
	responses []llm.Response
	err       error
	calls     int
	lastReq   llm.Request
	lastTask  llm.Task
}

func (f *fakeCompleter) Complete(_ context.Context, task llm.Task, req llm.Request) (llm.Response, error) {
	f.lastTask = task
	f.lastReq = req
	f.calls++
	if f.err != nil {
		return llm.Response{}, f.err
	}
	i := f.calls - 1
	if i >= len(f.responses) {
		i = len(f.responses) - 1
	}
	if i < 0 {
		return llm.Response{}, errors.New("no fake responses configured")
	}
	return f.responses[i], nil
}

func threadResponse(tweets ...string) llm.Response {
	raw, _ := json.Marshal(map[string]any{"tweets": tweets})
	return llm.Response{Text: string(raw), Provider: "fake", Model: "fake-1"}
}

type fakeApprovalStarter struct {
	calls []struct {
		contentID, kind, summary, previewPath string
	}
	nextID int
	err    error
}

func (f *fakeApprovalStarter) Start(_ context.Context, contentID, kind, summary, previewPath string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.calls = append(f.calls, struct{ contentID, kind, summary, previewPath string }{contentID, kind, summary, previewPath})
	f.nextID++
	return "ap-" + string(rune('0'+f.nextID)), nil
}

func sampleSource() SourcePost {
	return SourcePost{
		ContentID: "canonical-1",
		Title:     "Why most side projects never ship",
		URL:       "https://mayankbuilt.com/blog/why-side-projects-never-ship",
		Body: "Most side projects die in the first two weeks, not because the idea was bad, " +
			"but because there was never a real deadline or a real audience waiting. " +
			"This post walks through the three commitments that got my last project across the finish line.",
	}
}

func newTestRepurposer(sqlDB *sql.DB, completer *fakeCompleter, approval *fakeApprovalStarter) *XRepurposer {
	n := 0
	return &XRepurposer{
		DB:       sqlDB,
		LLM:      completer,
		Approval: approval,
		Now:      func() time.Time { return time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC) },
		NewID: func() string {
			n++
			return "content-" + string(rune('0'+n))
		},
	}
}

func TestXRepurposer_StartHappyPath(t *testing.T) {
	sqlDB := openBlogDB(t)
	src := sampleSource()
	completer := &fakeCompleter{responses: []llm.Response{threadResponse(
		"Shipping beats perfect every single time — here's the 3-part deadline trick that finally got my side project out the door.",
		"1) A real deadline, told to a real person. 2) A landing page before the code. 3) Ship the ugly version first.",
		"Full breakdown + the exact commitments: https://mayankbuilt.com/blog/why-side-projects-never-ship",
	)}}
	approval := &fakeApprovalStarter{}
	r := newTestRepurposer(sqlDB, completer, approval)

	contentID, approvalID, err := r.Start(context.Background(), src)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if contentID == "" || approvalID == "" {
		t.Fatalf("empty ids: content=%q approval=%q", contentID, approvalID)
	}
	if completer.lastTask != llm.TaskBlog {
		t.Fatalf("task=%q want blog (CLAUDE.md: Claude is for Builder and blog only)", completer.lastTask)
	}
	if len(approval.calls) != 1 {
		t.Fatalf("want 1 approval.Start call, got %d", len(approval.calls))
	}
	call := approval.calls[0]
	if call.contentID != contentID || call.kind != "blog" {
		t.Fatalf("approval call=%+v", call)
	}
	if !strings.Contains(call.summary, "Shipping beats perfect") {
		t.Fatalf("summary should carry the hook tweet: %q", call.summary)
	}

	var format, stage, scriptRaw string
	var channelID string
	err = sqlDB.QueryRow(`SELECT channel_id, format, stage, script FROM content_items WHERE id=?`, contentID).
		Scan(&channelID, &format, &stage, &scriptRaw)
	if err != nil {
		t.Fatalf("load content_items: %v", err)
	}
	if channelID != "blog" || format != FormatXThread || stage != "draft" {
		t.Fatalf("content_items row wrong: channel=%q format=%q stage=%q", channelID, format, stage)
	}
	var doc scriptDoc
	if err := json.Unmarshal([]byte(scriptRaw), &doc); err != nil {
		t.Fatalf("script not valid json: %v", err)
	}
	if doc.Platform != PlatformXPersonal {
		t.Fatalf("script.platform=%q want %q", doc.Platform, PlatformXPersonal)
	}
	if len(doc.Tweets) != 3 {
		t.Fatalf("want 3 stored tweets, got %d", len(doc.Tweets))
	}

	// The repurposed text must not be a copy-paste of the source body.
	if strings.Contains(strings.ToLower(src.Body[:40]), strings.ToLower(doc.Tweets[0][:20])) {
		t.Fatal("hook tweet looks copy-pasted from the source body")
	}
	if doc.Tweets[0] == src.Body {
		t.Fatal("thread must not literally equal the source body")
	}
}

func TestXRepurposer_RejectsCopyPastedHook(t *testing.T) {
	sqlDB := openBlogDB(t)
	src := sampleSource()
	// Hook is the source body's opening text, near-verbatim (just
	// whitespace/case differences) — must be rejected.
	copyHook := strings.ToUpper(src.Body[:60])
	completer := &fakeCompleter{responses: []llm.Response{threadResponse(
		copyHook,
		"second tweet",
	)}}
	approval := &fakeApprovalStarter{}
	r := newTestRepurposer(sqlDB, completer, approval)

	_, _, err := r.Start(context.Background(), src)
	if err == nil || !strings.Contains(err.Error(), "copy-pasted") {
		t.Fatalf("want copy-paste rejection, got %v", err)
	}
	if len(approval.calls) != 0 {
		t.Fatal("must not create an approval for a rejected repurpose")
	}
}

func TestXRepurposer_Redo(t *testing.T) {
	sqlDB := openBlogDB(t)
	src := sampleSource()
	completer := &fakeCompleter{responses: []llm.Response{
		threadResponse("original hook about deadlines and shipping", "second tweet", "third tweet with link"),
		threadResponse("redo hook, now mentioning the specific 3-commitment framework directly", "second tweet redo", "third tweet redo with link"),
	}}
	approval := &fakeApprovalStarter{}
	r := newTestRepurposer(sqlDB, completer, approval)

	contentID, _, err := r.Start(context.Background(), src)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	newApprovalID, err := r.Redo(context.Background(), contentID, src, "mention the 3-commitment framework by name in the hook")
	if err != nil {
		t.Fatalf("Redo: %v", err)
	}
	if newApprovalID == "" {
		t.Fatal("empty redo approval id")
	}
	if len(approval.calls) != 2 {
		t.Fatalf("want 2 approval.Start calls (start + redo), got %d", len(approval.calls))
	}
	if completer.calls != 2 {
		t.Fatalf("want 2 llm calls (start + redo), got %d", completer.calls)
	}
	if !strings.Contains(completer.lastReq.Messages[0].Content, "REDO NOTES") {
		t.Fatal("redo prompt must include the human's note")
	}

	var scriptRaw string
	if err := sqlDB.QueryRow(`SELECT script FROM content_items WHERE id=?`, contentID).Scan(&scriptRaw); err != nil {
		t.Fatalf("load: %v", err)
	}
	var doc scriptDoc
	if err := json.Unmarshal([]byte(scriptRaw), &doc); err != nil {
		t.Fatalf("script json: %v", err)
	}
	if doc.RedoNote == "" {
		t.Fatal("stored redo note is empty")
	}
	if !strings.Contains(doc.Tweets[0], "redo hook") {
		t.Fatalf("content_items script was not updated with the redo thread: %+v", doc.Tweets)
	}
}

func TestXRepurposer_MissingSourceFields(t *testing.T) {
	sqlDB := openBlogDB(t)
	r := newTestRepurposer(sqlDB, &fakeCompleter{}, &fakeApprovalStarter{})

	cases := []struct {
		name string
		src  SourcePost
	}{
		{"empty content id", SourcePost{Title: "t", URL: "u", Body: "b"}},
		{"empty title", SourcePost{ContentID: "c", URL: "u", Body: "b"}},
		{"empty url", SourcePost{ContentID: "c", Title: "t", Body: "b"}},
		{"empty body", SourcePost{ContentID: "c", Title: "t", URL: "u"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := r.Start(context.Background(), tc.src)
			if err == nil {
				t.Fatal("want validation error")
			}
		})
	}
}

func TestXRepurposer_LLMSchemaError(t *testing.T) {
	sqlDB := openBlogDB(t)
	completer := &fakeCompleter{responses: []llm.Response{{Text: `{"not_tweets": []}`}}}
	r := newTestRepurposer(sqlDB, completer, &fakeApprovalStarter{})

	_, _, err := r.Start(context.Background(), sampleSource())
	if err == nil {
		t.Fatal("want schema validation error")
	}
}

func TestXRepurposer_LLMError(t *testing.T) {
	sqlDB := openBlogDB(t)
	completer := &fakeCompleter{err: errors.New("provider down")}
	r := newTestRepurposer(sqlDB, completer, &fakeApprovalStarter{})

	_, _, err := r.Start(context.Background(), sampleSource())
	if err == nil || !strings.Contains(err.Error(), "provider down") {
		t.Fatalf("want wrapped provider error, got %v", err)
	}
}

// --- pure-function unit tests (no DB/LLM) -------------------------------

func TestEnforceTweetLength(t *testing.T) {
	longWord := strings.Repeat("a", 300)
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"under limit unchanged", "short tweet", false},
		{"exactly at limit unchanged", strings.Repeat("a", 280), false},
		{"over limit truncates at word boundary", strings.Repeat("word ", 60), false}, // 300 chars, has many spaces
		{"over limit, no word boundary near cut, rejected", longWord, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := enforceTweetLength(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got %q", out)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len([]rune(out)) > tweetMaxChars {
				t.Fatalf("output %d runes exceeds %d", len([]rune(out)), tweetMaxChars)
			}
			if len([]rune(tc.in)) > tweetMaxChars {
				if !strings.HasSuffix(out, "…") {
					t.Fatalf("truncated output must end with an ellipsis marker, got %q", out)
				}
				if strings.HasSuffix(strings.TrimSuffix(out, "…"), " ") {
					t.Fatalf("must not leave trailing space before the ellipsis: %q", out)
				}
				// Never cut mid-word: the truncated text (minus the marker)
				// must be a prefix ending exactly at a word boundary in the
				// original string.
				base := strings.TrimSuffix(out, "…")
				if base != "" {
					nextRune := tc.in[len(base):]
					if nextRune != "" && nextRune[0] != ' ' {
						t.Fatalf("cut mid-word: base=%q next=%q", base, nextRune)
					}
				}
			}
		})
	}
}

func TestNormalizeThread_CapsThreadLength(t *testing.T) {
	tweets := make([]string, maxThreadTweets+5)
	for i := range tweets {
		tweets[i] = "tweet number filler text here"
	}
	out, err := normalizeThread(Thread{Tweets: tweets}, sampleSource())
	if err != nil {
		t.Fatalf("normalizeThread: %v", err)
	}
	if len(out.Tweets) != maxThreadTweets {
		t.Fatalf("want thread capped at %d, got %d", maxThreadTweets, len(out.Tweets))
	}
}

func TestNormalizeThread_EmptyRejected(t *testing.T) {
	_, err := normalizeThread(Thread{}, sampleSource())
	if err == nil {
		t.Fatal("want error for empty thread")
	}
}

func TestLooksCopyPasted(t *testing.T) {
	body := "Most side projects die in the first two weeks, not because the idea was bad."
	cases := []struct {
		name string
		hook string
		want bool
	}{
		{"verbatim prefix", body[:50], true},
		{"case/space normalized match", strings.ToUpper(body[:50]), true},
		{"genuinely different hook", "Deadlines you actually keep change everything.", false},
		{"short hook below threshold", "Hi", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := looksCopyPasted(tc.hook, body)
			if got != tc.want {
				t.Fatalf("looksCopyPasted(%q) = %v, want %v", tc.hook, got, tc.want)
			}
		})
	}
}
