package blog

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mayank2/internal/db"
	"mayank2/internal/events"
	"mayank2/internal/queue"
	"mayank2/internal/telegram"
)

// --- BuildImportURL ---------------------------------------------------------

func TestBuildImportURL(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		want    string
		wantErr bool
	}{
		{
			name: "simple mayankbuilt post",
			src:  "https://mayankbuilt.com/blog/why-go-for-a-daemon",
			want: "https://medium.com/p/import?url=https%3A%2F%2Fmayankbuilt.com%2Fblog%2Fwhy-go-for-a-daemon",
		},
		{
			name: "trailing slash preserved",
			src:  "https://mayankbuilt.com/blog/shipping-fast/",
			want: "https://medium.com/p/import?url=https%3A%2F%2Fmayankbuilt.com%2Fblog%2Fshipping-fast%2F",
		},
		{
			name: "slug with query string preserved and re-encoded",
			src:  "https://mayankbuilt.com/blog/a-post?utm_source=x",
			want: "https://medium.com/p/import?url=https%3A%2F%2Fmayankbuilt.com%2Fblog%2Fa-post%3Futm_source%3Dx",
		},
		{
			name: "http scheme allowed",
			src:  "http://mayankbuilt.com/blog/legacy-post",
			want: "https://medium.com/p/import?url=http%3A%2F%2Fmayankbuilt.com%2Fblog%2Flegacy-post",
		},
		{
			name:    "empty source",
			src:     "",
			wantErr: true,
		},
		{
			name:    "whitespace-only source",
			src:     "   ",
			wantErr: true,
		},
		{
			name:    "not absolute (no scheme/host)",
			src:     "mayankbuilt.com/blog/x",
			wantErr: true,
		},
		{
			name:    "unsupported scheme",
			src:     "ftp://mayankbuilt.com/blog/x",
			wantErr: true,
		},
		{
			name:    "scheme with no host",
			src:     "https:///blog/x",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := BuildImportURL(tc.src)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("BuildImportURL(%q) = %q, want error", tc.src, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("BuildImportURL(%q) unexpected error: %v", tc.src, err)
			}
			if got != tc.want {
				t.Fatalf("BuildImportURL(%q) = %q, want %q", tc.src, got, tc.want)
			}
			if !strings.HasPrefix(got, MediumImportBaseURL) {
				t.Fatalf("BuildImportURL(%q) = %q, want prefix %q", tc.src, got, MediumImportBaseURL)
			}
		})
	}
}

// --- fake Telegram sender (no real network) ---------------------------------

type fakeSender struct {
	mu    sync.Mutex
	calls []sentMessage
	err   error
}

type sentMessage struct {
	chatID int64
	text   string
	markup *telegram.InlineKeyboardMarkup
}

func (f *fakeSender) SendMessage(_ context.Context, chatID int64, text string, markup *telegram.InlineKeyboardMarkup) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	f.calls = append(f.calls, sentMessage{chatID: chatID, text: text, markup: markup})
	return int64(1000 + len(f.calls)), nil
}

func (f *fakeSender) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeSender) last() sentMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

// --- test DB setup ------------------------------------------------------

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "blog.db")
	sqlDB, err := db.Open(ctx, path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := db.Migrate(ctx, sqlDB); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	return sqlDB
}

// seedContentWithPublication inserts a minimal channel/content_items row and
// optionally a matching `publications` row (platform=mayankbuilt), returning
// the new content_items.id.
func seedContentWithPublication(t *testing.T, sqlDB *sql.DB, publicURL string) string {
	t.Helper()
	ctx := context.Background()

	channelID := "chan_" + t.Name()
	_, err := sqlDB.ExecContext(ctx, `
INSERT INTO channels (id, platform, handle, language, niche, account_ref, status)
VALUES (?, 'blog', 'mayankbuilt', 'en', 'tech', '', 'active')`, channelID)
	if err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	contentID := "content_" + t.Name()
	_, err = sqlDB.ExecContext(ctx, `
INSERT INTO content_items (id, channel_id, kind, format, language, stage, created_at)
VALUES (?, ?, 'blog', '', 'en', '', ?)`, contentID, channelID, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatalf("seed content_items: %v", err)
	}

	if publicURL != "" {
		pubID := "pub_" + t.Name()
		_, err = sqlDB.ExecContext(ctx, `
INSERT INTO publications (id, content_id, platform, account, status, url, idempotency_key, published_at)
VALUES (?, ?, 'mayankbuilt', 'mayankbuilt', 'published', ?, ?, ?)`,
			pubID, contentID, publicURL, "idem_"+t.Name(), time.Now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			t.Fatalf("seed publications: %v", err)
		}
	}

	return contentID
}

