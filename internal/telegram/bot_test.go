package telegram

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mayank2/internal/db"
	"mayank2/internal/queue"
)

const (
	testToken  = "TEST_BOT_TOKEN_not_real"
	testUserID = int64(111)
	testChatID = int64(222)
)

type fakeAPI struct {
	mu       sync.Mutex
	messages []map[string]any
	edits    []map[string]any
	answers  []string
}

func (f *fakeAPI) handler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) < 2 || !strings.HasPrefix(parts[0], "bot") {
			http.Error(w, "bad path", 404)
			return
		}
		method := parts[1]

		f.mu.Lock()
		defer f.mu.Unlock()

		switch method {
		case "getUpdates":
			writeOK(w, []Update{})
		case "sendMessage":
			var m map[string]any
			_ = json.Unmarshal(body, &m)
			f.messages = append(f.messages, m)
			writeOK(w, map[string]any{"message_id": 1000 + len(f.messages)})
		case "editMessageText", "editMessageCaption":
			var m map[string]any
			_ = json.Unmarshal(body, &m)
			f.edits = append(f.edits, m)
			writeOK(w, true)
		case "answerCallbackQuery":
			var m map[string]any
			_ = json.Unmarshal(body, &m)
			if id, ok := m["callback_query_id"].(string); ok {
				f.answers = append(f.answers, id)
			}
			writeOK(w, true)
		default:
			t.Errorf("unexpected Bot API method %s body=%s", method, truncateBytes(body, 200))
			http.Error(w, "unknown", 500)
		}
	})
}

func writeOK(w http.ResponseWriter, result any) {
	raw, _ := json.Marshal(result)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":     true,
		"result": json.RawMessage(raw),
	})
}

