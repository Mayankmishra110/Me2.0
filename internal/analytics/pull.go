// Package analytics pulls YouTube Analytics metrics and updates growth-loop
// scores (CONTENT_STRATEGY §6, ARCHITECTURE §2). Job: analytics.pull.
package analytics

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"mayank2/internal/queue"
)

const (
	// JobAnalyticsPull is the SPEC §5 job type.
	JobAnalyticsPull = "analytics.pull"

	ScopeTopic    = "topic"
	ScopeFormat   = "format"
	ScopeHook     = "hook"
	ScopeTimeSlot = "time_slot"

	Window24h = "24h"
	Window7d  = "7d"
)

// TokenSourceFor returns OAuth for a YouTube Brand Account.
type TokenSourceFor func(ctx context.Context, account string) (oauth2.TokenSource, error)

// VideoMetrics is one Analytics API snapshot for a video.
type VideoMetrics struct {
	Views        int64
	WatchSeconds float64
	AvgViewPct   float64
	Likes        int64
	Comments     int64
	Shares       int64
	SubsGained   int64
	CTR          float64
}

// AnalyticsAPI is the YouTube Analytics remote surface (fakeable).
type AnalyticsAPI interface {
	PullVideo(ctx context.Context, tok *oauth2.Token, videoID, window string) (VideoMetrics, error)
}

// Service pulls metrics and updates scores.
type Service struct {
	DB     *sql.DB
	Tokens TokenSourceFor
	API    AnalyticsAPI
	Now    func() time.Time
	Log    *slog.Logger
}

func (s *Service) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) log() *slog.Logger {
	if s != nil && s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// PullRequest identifies one publication window to fetch.
type PullRequest struct {
	PublicationID string
	Window        string // 24h | 7d
}

// Pull fetches Analytics for one publication and upserts metrics + scores.
func (s *Service) Pull(ctx context.Context, req PullRequest) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("analytics: nil service/db")
	}
	window := strings.ToLower(strings.TrimSpace(req.Window))
	if window != Window24h && window != Window7d {
		return fmt.Errorf("analytics: window must be 24h or 7d, got %q", req.Window)
	}
	if req.PublicationID == "" {
		return fmt.Errorf("analytics: publication_id required")
	}

	var (
		contentID, platform, account, externalID, status string
		scheduledAt                                      sql.NullString
	)
	err := s.DB.QueryRowContext(ctx, `
SELECT content_id, platform, account, COALESCE(external_id,''), status, scheduled_at
FROM publications WHERE id=?`, req.PublicationID).Scan(
		&contentID, &platform, &account, &externalID, &status, &scheduledAt)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("analytics: publication %s not found", req.PublicationID)
	}
	if err != nil {
		return fmt.Errorf("analytics: load publication: %w", err)
	}
	if platform != "youtube" {
		return fmt.Errorf("analytics: unsupported platform %q", platform)
	}
	if status != "published" || externalID == "" {
		return fmt.Errorf("analytics: publication %s not published yet", req.PublicationID)
	}

	tok, err := s.token(ctx, account)
	if err != nil {
		return err
	}
	api := s.API
	if api == nil {
		api = &HTTPAnalyticsAPI{}
	}
	m, err := api.PullVideo(ctx, tok, externalID, window)
	if err != nil {
		return fmt.Errorf("analytics: pull %s: %w", externalID, err)
	}

	captured := s.now().Format(time.RFC3339Nano)
	_, err = s.DB.ExecContext(ctx, `
INSERT INTO metrics (
  publication_id, captured_at, views, watch_seconds, avg_view_pct,
  likes, comments, shares, subs_gained, ctr
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(publication_id, captured_at) DO UPDATE SET
  views=excluded.views, watch_seconds=excluded.watch_seconds,
  avg_view_pct=excluded.avg_view_pct, likes=excluded.likes,
  comments=excluded.comments, shares=excluded.shares,
  subs_gained=excluded.subs_gained, ctr=excluded.ctr`,
		req.PublicationID, captured, m.Views, m.WatchSeconds, m.AvgViewPct,
		m.Likes, m.Comments, m.Shares, m.SubsGained, m.CTR,
	)
	if err != nil {
		return fmt.Errorf("analytics: insert metrics: %w", err)
	}

	if err := s.updateScores(ctx, contentID, account, scheduledAt.String, m); err != nil {
		return err
	}
	s.log().Info("analytics: pulled",
		"publication_id", req.PublicationID, "window", window, "views", m.Views)
	return nil
}

func (s *Service) token(ctx context.Context, account string) (*oauth2.Token, error) {
	if s.Tokens == nil {
		return nil, fmt.Errorf("analytics: token source not configured")
	}
	ts, err := s.Tokens(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("analytics: token for %s: %w", account, err)
	}
	tok, err := ts.Token()
	if err != nil {
		return nil, fmt.Errorf("analytics: refresh for %s: %w", account, err)
	}
	if tok == nil || tok.AccessToken == "" {
		return nil, fmt.Errorf("analytics: empty access token for %s", account)
	}
	return tok, nil
}

