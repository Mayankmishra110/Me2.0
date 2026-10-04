// Manual topics (M2-120): GET/POST /api/topics (SPEC §4). POST is the one
// manual entry point a human has to seed the content pipeline with a topic
// outside the automatic daily scout.topics discovery run. A manually-added
// topic is written into the same `topics` table (ARCHITECTURE §4) that
// content.Scout (M2-202) writes discovered topics into, with source='manual'
// — it does not skip Niche Scout's scoring, it is scored the same way
// content.Scout.AddManual scores a manual topic (same fixed high-confidence
// signal set + content.DefaultScoutManualBoost) so it naturally sorts ahead
// of freshly discovered topics without a separate code path or schema. This
// endpoint does not construct a content.Scout directly: NewScout requires a
// configured signal source (YouTube key or RSS feeds, D24) to build at all,
// which is unrelated to accepting a manually typed topic and would make this
// endpoint fail whenever no discovery source is configured — the opposite of
// D24 ("everything runs with only the keys you have"). The scoring/dedupe
// logic below intentionally mirrors content.Scout.AddManual's behavior.
package httpapi

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"mayank2/internal/content"
	"mayank2/internal/events"
)

const (
	maxTopicTitleLen     = 200
	maxTopicSourceURLLen = 2000
	defaultTopicsLimit   = 100
	maxTopicsLimit       = 500
)

type topicCreateBody struct {
	ChannelID string `json:"channelId"`
	Title     string `json:"title"`
	SourceURL string `json:"sourceUrl"`
}

func (s *Server) handleTopicsCreate(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database not configured")
		return
	}

	var body topicCreateBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	channelID := strings.TrimSpace(body.ChannelID)
	if channelID == "" {
		writeError(w, http.StatusBadRequest, "topics: channelId is required")
		return
	}

	title := strings.TrimSpace(body.Title)
	if title == "" {
		writeError(w, http.StatusBadRequest, "topics: title is required")
		return
	}
	if len([]rune(title)) > maxTopicTitleLen {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("topics: title must be %d characters or fewer", maxTopicTitleLen))
		return
	}
	if hasControlChars(title) {
		writeError(w, http.StatusBadRequest, "topics: title contains invalid control characters")
		return
	}

	sourceURL := strings.TrimSpace(body.SourceURL)
	if sourceURL != "" {
		if len(sourceURL) > maxTopicSourceURLLen {
			writeError(w, http.StatusBadRequest, "topics: sourceUrl is too long")
			return
		}
		if hasControlChars(sourceURL) {
			writeError(w, http.StatusBadRequest, "topics: sourceUrl contains invalid control characters")
			return
		}
		u, err := url.Parse(sourceURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			writeError(w, http.StatusBadRequest, "topics: sourceUrl must be an http(s) URL")
			return
		}
	}

	channelExists, err := s.channelExists(r.Context(), channelID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "topics: channel lookup failed")
		return
	}
	if !channelExists {
		writeError(w, http.StatusBadRequest, "topics: unknown channelId")
		return
	}

	dupe, err := s.recentDuplicateTopic(r.Context(), channelID, title)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "topics: dedup check failed")
		return
	}
	if dupe {
		writeError(w, http.StatusBadRequest, "topics: this topic was already suggested recently")
		return
	}

	now := time.Now().UTC()
	sig := content.TopicSignals{
		Demand:      0.8,
		Freshness:   1.0,
		Fit:         1.0,
		Competition: 0.2,
		ManualBoost: content.DefaultScoutManualBoost,
		Sources:     []string{"manual"},
	}
	base := sig.Demand * sig.Freshness * sig.Fit * (1 - sig.Competition)
	sig.BaseScore = base

	topic := content.Topic{
		ID:        newTopicULID(),
		ChannelID: channelID,
		Title:     title,
		Source:    content.TopicSourceManual,
		SourceURL: sourceURL,
		Score:     base * content.DefaultScoutManualBoost,
		Signals:   sig,
		Status:    content.TopicStatusNew,
		CreatedAt: now,
	}

	if err := s.insertTopic(r.Context(), topic); err != nil {
		writeError(w, http.StatusInternalServerError, "topics: create failed")
		return
	}

	if s.events != nil {
		_, _ = s.events.Emit(r.Context(), events.ActorUser, "topic.added", topic.ID,
			fmt.Sprintf("manual topic added: %s", topic.Title),
			map[string]any{
				"id": topic.ID, "channel_id": topic.ChannelID, "title": topic.Title,
			})
	}

	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "topic": topicToJSON(topic)})
}

