package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mayank2/internal/events"
)

func TestTopicsCreateAndList(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	if _, err := sqlDB.ExecContext(ctx, `
INSERT INTO channels (id, platform, handle, language, niche, status)
VALUES ('ch1', 'youtube', 'test', 'en', 'money_side_hustles', 'active')`); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	bus := events.New(sqlDB)
	h := testServer(t, sqlDB, bus).Handler()
	cookie := login(t, h)

	// Unauthenticated: 401.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/topics", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET status=%d", rr.Code)
	}

	rr = httptest.NewRecorder()
	body, _ := json.Marshal(map[string]any{"channelId": "ch1", "title": "Budgeting for beginners"})
	req = httptest.NewRequest(http.MethodPost, "/api/topics", bytes.NewReader(body))
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated POST status=%d", rr.Code)
	}

	// Empty list initially.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/topics", nil)
	req.AddCookie(cookie)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rr.Code, rr.Body.String())
	}
	var list struct {
		Topics []any `json:"topics"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Topics) != 0 {
		t.Fatalf("want empty topics, got %d", len(list.Topics))
	}

	// Valid create -> 201 + real DB row (not an echo).
	rr = httptest.NewRecorder()
	body, _ = json.Marshal(map[string]any{
		"channelId": "ch1",
		"title":     "  Budgeting for beginners  ",
		"sourceUrl": "https://example.com/article",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/topics", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rr.Code, rr.Body.String())
	}
	var created struct {
		OK    bool `json:"ok"`
		Topic struct {
			ID        string  `json:"id"`
			ChannelID string  `json:"channelId"`
			Title     string  `json:"title"`
			Source    string  `json:"source"`
			SourceURL string  `json:"sourceUrl"`
			Score     float64 `json:"score"`
			Status    string  `json:"status"`
			CreatedAt string  `json:"createdAt"`
		} `json:"topic"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Topic.ID == "" {
		t.Fatalf("no id assigned: %+v", created)
	}
	if created.Topic.Title != "Budgeting for beginners" {
		t.Fatalf("title not trimmed: %q", created.Topic.Title)
	}
	if created.Topic.Source != "manual" {
		t.Fatalf("source=%q want manual", created.Topic.Source)
	}
	if created.Topic.Status != "new" {
		t.Fatalf("status=%q want new", created.Topic.Status)
	}
	if created.Topic.Score <= 0 {
		t.Fatalf("score=%v want >0", created.Topic.Score)
	}

	var count int
	if err := sqlDB.QueryRowContext(ctx, `SELECT count(*) FROM topics WHERE id=? AND channel_id='ch1' AND source='manual'`, created.Topic.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("topic row not persisted, count=%d", count)
	}

	// GET now returns the real row.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/topics?channel_id=ch1", nil)
	req.AddCookie(cookie)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status=%d", rr.Code)
	}
	var listed struct {
		Topics []map[string]any `json:"topics"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Topics) != 1 {
		t.Fatalf("listed=%d want 1: %+v", len(listed.Topics), listed.Topics)
	}

	// Duplicate within the dedup window -> refused.
	rr = httptest.NewRecorder()
	body, _ = json.Marshal(map[string]any{"channelId": "ch1", "title": "budgeting for beginners"})
	req = httptest.NewRequest(http.MethodPost, "/api/topics", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("duplicate status=%d body=%s", rr.Code, rr.Body.String())
	}
}

// TestTopicsCreateDuplicatePunctuation is a regression test for the QA
// re-review finding (M2-120, 2026-09-29): topics.go's dedup normalization
// previously only lowercased and collapsed whitespace (strings.Fields), never
// stripping punctuation, so "AI Tools: 2024!" and "AI Tools 2024" were not
// caught as duplicates even though content.Scout.AddManual's real
// normalizeTitle (regex-stripped) would catch them. Both handlers now share
// content.NormalizeTopicTitle, so a title differing from an existing one only
// by punctuation must be rejected as a duplicate.
func TestTopicsCreateDuplicatePunctuation(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	if _, err := sqlDB.ExecContext(ctx, `
INSERT INTO channels (id, platform, handle, language, niche, status)
VALUES ('ch1', 'youtube', 'test', 'en', 'money_side_hustles', 'active')`); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	bus := events.New(sqlDB)
	h := testServer(t, sqlDB, bus).Handler()
	cookie := login(t, h)

	// First topic, no punctuation.
	rr := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]any{"channelId": "ch1", "title": "AI Tools 2024"})
	req := httptest.NewRequest(http.MethodPost, "/api/topics", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("first create status=%d body=%s", rr.Code, rr.Body.String())
	}

	// Same topic, re-typed with punctuation -> must be caught as a duplicate.
	rr = httptest.NewRecorder()
	body, _ = json.Marshal(map[string]any{"channelId": "ch1", "title": "AI Tools: 2024!"})
	req = httptest.NewRequest(http.MethodPost, "/api/topics", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("punctuation-variant duplicate status=%d body=%s (want 400 — punctuation-only titles must normalize the same as scout.go's normalizeTitle)", rr.Code, rr.Body.String())
	}

	var count int
	if err := sqlDB.QueryRowContext(ctx, `SELECT count(*) FROM topics WHERE channel_id='ch1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected only the first topic to be persisted, got count=%d", count)
	}
}