// ScoreFromMetrics maps CONTENT_STRATEGY §6 signals into a 0–1 score.
func ScoreFromMetrics(m VideoMetrics) float64 {
	// Normalize lightly: 10k views → 1.0, 100% avg view → 1.0, 10% CTR → 1.0, 50 subs → 1.0.
	views := clamp01(float64(m.Views) / 10000)
	avg := clamp01(m.AvgViewPct / 100)
	ctr := clamp01(m.CTR / 0.10)
	subs := clamp01(float64(m.SubsGained) / 50)
	shares := clamp01(float64(m.Shares) / 100)
	return clamp01(0.35*views + 0.25*avg + 0.20*ctr + 0.15*subs + 0.05*shares)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func (s *Service) updateScores(ctx context.Context, contentID, channelID, scheduledAt string, m VideoMetrics) error {
	var format, scriptJSON sql.NullString
	var topicID sql.NullString
	err := s.DB.QueryRowContext(ctx, `
SELECT format, script, topic_id FROM content_items WHERE id=?`, contentID).
		Scan(&format, &scriptJSON, &topicID)
	if err != nil {
		return fmt.Errorf("analytics: load content %s: %w", contentID, err)
	}

	score := ScoreFromMetrics(m)
	at := s.now().Format(time.RFC3339Nano)

	// Topic cluster: prefer topics.title; else content id.
	topicKey := "unknown"
	if topicID.Valid && topicID.String != "" {
		var title string
		if err := s.DB.QueryRowContext(ctx, `SELECT title FROM topics WHERE id=?`, topicID.String).Scan(&title); err == nil && title != "" {
			topicKey = clusterKey(title)
		} else {
			topicKey = topicID.String
		}
	}
	if err := upsertScore(ctx, s.DB, ScopeTopic, channelID, topicKey, score, at); err != nil {
		return err
	}

	formatKey := "unknown"
	if format.Valid && strings.TrimSpace(format.String) != "" {
		formatKey = strings.TrimSpace(format.String)
	}
	if err := upsertScore(ctx, s.DB, ScopeFormat, channelID, formatKey, score, at); err != nil {
		return err
	}

	hookKey := "unknown"
	if scriptJSON.Valid && scriptJSON.String != "" {
		var doc struct {
			HookStyle string `json:"hook_style"`
		}
		if json.Unmarshal([]byte(scriptJSON.String), &doc) == nil && strings.TrimSpace(doc.HookStyle) != "" {
			hookKey = strings.TrimSpace(doc.HookStyle)
		}
	}
	if err := upsertScore(ctx, s.DB, ScopeHook, channelID, hookKey, score, at); err != nil {
		return err
	}

	slotKey := timeSlotKey(scheduledAt)
	if err := upsertScore(ctx, s.DB, ScopeTimeSlot, channelID, slotKey, score, at); err != nil {
		return err
	}
	return nil
}

func clusterKey(title string) string {
	t := strings.ToLower(strings.TrimSpace(title))
	fields := strings.Fields(t)
	if len(fields) == 0 {
		return "unknown"
	}
	if len(fields) > 3 {
		fields = fields[:3]
	}
	return strings.Join(fields, "_")
}

func timeSlotKey(scheduledAt string) string {
	if scheduledAt == "" {
		return "unknown"
	}
	t, err := time.Parse(time.RFC3339Nano, scheduledAt)
	if err != nil {
		t, err = time.Parse(time.RFC3339, scheduledAt)
		if err != nil {
			return "unknown"
		}
	}
	// Bucket by local UTC hour window used in CONTENT_STRATEGY (coarse).
	h := t.UTC().Hour()
	switch {
	case h >= 12 && h < 15:
		return "12-15"
	case h >= 18 && h < 22:
		return "18-22"
	default:
		return fmt.Sprintf("%02d", h)
	}
}

func upsertScore(ctx context.Context, db *sql.DB, scope, channelID, key string, sample float64, at string) error {
	var prev float64
	var samples int
	err := db.QueryRowContext(ctx, `
SELECT score, samples FROM scores WHERE scope=? AND channel_id=? AND key=?`,
		scope, channelID, key).Scan(&prev, &samples)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = db.ExecContext(ctx, `
INSERT INTO scores (scope, channel_id, key, score, samples, updated_at)
VALUES (?, ?, ?, ?, 1, ?)`, scope, channelID, key, sample, at)
		return err
	}
	if err != nil {
		return err
	}
	n := samples + 1
	// Running mean.
	next := (prev*float64(samples) + sample) / float64(n)
	_, err = db.ExecContext(ctx, `
UPDATE scores SET score=?, samples=?, updated_at=? WHERE scope=? AND channel_id=? AND key=?`,
		next, n, at, scope, channelID, key)
	return err
}