func contentStage(t *testing.T, sqlDB *sql.DB, contentID string) string {
	t.Helper()
	var stage string
	if err := sqlDB.QueryRowContext(context.Background(), `SELECT stage FROM content_items WHERE id=?`, contentID).Scan(&stage); err != nil {
		t.Fatalf("read stage: %v", err)
	}
	return stage
}

// --- Handle: happy path -------------------------------------------------

func TestMedium_Handle_SendsLinkAndRecords(t *testing.T) {
	sqlDB := testDB(t)
	contentID := seedContentWithPublication(t, sqlDB, "https://mayankbuilt.com/blog/hello-world")
	sender := &fakeSender{}
	bus := events.New(sqlDB)

	m := &Medium{DB: sqlDB, Bus: bus, Sender: sender, ChatID: 555}

	job := queue.Job{Type: JobBlogMedium, Payload: json.RawMessage(`{"content_id":"` + contentID + `"}`)}
	raw, err := m.Handle(context.Background(), job)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if sender.callCount() != 1 {
		t.Fatalf("expected 1 telegram send, got %d", sender.callCount())
	}
	sent := sender.last()
	if sent.chatID != 555 {
		t.Fatalf("chatID = %d, want 555", sent.chatID)
	}
	wantImport := "https://medium.com/p/import?url=https%3A%2F%2Fmayankbuilt.com%2Fblog%2Fhello-world"
	if !strings.Contains(sent.text, wantImport) {
		t.Fatalf("message text %q does not contain import url %q", sent.text, wantImport)
	}
	if !strings.Contains(sent.text, "https://mayankbuilt.com/blog/hello-world") {
		t.Fatalf("message text %q does not contain the live post url", sent.text)
	}
	if !strings.Contains(strings.ToLower(sent.text), "manual") && !strings.Contains(sent.text, "yourself") {
		t.Fatalf("message text %q does not make clear the import is manual", sent.text)
	}

	if got := contentStage(t, sqlDB, contentID); got != stageMediumLinkSent {
		t.Fatalf("content_items.stage = %q, want %q", got, stageMediumLinkSent)
	}

	var result struct {
		TelegramMessageID int64  `json:"telegram_message_id"`
		ImportURL         string `json:"import_url"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.ImportURL != wantImport {
		t.Fatalf("result.ImportURL = %q, want %q", result.ImportURL, wantImport)
	}

	// events row recorded for dashboard/audit visibility.
	recent, err := bus.Recent(context.Background(), 10)
	if err != nil {
		t.Fatalf("events.Recent: %v", err)
	}
	found := false
	for _, ev := range recent {
		if ev.Kind == EventMediumLinkSent && ev.Ref == contentID {
			found = true
		}
	}
	if !found {
		t.Fatalf("no %s event recorded for content %s; events=%+v", EventMediumLinkSent, contentID, recent)
	}
}

// --- Handle: idempotent no-duplicate-send --------------------------------

func TestMedium_Handle_IdempotentNoDuplicateSend(t *testing.T) {
	sqlDB := testDB(t)
	contentID := seedContentWithPublication(t, sqlDB, "https://mayankbuilt.com/blog/idempotent-post")
	sender := &fakeSender{}
	m := &Medium{DB: sqlDB, Sender: sender, ChatID: 555}

	job := queue.Job{Type: JobBlogMedium, Payload: json.RawMessage(`{"content_id":"` + contentID + `"}`)}

	if _, err := m.Handle(context.Background(), job); err != nil {
		t.Fatalf("first Handle: %v", err)
	}
	if sender.callCount() != 1 {
		t.Fatalf("after first run: expected 1 send, got %d", sender.callCount())
	}

	raw, err := m.Handle(context.Background(), job)
	if err != nil {
		t.Fatalf("second Handle: %v", err)
	}
	if sender.callCount() != 1 {
		t.Fatalf("after second (re-)run: expected still 1 send (no duplicate), got %d", sender.callCount())
	}
	var result struct {
		Skipped string `json:"skipped"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.Skipped != "already_sent" {
		t.Fatalf("result.Skipped = %q, want %q", result.Skipped, "already_sent")
	}

	// A third run (simulating an at-least-once redelivery) still doesn't resend.
	if _, err := m.Handle(context.Background(), job); err != nil {
		t.Fatalf("third Handle: %v", err)
	}
	if sender.callCount() != 1 {
		t.Fatalf("after third run: expected still 1 send, got %d", sender.callCount())
	}
}

// --- Handle: error paths --------------------------------------------------

func TestMedium_Handle_MissingContentID(t *testing.T) {
	sqlDB := testDB(t)
	sender := &fakeSender{}
	m := &Medium{DB: sqlDB, Sender: sender, ChatID: 555}

	job := queue.Job{Type: JobBlogMedium, Payload: json.RawMessage(`{}`)}
	_, err := m.Handle(context.Background(), job)
	if err == nil {
		t.Fatal("expected error for missing content_id")
	}
	if !queue.IsPermanent(err) {
		t.Fatalf("expected a Permanent error, got %v", err)
	}
	if sender.callCount() != 0 {
		t.Fatalf("expected no telegram send, got %d", sender.callCount())
	}
}

func TestMedium_Handle_BadPayload(t *testing.T) {
	sqlDB := testDB(t)
	sender := &fakeSender{}
	m := &Medium{DB: sqlDB, Sender: sender, ChatID: 555}

	job := queue.Job{Type: JobBlogMedium, Payload: json.RawMessage(`not json`)}
	_, err := m.Handle(context.Background(), job)
	if err == nil || !queue.IsPermanent(err) {
		t.Fatalf("expected Permanent decode error, got %v", err)
	}
}

func TestMedium_Handle_UnknownContentID(t *testing.T) {
	sqlDB := testDB(t)
	sender := &fakeSender{}
	m := &Medium{DB: sqlDB, Sender: sender, ChatID: 555}

	job := queue.Job{Type: JobBlogMedium, Payload: json.RawMessage(`{"content_id":"does-not-exist"}`)}
	_, err := m.Handle(context.Background(), job)
	if err == nil {
		t.Fatal("expected error for unknown content_id")
	}
	if !queue.IsPermanent(err) {
		t.Fatalf("expected a Permanent error (row will never appear under this id), got %v", err)
	}
	if sender.callCount() != 0 {
		t.Fatalf("expected no telegram send, got %d", sender.callCount())
	}
}

func TestMedium_Handle_NoPublicationYetIsRetryable(t *testing.T) {
	sqlDB := testDB(t)
	// content_items row exists, but no publications row yet (blog.merge
	// hasn't landed) — should be a plain (retryable) error, not Permanent,
	// and must not send anything.
	contentID := seedContentWithPublication(t, sqlDB, "")
	sender := &fakeSender{}
	m := &Medium{DB: sqlDB, Sender: sender, ChatID: 555}

	job := queue.Job{Type: JobBlogMedium, Payload: json.RawMessage(`{"content_id":"` + contentID + `"}`)}
	_, err := m.Handle(context.Background(), job)
	if err == nil {
		t.Fatal("expected error when no mayankbuilt publication url exists yet")
	}
	if queue.IsPermanent(err) {
		t.Fatalf("expected a retryable (non-Permanent) error, got %v", err)
	}
	if sender.callCount() != 0 {
		t.Fatalf("expected no telegram send, got %d", sender.callCount())
	}
	if got := contentStage(t, sqlDB, contentID); got == stageMediumLinkSent {
		t.Fatalf("stage must not be marked sent when nothing was sent")
	}
}

func TestMedium_Handle_TelegramSendFails(t *testing.T) {
	sqlDB := testDB(t)
	contentID := seedContentWithPublication(t, sqlDB, "https://mayankbuilt.com/blog/send-fails")
	sender := &fakeSender{err: errSend}
	m := &Medium{DB: sqlDB, Sender: sender, ChatID: 555}

	job := queue.Job{Type: JobBlogMedium, Payload: json.RawMessage(`{"content_id":"` + contentID + `"}`)}
	_, err := m.Handle(context.Background(), job)
	if err == nil {
		t.Fatal("expected error when telegram send fails")
	}
	if queue.IsPermanent(err) {
		t.Fatalf("expected a retryable error on transient send failure, got %v", err)
	}
	if got := contentStage(t, sqlDB, contentID); got == stageMediumLinkSent {
		t.Fatalf("stage must not be marked sent when the send failed")
	}
}

func TestMedium_Handle_NilSenderIsPermanent(t *testing.T) {
	sqlDB := testDB(t)
	m := &Medium{DB: sqlDB}
	job := queue.Job{Type: JobBlogMedium, Payload: json.RawMessage(`{"content_id":"x"}`)}
	_, err := m.Handle(context.Background(), job)
	if err == nil || !queue.IsPermanent(err) {
		t.Fatalf("expected Permanent error for nil sender, got %v", err)
	}
}

// --- RegisterHandler wiring -------------------------------------------------

func TestMedium_RegisterHandler(t *testing.T) {
	sqlDB := testDB(t)
	sender := &fakeSender{}
	m := &Medium{DB: sqlDB, Sender: sender, ChatID: 555}
	q := queue.New(sqlDB)
	m.RegisterHandler(q)

	contentID := seedContentWithPublication(t, sqlDB, "https://mayankbuilt.com/blog/via-queue")
	if _, err := q.Enqueue(context.Background(), JobBlogMedium, payload{ContentID: contentID}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	// RegisterHandler is exercised for wiring; running the job through the
	// worker loop is covered indirectly by the queue package's own tests, so
	// here we only assert the job type is accepted (Enqueue would fail with
	// "job type not registered" otherwise).
}

var errSend = &sendError{"telegram: simulated send failure"}

type sendError struct{ msg string }

func (e *sendError) Error() string { return e.msg }