func (s *Server) handleTopicsList(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database not configured")
		return
	}

	q := r.URL.Query()
	channelID := strings.TrimSpace(q.Get("channel_id"))
	status := strings.TrimSpace(q.Get("status"))

	limit := defaultTopicsLimit
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		if n > maxTopicsLimit {
			n = maxTopicsLimit
		}
		limit = n
	}

	query := `SELECT id, channel_id, title, source, source_url, score, status, created_at FROM topics WHERE 1=1`
	args := []any{}
	if channelID != "" {
		query += ` AND channel_id=?`
		args = append(args, channelID)
	}
	if status != "" {
		query += ` AND status=?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list topics failed")
		return
	}
	defer rows.Close()

	topics := make([]map[string]any, 0)
	for rows.Next() {
		var id, chID, title, source, st, createdAt string
		var sourceURL sql.NullString
		var score float64
		if err := rows.Scan(&id, &chID, &title, &source, &sourceURL, &score, &st, &createdAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan topics failed")
			return
		}
		var srcURL any
		if sourceURL.Valid {
			srcURL = sourceURL.String
		}
		topics = append(topics, map[string]any{
			"id":        id,
			"channelId": chID,
			"title":     title,
			"source":    source,
			"sourceUrl": srcURL,
			"score":     score,
			"status":    st,
			"createdAt": createdAt,
		})
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "iterate topics failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"topics": topics})
}

func topicToJSON(t content.Topic) map[string]any {
	var srcURL any
	if t.SourceURL != "" {
		srcURL = t.SourceURL
	}
	return map[string]any{
		"id":        t.ID,
		"channelId": t.ChannelID,
		"title":     t.Title,
		"source":    t.Source,
		"sourceUrl": srcURL,
		"score":     t.Score,
		"status":    t.Status,
		"createdAt": t.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func (s *Server) channelExists(ctx context.Context, channelID string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM channels WHERE id=?`, channelID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// recentDuplicateTopic mirrors content.Scout's dedup window (same channel,
// same normalized title, within content.DefaultScoutDedupWindow) so a
// manually re-typed topic gets the same "already suggested recently" refusal
// a rediscovered one would. Title comparison uses content.NormalizeTopicTitle
// (the exact function content.Scout's own dedup/ranking uses), not a local
// reimplementation, so punctuation-only variants (e.g. "AI Tools: 2024!" vs
// "AI Tools 2024") are caught the same way here as they would be by
// content.Scout.AddManual (M2-120 QA re-review fix, 2026-09-29).
func (s *Server) recentDuplicateTopic(ctx context.Context, channelID, title string) (bool, error) {
	since := time.Now().UTC().Add(-content.DefaultScoutDedupWindow).Format(time.RFC3339Nano)
	rows, err := s.db.QueryContext(ctx,
		`SELECT title FROM topics WHERE channel_id=? AND created_at>=?`, channelID, since)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	want := content.NormalizeTopicTitle(title)
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return false, err
		}
		if content.NormalizeTopicTitle(t) == want {
			return true, nil
		}
	}
	return false, rows.Err()
}

// hasControlChars rejects raw control bytes (including NUL) so a topic title
// can't smuggle terminal/shell escape sequences into logs or, later,
// LLM/pipeline input. Titles are single-line, so even normal whitespace
// control chars (tab, newline) are rejected — CLAUDE.md: child processes use
// fixed argument lists, so this value must never be treated as one, but it
// still becomes downstream LLM prompt input and must be sanitized here.
func hasControlChars(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func (s *Server) insertTopic(ctx context.Context, t content.Topic) error {
	sig, err := json.Marshal(t.Signals)
	if err != nil {
		return fmt.Errorf("httpapi: marshal topic signals: %w", err)
	}
	var sourceURL any
	if t.SourceURL != "" {
		sourceURL = t.SourceURL
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO topics (id, channel_id, title, source, source_url, score, signals, status, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.ChannelID, t.Title, t.Source, sourceURL, t.Score, string(sig), t.Status,
		t.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("httpapi: insert topic: %w", err)
	}
	return nil
}

// newTopicULID is a local Crockford ULID, matching the per-package local
// ULID generator convention already used across internal/content,
// internal/blog, internal/builder etc. (each package keeps its own so none
// of them need to import internal/queue for id generation).
func newTopicULID() string {
	const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	ms := uint64(time.Now().UTC().UnixMilli())
	var buf [26]byte
	for i := 9; i >= 0; i-- {
		buf[i] = crockford[ms&31]
		ms >>= 5
	}
	var rnd [16]byte
	_, _ = rand.Read(rnd[:])
	for i := 10; i < 26; i++ {
		buf[i] = crockford[int(rnd[i-10])%32]
	}
	return string(buf[:])
}