func TestTopicsCreateValidation(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	if _, err := sqlDB.ExecContext(ctx, `
INSERT INTO channels (id, platform, handle, language, niche, status)
VALUES ('ch1', 'youtube', 'test', 'en', 'money_side_hustles', 'active')`); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	bus := events.New(sqlDB)
	h := testServer(t, sqlDB, bus).Handler()
	cookie := login(t, h)

	cases := []struct {
		name string
		body map[string]any
		raw  string // when set, used instead of body (malformed JSON case)
	}{
		{name: "missing channelId", body: map[string]any{"title": "x"}},
		{name: "empty title", body: map[string]any{"channelId": "ch1", "title": "   "}},
		{name: "unknown channel", body: map[string]any{"channelId": "does-not-exist", "title": "valid title"}},
		{name: "oversized title", body: map[string]any{"channelId": "ch1", "title": strings.Repeat("a", 201)}},
		{name: "control chars in title", body: map[string]any{"channelId": "ch1", "title": "hello\x00world"}},
		{name: "bad sourceUrl scheme", body: map[string]any{"channelId": "ch1", "title": "some topic", "sourceUrl": "javascript:alert(1)"}},
		{name: "shell-injection-looking title accepted only as plain text", body: map[string]any{"channelId": "ch1", "title": "topic; rm -rf / #"}},
		{name: "snake_case channel_id is no longer accepted", body: map[string]any{"channel_id": "ch1", "title": "valid title"}},
		{name: "malformed json", raw: `{"channelId":`},
	}

	acceptedOnce := false
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var reqBody []byte
			if tc.raw != "" {
				reqBody = []byte(tc.raw)
			} else {
				b, _ := json.Marshal(tc.body)
				reqBody = b
			}
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/topics", bytes.NewReader(reqBody))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(cookie)
			h.ServeHTTP(rr, req)

			if tc.name == "shell-injection-looking title accepted only as plain text" {
				// A title containing shell metacharacters is not itself
				// dangerous input (CLAUDE.md: never build a shell string
				// from AI output — nothing here ever does), so it is
				// accepted as ordinary text and stored verbatim, not
				// executed or interpreted.
				if rr.Code != http.StatusCreated {
					t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
				}
				acceptedOnce = true
				return
			}
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status=%d want 400, body=%s", rr.Code, rr.Body.String())
			}
		})
	}
	_ = acceptedOnce
}

func TestTopicsCreate_databaseNotConfigured(t *testing.T) {
	s := &Server{log: nil}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/topics", strings.NewReader(`{"channelId":"x","title":"y"}`))
	// Call handler directly since db is nil (bypassing auth wiring, which
	// needs a full Server); this only proves the nil-db guard.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("handler panicked with nil db: %v", r)
		}
	}()
	s.handleTopicsCreate(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", rr.Code)
	}
}
