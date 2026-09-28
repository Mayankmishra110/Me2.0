package telegram

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mayank2/internal/content"
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
	// ApprovalService.Decide redo enqueues script.write — register a no-op so enqueue succeeds.
	q.Register(content.JobScriptWrite, queue.ResourceLight, 3, func(ctx context.Context, job queue.Job) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	})
	bot, err := New(Config{
		Token:     testToken,
		UserID:    testUserID,
		ChatID:    testChatID,
		DataRoot:  t.TempDir(),
		DB:        sqlDB,
		Queue:     q,
		Approvals: &content.ApprovalService{DB: sqlDB, Enqueue: q},
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
	warmup := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339Nano)
	_, err := sqlDB.Exec(`
INSERT INTO channels (id, platform, language, niche, status, warmup_started_at)
VALUES (?, 'youtube', 'en', 'ai', 'active', ?)`, id, warmup)
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
	var pubs int
	_ = sqlDB.QueryRow(`SELECT COUNT(*) FROM publications WHERE content_id='c1' AND status='scheduled'`).Scan(&pubs)
	if pubs < 1 {
		t.Fatalf("approve must schedule publications, got %d", pubs)
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
	// ApprovalService.Decide enqueues script.write on redo.
	var scriptJobs int
	_ = sqlDB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type='script.write'`).Scan(&scriptJobs)
	if scriptJobs < 1 {
		t.Fatalf("redo must enqueue script.write, got %d jobs", scriptJobs)
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

func TestPINRequiredForPause(t *testing.T) {
	sqlDB := testDB(t)
	api := &fakeAPI{}
	bot, _ := newTestBot(t, sqlDB, api)

	hash, err := HashPIN("1357")
	if err != nil {
		t.Fatalf("HashPIN: %v", err)
	}
	if err := bot.SetPINHash(context.Background(), hash); err != nil {
		t.Fatalf("SetPINHash: %v", err)
	}

	send := func(text string) {
		t.Helper()
		if err := bot.HandleUpdate(context.Background(), Update{
			UpdateID: time.Now().UnixNano(),
			Message: &Message{
				MessageID: time.Now().UnixNano(),
				From:      &User{ID: testUserID},
				Chat:      Chat{ID: testChatID},
				Text:      text,
			},
		}); err != nil {
			t.Fatalf("send %q: %v", text, err)
		}
	}

	send("/pause all")
	api.mu.Lock()
	last := ""
	if len(api.messages) > 0 {
		last = msgText(api.messages[len(api.messages)-1])
	}
	api.mu.Unlock()
	if !strings.Contains(last, "Enter PIN to pause") {
		t.Fatalf("expected pause PIN prompt, got %q", last)
	}

	// Wrong PIN must leave the queue running (no pause:all=1).
	send("0000")
	var pauseVal string
	err = sqlDB.QueryRow(`SELECT value FROM settings WHERE key='pause:all'`).Scan(&pauseVal)
	if err == nil && pauseVal == "1" {
		t.Fatalf("wrong PIN must not pause; pause:all=%q", pauseVal)
	}
	if err != nil && err != sql.ErrNoRows {
		// no row = still running — ok
		t.Fatalf("read pause: %v", err)
	}

	send("/pause all")
	send("1357")
	if err := sqlDB.QueryRow(`SELECT value FROM settings WHERE key='pause:all'`).Scan(&pauseVal); err != nil {
		t.Fatalf("read pause after good PIN: %v", err)
	}
	if pauseVal != "1" {
		t.Fatalf("correct PIN should pause, got %q", pauseVal)
	}
}

func TestTokenRedactedInTransportErrors(t *testing.T) {
	const token = "123456:SECRET-BOT-TOKEN-xyz"
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		// Mimic net/http embedding the full URL (token in path) in the error.
		return nil, fmt.Errorf("Get %q: connection refused", req.URL.String())
	})
	cl := NewClient(token, WithHTTPClient(&http.Client{Transport: transport}))
	_, err := cl.GetUpdates(context.Background(), 0, 0)
	if err == nil {
		t.Fatal("expected transport error")
	}
	msg := err.Error()
	if strings.Contains(msg, token) {
		t.Fatalf("error leaked bot token: %q", msg)
	}
	if !strings.Contains(msg, "[REDACTED]") {
		t.Fatalf("expected [REDACTED] placeholder, got %q", msg)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPreviewPathOutsideDataRootFallsBackToText(t *testing.T) {
	sqlDB := testDB(t)
	api := &fakeAPI{}
	dataRoot := t.TempDir()
	srv := httptest.NewServer(api.handler(t))
	t.Cleanup(srv.Close)
	q := queue.New(sqlDB, queue.WithWorkerName("tg-test"))
	bot, err := New(Config{
		Token:     testToken,
		UserID:    testUserID,
		ChatID:    testChatID,
		DataRoot:  dataRoot,
		DB:        sqlDB,
		Queue:     q,
		Approvals: &content.ApprovalService{DB: sqlDB, Enqueue: q},
		ClientOpts: []ClientOption{
			WithBaseURL(srv.URL),
			WithHTTPClient(srv.Client()),
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	bot.RegisterHandlers(q)

	// File exists but outside dataRoot — must not be uploaded.
	outside := filepath.Join(t.TempDir(), "escape.png")
	if err := os.WriteFile(outside, []byte("fake-png"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	seedChannel(t, sqlDB, "yt-ai-en")
	seedContent(t, sqlDB, "c-esc", "yt-ai-en")
	_, err = sqlDB.Exec(`
INSERT INTO approvals (id, content_id, kind, summary, preview_path, status, nonce)
VALUES ('a-esc', 'c-esc', 'short', 'Outside preview', ?, 'pending', 'n-esc')`, outside)
	if err != nil {
		t.Fatalf("seed approval: %v", err)
	}

	_, err = bot.handleApprovalRequest(context.Background(), queue.Job{
		ID:      "j-esc",
		Type:    "approval.request",
		Payload: json.RawMessage(`{"approval_id":"a-esc"}`),
	})
	if err != nil {
		t.Fatalf("handleApprovalRequest: %v", err)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.messages) != 1 {
		t.Fatalf("want 1 text sendMessage, got %d", len(api.messages))
	}
	// sendPhoto would be a different method; fakeAPI only records sendMessage in messages.
	if msgText(api.messages[0]) != "Outside preview" {
		t.Fatalf("want text fallback caption, got %q", msgText(api.messages[0]))
	}
}

func TestConfineUnderRootRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	if err := confineUnderRoot(root, filepath.Join(root, "..", "outside.png")); err == nil {
		t.Fatal("expected rejection for .. escape")
	}
	if err := confineUnderRoot("", filepath.Join(root, "ok.png")); err == nil {
		t.Fatal("expected rejection when data root empty")
	}
	inside := filepath.Join(root, "ok.png")
	if err := os.WriteFile(inside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := confineUnderRoot(root, inside); err != nil {
		t.Fatalf("inside path should be allowed: %v", err)
	}
}

func TestNewFromEnvDisabled(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	_, err := NewFromEnv(testDB(t), nil, nil)
	if err != ErrDisabled {
		t.Fatalf("want ErrDisabled, got %v", err)
	}
}