// ChannelPoint is one metrics sample for the dashboard.
type ChannelPoint struct {
	PublicationID string  `json:"publicationId"`
	CapturedAt    string  `json:"capturedAt"`
	Views         int64   `json:"views"`
	AvgViewPct    float64 `json:"avgViewPct"`
	CTR           float64 `json:"ctr"`
	SubsGained    int64   `json:"subsGained"`
	Shares        int64   `json:"shares"`
	ExternalID    string  `json:"externalId,omitempty"`
	TitleHint     string  `json:"titleHint,omitempty"`
}

// ChannelReport is the payload shape for GET /api/channels/{id}/metrics.
type ChannelReport struct {
	ChannelID string             `json:"channelId"`
	Range     string             `json:"range"`
	Points    []ChannelPoint     `json:"points"`
	Scores    map[string]float64 `json:"scores"` // scope:key → score
}

// QueryChannelMetrics aggregates metrics + scores for the dashboard endpoint.
func QueryChannelMetrics(ctx context.Context, db *sql.DB, channelID, rangeParam string) (ChannelReport, error) {
	out := ChannelReport{
		ChannelID: channelID,
		Range:     rangeParam,
		Points:    []ChannelPoint{},
		Scores:    map[string]float64{},
	}
	if db == nil || channelID == "" {
		return out, fmt.Errorf("analytics: channel_id and db required")
	}
	if rangeParam == "" {
		rangeParam = "7d"
		out.Range = rangeParam
	}
	since := time.Now().UTC()
	switch strings.ToLower(rangeParam) {
	case "24h":
		since = since.Add(-24 * time.Hour)
	case "7d":
		since = since.Add(-7 * 24 * time.Hour)
	case "28d":
		since = since.Add(-28 * 24 * time.Hour)
	default:
		since = since.Add(-7 * 24 * time.Hour)
		out.Range = "7d"
	}
	sinceStr := since.Format(time.RFC3339Nano)

	rows, err := db.QueryContext(ctx, `
SELECT m.publication_id, m.captured_at, m.views, m.avg_view_pct, m.ctr, m.subs_gained, m.shares,
       COALESCE(p.external_id,''), COALESCE(c.format,'')
FROM metrics m
JOIN publications p ON p.id = m.publication_id
JOIN content_items c ON c.id = p.content_id
WHERE c.channel_id=? AND m.captured_at >= ?
ORDER BY m.captured_at DESC
LIMIT 500`, channelID, sinceStr)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var pt ChannelPoint
		if err := rows.Scan(&pt.PublicationID, &pt.CapturedAt, &pt.Views, &pt.AvgViewPct, &pt.CTR,
			&pt.SubsGained, &pt.Shares, &pt.ExternalID, &pt.TitleHint); err != nil {
			return out, err
		}
		out.Points = append(out.Points, pt)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}

	srows, err := db.QueryContext(ctx, `
SELECT scope, key, score FROM scores WHERE channel_id=?`, channelID)
	if err != nil {
		return out, err
	}
	defer srows.Close()
	for srows.Next() {
		var scope, key string
		var score float64
		if err := srows.Scan(&scope, &key, &score); err != nil {
			return out, err
		}
		out.Scores[scope+":"+key] = score
	}
	return out, srows.Err()
}

// EnqueuePulls schedules +24h and +7d analytics.pull jobs after publish.
func EnqueuePulls(ctx context.Context, q *queue.Queue, publicationID, contentID string, publishedAt time.Time) error {
	if q == nil || publicationID == "" {
		return fmt.Errorf("analytics: queue and publication_id required")
	}
	for _, w := range []struct {
		Window string
		After  time.Duration
	}{
		{Window24h, 24 * time.Hour},
		{Window7d, 7 * 24 * time.Hour},
	} {
		payload := map[string]any{
			"publication_id": publicationID,
			"window":         w.Window,
		}
		opts := []queue.EnqueueOpt{queue.RunAt(publishedAt.Add(w.After))}
		if contentID != "" {
			opts = append(opts, queue.ContentID(contentID))
		}
		if _, err := q.Enqueue(ctx, JobAnalyticsPull, payload, opts...); err != nil {
			return fmt.Errorf("analytics: enqueue %s: %w", w.Window, err)
		}
	}
	return nil
}

type pullPayload struct {
	PublicationID string `json:"publication_id"`
	Window        string `json:"window"`
}

// RegisterHandler wires analytics.pull onto ResourceNet.
func (s *Service) RegisterHandler(q *queue.Queue) {
	if q == nil || s == nil {
		return
	}
	q.Register(JobAnalyticsPull, queue.ResourceNet, 5, s.Handle)
}

// Handle runs analytics.pull.
func (s *Service) Handle(ctx context.Context, job queue.Job) (json.RawMessage, error) {
	var p pullPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return nil, queue.Permanent(fmt.Errorf("analytics: payload: %w", err))
	}
	if err := s.Pull(ctx, PullRequest{PublicationID: p.PublicationID, Window: p.Window}); err != nil {
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "not published") {
			return nil, queue.Permanent(err)
		}
		return nil, err
	}
	return json.RawMessage(`{"ok":true}`), nil
}