func truncateBytes(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tg.db")
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

func newTestBot(t *testing.T, sqlDB *sql.DB, api *fakeAPI) (*Bot, *queue.Queue) {
	t.Helper()
	srv := httptest.NewServer(api.handler(t))
	t.Cleanup(srv.Close)
	q := queue.New(sqlDB, queue.WithWorkerName("tg-test"))
	bot, err := New(Config{
		Token:  testToken,
		UserID: testUserID,
		ChatID: testChatID,
		DB:     sqlDB,
		Queue:  q,
		ClientOpts: []ClientOption{
			WithBaseURL(srv.URL),
			WithHTTPClient(srv.Client()),
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return bot, q
}

func seedChannel(t *testing.T, sqlDB *sql.DB, id string) {
	t.Helper()
	_, err := sqlDB.Exec(`INSERT INTO channels (id, platform, language, niche, status) VALUES (?, 'youtube', 'en', 'ai', 'active')`, id)
	if err != nil {
		t.Fatalf("seed channel: %v", err)
	}
}

func seedContent(t *testing.T, sqlDB *sql.DB, contentID, channelID string) {
	t.Helper()
	_, err := sqlDB.Exec(`
INSERT INTO content_items (id, channel_id, kind, language, stage, created_at)
VALUES (?, ?, 'short', 'en', 'approval', ?)`, contentID, channelID, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatalf("seed content: %v", err)
	}
}

func seedApproval(t *testing.T, sqlDB *sql.DB, id, contentID, nonce string) {
	t.Helper()
	_, err := sqlDB.Exec(`
INSERT INTO approvals (id, content_id, kind, summary, status, nonce)
VALUES (?, ?, 'short', 'Test short preview', 'pending', ?)`, id, contentID, nonce)
	if err != nil {
		t.Fatalf("seed approval: %v", err)
	}
}

func countEvents(t *testing.T, sqlDB *sql.DB, kind string) int {
	t.Helper()
	var n int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=?`, kind).Scan(&n); err != nil {
		t.Fatalf("countEvents: %v", err)
	}
	return n
}

func msgText(m map[string]any) string {
	if t, ok := m["text"].(string); ok {
		return t
	}
	return ""
}

func TestStrangerIgnored(t *testing.T) {
	sqlDB := testDB(t)
	api := &fakeAPI{}
	bot, _ := newTestBot(t, sqlDB, api)

	err := bot.HandleUpdate(context.Background(), Update{
		UpdateID: 1,
		Message: &Message{
			MessageID: 9,
			From:      &User{ID: 999},
			Chat:      Chat{ID: testChatID},
			Text:      "/status",
		},
	})
	if err != nil {
		t.Fatalf("HandleUpdate: %v", err)
	}
	api.mu.Lock()
	n := len(api.messages)
	api.mu.Unlock()
	if n != 0 {
		t.Fatalf("expected no outbound messages, got %d", n)
	}
	if got := countEvents(t, sqlDB, "telegram.ignored"); got != 1 {
		t.Fatalf("expected 1 telegram.ignored event, got %d", got)
	}
}

func TestStrangerWrongChat(t *testing.T) {
	sqlDB := testDB(t)
	api := &fakeAPI{}
	bot, _ := newTestBot(t, sqlDB, api)

	_ = bot.HandleUpdate(context.Background(), Update{
		UpdateID: 2,
		Message: &Message{
			MessageID: 9,
			From:      &User{ID: testUserID},
			Chat:      Chat{ID: 333},
			Text:      "/help",
		},
	})
	if got := countEvents(t, sqlDB, "telegram.ignored"); got != 1 {
		t.Fatalf("expected ignored event, got %d", got)
	}
}

func TestHelpAllowlisted(t *testing.T) {
	sqlDB := testDB(t)
	api := &fakeAPI{}
	bot, _ := newTestBot(t, sqlDB, api)

	if err := bot.HandleUpdate(context.Background(), Update{
		UpdateID: 3,
		Message: &Message{
			MessageID: 1,
			From:      &User{ID: testUserID},
			Chat:      Chat{ID: testChatID},
			Text:      "/help",
		},
	}); err != nil {
		t.Fatalf("HandleUpdate: %v", err)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.messages) != 1 {
		t.Fatalf("want 1 message, got %d", len(api.messages))
	}
	if !strings.Contains(msgText(api.messages[0]), "/status") {
		t.Fatalf("help text missing commands: %q", msgText(api.messages[0]))
	}
}

func TestDoubleTapIgnored(t *testing.T) {
	sqlDB := testDB(t)
	api := &fakeAPI{}
	bot, _ := newTestBot(t, sqlDB, api)

	seedChannel(t, sqlDB, "yt-ai-en")
	seedContent(t, sqlDB, "c1", "yt-ai-en")
	seedApproval(t, sqlDB, "a1", "c1", "nonce-one")

	cb := func(updateID int64) Update {
		return Update{
			UpdateID: updateID,
			CallbackQuery: &CallbackQuery{
				ID:   "cq1",
				From: User{ID: testUserID},
				Message: &Message{
					MessageID: 55,
					Chat:      Chat{ID: testChatID},
				},
				Data: "approve:a1:nonce-one",
			},
		}
	}

	if err := bot.HandleUpdate(context.Background(), cb(10)); err != nil {
		t.Fatalf("first tap: %v", err)
	}
	var status, nonce string
	_ = sqlDB.QueryRow(`SELECT status, nonce FROM approvals WHERE id='a1'`).Scan(&status, &nonce)
	if status != "approved" || nonce != "" {
		t.Fatalf("after first tap: status=%q nonce=%q", status, nonce)
	}

	api.mu.Lock()
	editsAfterFirst := len(api.edits)
	api.mu.Unlock()

	if err := bot.HandleUpdate(context.Background(), cb(11)); err != nil {
		t.Fatalf("second tap: %v", err)
	}
	api.mu.Lock()
	editsAfterSecond := len(api.edits)
	api.mu.Unlock()
	if editsAfterSecond != editsAfterFirst {
		t.Fatalf("double tap should not edit again: edits %d → %d", editsAfterFirst, editsAfterSecond)
	}
}

func TestPINRequiredForResume(t *testing.T) {
	sqlDB := testDB(t)
	api := &fakeAPI{}
	bot, q := newTestBot(t, sqlDB, api)

	hash, err := HashPIN("4242")
	if err != nil {
		t.Fatalf("HashPIN: %v", err)
	}
	if err := bot.SetPINHash(context.Background(), hash); err != nil {
		t.Fatalf("SetPINHash: %v", err)
	}
	if err := q.PauseAll(context.Background()); err != nil {
		t.Fatalf("PauseAll: %v", err)
	}

	if err := bot.HandleUpdate(context.Background(), Update{
		UpdateID: 20,
		Message: &Message{
			MessageID: 1,
			From:      &User{ID: testUserID},
			Chat:      Chat{ID: testChatID},
			Text:      "/resume all",
		},
	}); err != nil {
		t.Fatalf("resume cmd: %v", err)
	}
	var pauseVal string
	_ = sqlDB.QueryRow(`SELECT value FROM settings WHERE key='pause:all'`).Scan(&pauseVal)
	if pauseVal != "1" {
		t.Fatalf("should still be paused before PIN, got %q", pauseVal)
	}
	api.mu.Lock()
	last := ""
	if len(api.messages) > 0 {
		last = msgText(api.messages[len(api.messages)-1])
	}
	api.mu.Unlock()
	if !strings.Contains(last, "Enter PIN") {
		t.Fatalf("expected PIN prompt, got %q", last)
	}

	// Wrong PIN cancels the pending resume; pause stays on.
	if err := bot.HandleUpdate(context.Background(), Update{
		UpdateID: 21,
		Message: &Message{
			MessageID: 2,
			From:      &User{ID: testUserID},
			Chat:      Chat{ID: testChatID},
			Text:      "0000",
		},
	}); err != nil {
		t.Fatalf("wrong pin: %v", err)
	}
	_ = sqlDB.QueryRow(`SELECT value FROM settings WHERE key='pause:all'`).Scan(&pauseVal)
	if pauseVal != "1" {
		t.Fatalf("wrong PIN must not resume, got pause=%q", pauseVal)
	}

	// Restart resume flow with correct PIN.
	if err := bot.HandleUpdate(context.Background(), Update{
		UpdateID: 22,
		Message: &Message{
			MessageID: 3,
			From:      &User{ID: testUserID},
			Chat:      Chat{ID: testChatID},
			Text:      "/resume all",
		},
	}); err != nil {
		t.Fatalf("resume again: %v", err)
	}
	if err := bot.HandleUpdate(context.Background(), Update{
		UpdateID: 23,
		Message: &Message{
			MessageID: 4,
			From:      &User{ID: testUserID},
			Chat:      Chat{ID: testChatID},
			Text:      "4242",
		},
	}); err != nil {
		t.Fatalf("good pin: %v", err)
	}
	_ = sqlDB.QueryRow(`SELECT value FROM settings WHERE key='pause:all'`).Scan(&pauseVal)
	if pauseVal != "0" {
		t.Fatalf("correct PIN should resume, got pause=%q", pauseVal)
	}
}

func TestApprovalRequestSendsButtons(t *testing.T) {
	sqlDB := testDB(t)
	api := &fakeAPI{}
	bot, q := newTestBot(t, sqlDB, api)
	bot.RegisterHandlers(q)

	seedChannel(t, sqlDB, "yt-ai-en")
	seedContent(t, sqlDB, "c2", "yt-ai-en")
	seedApproval(t, sqlDB, "a2", "c2", "n2")

	job := queue.Job{
		ID:      "j1",
		Type:    "approval.request",
		Payload: json.RawMessage(`{"approval_id":"a2"}`),
	}
	result, err := bot.handleApprovalRequest(context.Background(), job)
	if err != nil {
		t.Fatalf("handleApprovalRequest: %v", err)
	}
	if !bytes.Contains(result, []byte("telegram_message_id")) {
		t.Fatalf("unexpected result %s", result)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.messages) != 1 {
		t.Fatalf("want 1 sendMessage, got %d", len(api.messages))
	}
	raw, _ := json.Marshal(api.messages[0]["reply_markup"])
	markup := string(raw)
	if !strings.Contains(markup, "approve:a2:n2") {
		t.Fatalf("markup missing approve: %s", markup)
	}
	if !strings.Contains(markup, "reject:a2:n2") || !strings.Contains(markup, "redo:a2:n2") {
		t.Fatalf("markup missing reject/redo: %s", markup)
	}
}

func TestRedoAsksForNote(t *testing.T) {
	sqlDB := testDB(t)
	api := &fakeAPI{}
	bot, _ := newTestBot(t, sqlDB, api)

	seedChannel(t, sqlDB, "yt-ai-en")
	seedContent(t, sqlDB, "c3", "yt-ai-en")
	seedApproval(t, sqlDB, "a3", "c3", "n3")

	if err := bot.HandleUpdate(context.Background(), Update{
		UpdateID: 30,
		CallbackQuery: &CallbackQuery{
			ID:   "cq-redo",
			From: User{ID: testUserID},
			Message: &Message{
				MessageID: 70,
				Chat:      Chat{ID: testChatID},
			},
			Data: "redo:a3:n3",
		},
	}); err != nil {
		t.Fatalf("redo callback: %v", err)
	}

	var nonce, status string
	_ = sqlDB.QueryRow(`SELECT nonce, status FROM approvals WHERE id='a3'`).Scan(&nonce, &status)
	if nonce == "n3" {
		t.Fatalf("nonce should be rotated on redo tap, still %q", nonce)
	}
	if status != "pending" {
		t.Fatalf("status should stay pending until note, got %q", status)
	}

	// Double-tap with old nonce ignored.
	if err := bot.HandleUpdate(context.Background(), Update{
		UpdateID: 30,
		CallbackQuery: &CallbackQuery{
			ID:      "cq-redo-2",
			From:    User{ID: testUserID},
			Message: &Message{MessageID: 70, Chat: Chat{ID: testChatID}},
			Data:    "redo:a3:n3",
		},
	}); err != nil {
		t.Fatalf("redo double: %v", err)
	}

	if err := bot.HandleUpdate(context.Background(), Update{
		UpdateID: 31,
		Message: &Message{
			MessageID: 71,
			From:      &User{ID: testUserID},
			Chat:      Chat{ID: testChatID},
			Text:      "hook is weak, try myth-vs-fact",
		},
	}); err != nil {
		t.Fatalf("redo note: %v", err)
	}
	var note string
	_ = sqlDB.QueryRow(`SELECT status, note FROM approvals WHERE id='a3'`).Scan(&status, &note)
	if status != "redo" || note != "hook is weak, try myth-vs-fact" {
		t.Fatalf("got status=%q note=%q", status, note)
	}
}

func TestOffsetStored(t *testing.T) {
	sqlDB := testDB(t)
	api := &fakeAPI{}
	bot, _ := newTestBot(t, sqlDB, api)
	ctx := context.Background()
	if err := bot.setOffset(ctx, 42); err != nil {
		t.Fatalf("setOffset: %v", err)
	}
	got, err := bot.getOffset(ctx)
	if err != nil || got != 42 {
		t.Fatalf("getOffset: got %d err %v", got, err)
	}
}

func TestNoShellFromCommands(t *testing.T) {
	sqlDB := testDB(t)
	api := &fakeAPI{}
	bot, _ := newTestBot(t, sqlDB, api)
	seedChannel(t, sqlDB, "yt-ai-en")

	evil := `$(rm -rf /) && curl http://evil`
	if err := bot.HandleUpdate(context.Background(), Update{
		UpdateID: 40,
		Message: &Message{
			MessageID: 1,
			From:      &User{ID: testUserID},
			Chat:      Chat{ID: testChatID},
			Text:      "/topic " + evil + " yt-ai-en",
		},
	}); err != nil {
		t.Fatalf("topic: %v", err)
	}
	var title string
	if err := sqlDB.QueryRow(`SELECT title FROM topics WHERE channel_id='yt-ai-en'`).Scan(&title); err != nil {
		t.Fatalf("select topic: %v", err)
	}
	if title != evil {
		t.Fatalf("title should be stored as data, got %q", title)
	}
}

func TestPauseAll(t *testing.T) {
	sqlDB := testDB(t)
	api := &fakeAPI{}
	bot, _ := newTestBot(t, sqlDB, api)

	if err := bot.HandleUpdate(context.Background(), Update{
		UpdateID: 50,
		Message: &Message{
			MessageID: 1,
			From:      &User{ID: testUserID},
			Chat:      Chat{ID: testChatID},
			Text:      "/pause all",
		},
	}); err != nil {
		t.Fatalf("pause: %v", err)
	}
	var v string
	_ = sqlDB.QueryRow(`SELECT value FROM settings WHERE key='pause:all'`).Scan(&v)
	if v != "1" {
		t.Fatalf("expected pause:all=1, got %q", v)
	}
}

func TestNewFromEnvDisabled(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	_, err := NewFromEnv(testDB(t), nil, nil)
	if err != ErrDisabled {
		t.Fatalf("want ErrDisabled, got %v", err)
	}
}
