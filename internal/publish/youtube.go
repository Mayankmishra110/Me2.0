// Package publish adapts platform APIs behind the SPEC §3 Publisher interface.
// YouTube (M2-211): per-channel OAuth, resumable upload, thumbnail, captions,
// optional playlist, quota tracking, idempotent on publications.idempotency_key.
package publish

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"mayank2/internal/content"
	"mayank2/internal/queue"
)

const (
	// JobPublishYouTube is the queue job type (SPEC §5).
	JobPublishYouTube = "publish.youtube"

	quotaProviderYouTube = "youtube"
	// Default YouTube Data API daily unit budget (CONTEXT §6 / PRD).
	DefaultYouTubeDailyQuota = 10000
	// Approximate unit costs (YouTube Data API quota calculator).
	quotaCostVideosInsert   = 1600
	quotaCostThumbnailsSet  = 50
	quotaCostCaptionsInsert = 400
	quotaCostPlaylistInsert = 50

	defaultCategoryID = "27" // Education
	ytUploadURL       = "https://www.googleapis.com/upload/youtube/v3/videos?uploadType=resumable&part=snippet,status"
	ytThumbURL        = "https://www.googleapis.com/upload/youtube/v3/thumbnails/set"
	ytCaptionsURL     = "https://www.googleapis.com/upload/youtube/v3/captions?uploadType=multipart&part=snippet"
	ytPlaylistURL     = "https://www.googleapis.com/youtube/v3/playlistItems?part=snippet"
)

// Publisher is the SPEC §3 platform adapter.
type Publisher interface {
	Platform() string
	Publish(ctx context.Context, p PublishRequest) (PublishResult, error)
}

// PublishRequest is one scheduled publication ready for a platform call.
type PublishRequest struct {
	PublicationID  string
	ContentID      string
	Account        string // Brand Account / oauth_tokens.account
	IdempotencyKey string
	VideoPath      string
	ThumbnailPath  string
	CaptionsPath   string // optional .srt
	PlaylistID     string // optional
	Title          string
	Description    string
	Tags           []string // max 15 (COMPLIANCE)
	CategoryID     string
	Language       string
	ScheduledAt    time.Time
	// ContainsSynthetic sets status.containsSyntheticMedia (altered/synthetic disclosure).
	ContainsSynthetic bool
	// PaidPromotion sets status.containsPaidProductPlacement.
	PaidPromotion bool
}

// PublishResult is the platform identity after a successful publish.
type PublishResult struct {
	ExternalID string
	URL        string
}

// TokenSourceFor returns a refreshing OAuth token source for a YouTube account.
type TokenSourceFor func(ctx context.Context, account string) (oauth2.TokenSource, error)

// YouTubeAPI is the remote surface used by YouTube (fakeable in tests).
type YouTubeAPI interface {
	ResumableUpload(ctx context.Context, tok *oauth2.Token, meta VideoMeta, video io.Reader, size int64) (videoID string, err error)
	SetThumbnail(ctx context.Context, tok *oauth2.Token, videoID string, thumb io.Reader, size int64) error
	UploadCaption(ctx context.Context, tok *oauth2.Token, videoID, language string, srt io.Reader, size int64) error
	AddToPlaylist(ctx context.Context, tok *oauth2.Token, playlistID, videoID string) error
}

// VideoMeta is snippet+status for videos.insert.
type VideoMeta struct {
	Title             string
	Description       string
	Tags              []string
	CategoryID        string
	Language          string
	PrivacyStatus     string // private | public | unlisted
	PublishAt         *time.Time
	MadeForKids       bool
	ContainsSynthetic bool
	PaidPromotion     bool
}

// YouTube is the YouTube Data API v3 publisher for the 4 channels.
type YouTube struct {
	DB         *sql.DB
	Tokens     TokenSourceFor
	API        YouTubeAPI
	QuotaLimit int
	Now        func() time.Time
	Log        *slog.Logger
	DataRoot   string // optional: confine media paths under data/
}

func (y *YouTube) Platform() string { return "youtube" }

func (y *YouTube) now() time.Time {
	if y != nil && y.Now != nil {
		return y.Now().UTC()
	}
	return time.Now().UTC()
}

func (y *YouTube) log() *slog.Logger {
	if y != nil && y.Log != nil {
		return y.Log
	}
	return slog.Default()
}

func (y *YouTube) quotaLimit() int {
	if y != nil && y.QuotaLimit > 0 {
		return y.QuotaLimit
	}
	return DefaultYouTubeDailyQuota
}

// Publish uploads one video. Idempotent on IdempotencyKey / publication row.
func (y *YouTube) Publish(ctx context.Context, p PublishRequest) (PublishResult, error) {
	var zero PublishResult
	if y == nil || y.DB == nil {
		return zero, fmt.Errorf("publish/youtube: nil service/db")
	}
	if p.ContentID == "" || p.Account == "" {
		return zero, fmt.Errorf("publish/youtube: content_id and account required")
	}
	if p.IdempotencyKey == "" {
		return zero, fmt.Errorf("publish/youtube: idempotency_key required")
	}
	if err := content.RequireApproved(ctx, y.DB, p.ContentID); err != nil {
		return zero, err
	}

	// Idempotency: already published → return stored identity.
	if res, ok, err := y.lookupPublished(ctx, p); err != nil {
		return zero, err
	} else if ok {
		return res, nil
	}

	if err := y.markPublishing(ctx, p); err != nil {
		return zero, err
	}

	cost := quotaCostVideosInsert
	if strings.TrimSpace(p.ThumbnailPath) != "" {
		cost += quotaCostThumbnailsSet
	}
	if strings.TrimSpace(p.CaptionsPath) != "" {
		cost += quotaCostCaptionsInsert
	}
	if strings.TrimSpace(p.PlaylistID) != "" {
		cost += quotaCostPlaylistInsert
	}
	if err := y.reserveQuota(ctx, cost); err != nil {
		_ = y.failPublication(ctx, p, err.Error())
		return zero, err
	}

	tok, err := y.token(ctx, p.Account)
	if err != nil {
		_ = y.failPublication(ctx, p, "oauth token unavailable")
		return zero, err
	}

	videoPath, err := y.confine(p.VideoPath)
	if err != nil {
		_ = y.failPublication(ctx, p, err.Error())
		return zero, err
	}
	vf, size, err := openFile(videoPath)
	if err != nil {
		_ = y.failPublication(ctx, p, err.Error())
		return zero, err
	}
	defer vf.Close()

	meta := y.buildMeta(p)
	api := y.api()
	videoID, err := api.ResumableUpload(ctx, tok, meta, vf, size)
	if err != nil {
		_ = y.failPublication(ctx, p, "upload failed")
		y.log().Error("publish/youtube: upload", "account", p.Account, "content_id", p.ContentID, "error", err)
		return zero, fmt.Errorf("publish/youtube: upload: %w", err)
	}

	if thumb := strings.TrimSpace(p.ThumbnailPath); thumb != "" {
		tp, err := y.confine(thumb)
		if err == nil {
			if tf, tsize, err := openFile(tp); err == nil {
				_ = api.SetThumbnail(ctx, tok, videoID, tf, tsize)
				tf.Close()
			}
		}
	}
	if capPath := strings.TrimSpace(p.CaptionsPath); capPath != "" {
		cp, err := y.confine(capPath)
		if err == nil {
			if cf, csize, err := openFile(cp); err == nil {
				lang := p.Language
				if lang == "" {
					lang = "en"
				}
				_ = api.UploadCaption(ctx, tok, videoID, lang, cf, csize)
				cf.Close()
			}
		}
	}
	if pl := strings.TrimSpace(p.PlaylistID); pl != "" {
		_ = api.AddToPlaylist(ctx, tok, pl, videoID)
	}

	url := "https://www.youtube.com/watch?v=" + videoID
	if err := y.completePublication(ctx, p, videoID, url); err != nil {
		return zero, err
	}
	y.log().Info("publish/youtube: published", "account", p.Account, "content_id", p.ContentID, "video_id", videoID)
	return PublishResult{ExternalID: videoID, URL: url}, nil
}

func (y *YouTube) buildMeta(p PublishRequest) VideoMeta {
	cat := p.CategoryID
	if cat == "" {
		cat = defaultCategoryID
	}
	tags := p.Tags
	if len(tags) > 15 {
		tags = tags[:15]
	}
	now := y.now()
	meta := VideoMeta{
		Title:             p.Title,
		Description:       p.Description,
		Tags:              tags,
		CategoryID:        cat,
		Language:          p.Language,
		PrivacyStatus:     "private",
		MadeForKids:       false,
		ContainsSynthetic: p.ContainsSynthetic,
		PaidPromotion:     p.PaidPromotion,
	}
	if !p.ScheduledAt.IsZero() && p.ScheduledAt.After(now.Add(time.Minute)) {
		at := p.ScheduledAt.UTC()
		meta.PublishAt = &at
		meta.PrivacyStatus = "private"
	} else {
		// Slot is now / past → public after private upload path (API sets public).
		meta.PrivacyStatus = "public"
	}
	return meta
}

func (y *YouTube) token(ctx context.Context, account string) (*oauth2.Token, error) {
	if y.Tokens == nil {
		return nil, fmt.Errorf("publish/youtube: token source not configured")
	}
	ts, err := y.Tokens(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("publish/youtube: token for %s: %w", account, err)
	}
	tok, err := ts.Token()
	if err != nil {
		return nil, fmt.Errorf("publish/youtube: refresh token for %s: %w", account, err)
	}
	if tok == nil || tok.AccessToken == "" {
		return nil, fmt.Errorf("publish/youtube: empty access token for %s", account)
	}
	return tok, nil
}

func (y *YouTube) api() YouTubeAPI {
	if y.API != nil {
		return y.API
	}
	return &HTTPYouTubeAPI{HTTP: http.DefaultClient}
}

func (y *YouTube) confine(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("publish/youtube: empty media path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	root := strings.TrimSpace(y.DataRoot)
	if root == "" {
		return abs, nil
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absRoot, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("publish/youtube: path escapes data root")
	}
	return abs, nil
}

func openFile(path string) (*os.File, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("publish/youtube: open %s: %w", filepath.Base(path), err)
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, st.Size(), nil
}

func (y *YouTube) lookupPublished(ctx context.Context, p PublishRequest) (PublishResult, bool, error) {
	var ext, url, status string
	err := y.DB.QueryRowContext(ctx, `
SELECT COALESCE(external_id,''), COALESCE(url,''), status
FROM publications WHERE idempotency_key=?`, p.IdempotencyKey).Scan(&ext, &url, &status)
	if errors.Is(err, sql.ErrNoRows) {
		if p.PublicationID == "" {
			return PublishResult{}, false, nil
		}
		err = y.DB.QueryRowContext(ctx, `
SELECT COALESCE(external_id,''), COALESCE(url,''), status
FROM publications WHERE id=?`, p.PublicationID).Scan(&ext, &url, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return PublishResult{}, false, nil
		}
	}
	if err != nil {
		return PublishResult{}, false, fmt.Errorf("publish/youtube: lookup: %w", err)
	}
	if status == "published" && ext != "" {
		return PublishResult{ExternalID: ext, URL: url}, true, nil
	}
	return PublishResult{}, false, nil
}

func (y *YouTube) markPublishing(ctx context.Context, p PublishRequest) error {
	if p.PublicationID == "" {
		return nil
	}
	_, err := y.DB.ExecContext(ctx, `
UPDATE publications SET status='publishing', error=NULL
WHERE id=? AND status IN ('scheduled','publishing','failed')`, p.PublicationID)
	return err
}

func (y *YouTube) completePublication(ctx context.Context, p PublishRequest, videoID, url string) error {
	at := y.now().Format(time.RFC3339Nano)
	if p.PublicationID != "" {
		_, err := y.DB.ExecContext(ctx, `
UPDATE publications
SET status='published', external_id=?, url=?, published_at=?, error=NULL
WHERE id=?`, videoID, url, at, p.PublicationID)
		return err
	}
	_, err := y.DB.ExecContext(ctx, `
UPDATE publications
SET status='published', external_id=?, url=?, published_at=?, error=NULL
WHERE idempotency_key=?`, videoID, url, at, p.IdempotencyKey)
	return err
}

func (y *YouTube) failPublication(ctx context.Context, p PublishRequest, msg string) error {
	if p.PublicationID == "" {
		return nil
	}
	_, err := y.DB.ExecContext(ctx, `
UPDATE publications SET status='failed', error=? WHERE id=?`, msg, p.PublicationID)
	return err
}

func (y *YouTube) windowStart() string {
	t := y.now().UTC().Truncate(24 * time.Hour)
	return t.Format(time.RFC3339Nano)
}

// reserveQuota atomically spends units for the UTC day window.
func (y *YouTube) reserveQuota(ctx context.Context, units int) error {
	if units <= 0 {
		return nil
	}
	ws := y.windowStart()
	lim := y.quotaLimit()
	res, err := y.DB.ExecContext(ctx, `
INSERT INTO quotas (provider, window_start, used, "limit")
VALUES (?, ?, ?, ?)
ON CONFLICT(provider, window_start) DO UPDATE SET
  used = used + excluded.used
WHERE quotas.used + excluded.used <= quotas."limit"`,
		quotaProviderYouTube, ws, units, lim,
	)
	if err != nil {
		return fmt.Errorf("publish/youtube: quota reserve: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Conflict path when WHERE failed — SQLite ON CONFLICT DO UPDATE WHERE
		// yields 0 rows when the predicate fails. Ensure row exists then recheck.
		var used, limit int
		err := y.DB.QueryRowContext(ctx, `
SELECT used, "limit" FROM quotas WHERE provider=? AND window_start=?`,
			quotaProviderYouTube, ws).Scan(&used, &limit)
		if errors.Is(err, sql.ErrNoRows) {
			_, err = y.DB.ExecContext(ctx, `
INSERT INTO quotas (provider, window_start, used, "limit") VALUES (?, ?, 0, ?)`,
				quotaProviderYouTube, ws, lim)
			if err != nil {
				return fmt.Errorf("publish/youtube: quota init: %w", err)
			}
			used, limit = 0, lim
		} else if err != nil {
			return err
		}
		if used+units > limit {
			return fmt.Errorf("publish/youtube: daily quota exceeded (%d+%d > %d)", used, units, limit)
		}
		res, err = y.DB.ExecContext(ctx, `
UPDATE quotas SET used = used + ?
WHERE provider=? AND window_start=? AND used + ? <= "limit"`,
			units, quotaProviderYouTube, ws, units)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("publish/youtube: daily quota exceeded")
		}
	}
	return nil
}

// RegisterHandler wires publish.youtube onto the net resource class.
func (y *YouTube) RegisterHandler(q *queue.Queue) {
	if q == nil || y == nil {
		return
	}
	q.Register(JobPublishYouTube, queue.ResourceNet, 5, y.Handle)
}

// youtubeJobPayload is the publish.youtube job body.
type youtubeJobPayload struct {
	PublicationID     string   `json:"publication_id"`
	ContentID         string   `json:"content_id"`
	Account           string   `json:"account"`
	IdempotencyKey    string   `json:"idempotency_key"`
	VideoPath         string   `json:"video_path"`
	ThumbnailPath     string   `json:"thumbnail_path"`
	CaptionsPath      string   `json:"captions_path"`
	PlaylistID        string   `json:"playlist_id"`
	Title             string   `json:"title"`
	Description       string   `json:"description"`
	Tags              []string `json:"tags"`
	CategoryID        string   `json:"category_id"`
	Language          string   `json:"language"`
	ScheduledAt       string   `json:"scheduled_at"`
	ContainsSynthetic bool     `json:"contains_synthetic"`
	PaidPromotion     bool     `json:"paid_promotion"`
}

// Handle runs a publish.youtube job.
func (y *YouTube) Handle(ctx context.Context, job queue.Job) (json.RawMessage, error) {
	var p youtubeJobPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return nil, queue.Permanent(fmt.Errorf("publish/youtube: payload: %w", err))
	}
	req := PublishRequest{
		PublicationID:     p.PublicationID,
		ContentID:         p.ContentID,
		Account:           p.Account,
		IdempotencyKey:    p.IdempotencyKey,
		VideoPath:         p.VideoPath,
		ThumbnailPath:     p.ThumbnailPath,
		CaptionsPath:      p.CaptionsPath,
		PlaylistID:        p.PlaylistID,
		Title:             p.Title,
		Description:       p.Description,
		Tags:              p.Tags,
		CategoryID:        p.CategoryID,
		Language:          p.Language,
		ContainsSynthetic: p.ContainsSynthetic,
		PaidPromotion:     p.PaidPromotion,
	}
	if p.ScheduledAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, p.ScheduledAt); err == nil {
			req.ScheduledAt = t
		} else if t, err := time.Parse(time.RFC3339, p.ScheduledAt); err == nil {
			req.ScheduledAt = t
		}
	}
	if req.ContentID == "" && job.ContentID != nil {
		req.ContentID = *job.ContentID
	}
	res, err := y.Publish(ctx, req)
	if err != nil {
		if errors.Is(err, content.ErrNotApproved) {
			return nil, queue.Permanent(err)
		}
		if strings.Contains(err.Error(), "quota exceeded") {
			return nil, queue.Permanent(err)
		}
		return nil, err
	}
	raw, _ := json.Marshal(res)
	return raw, nil
}

// --- HTTP YouTube Data API v3 client -----------------------------------------

// HTTPYouTubeAPI talks to the official YouTube Data API over HTTPS.
type HTTPYouTubeAPI struct {
	HTTP *http.Client
}

func (a *HTTPYouTubeAPI) http() *http.Client {
	if a != nil && a.HTTP != nil {
		return a.HTTP
	}
	return http.DefaultClient
}

func (a *HTTPYouTubeAPI) ResumableUpload(ctx context.Context, tok *oauth2.Token, meta VideoMeta, video io.Reader, size int64) (string, error) {
	body, err := json.Marshal(map[string]any{
		"snippet": map[string]any{
			"title":                meta.Title,
			"description":          meta.Description,
			"tags":                 meta.Tags,
			"categoryId":           meta.CategoryID,
			"defaultLanguage":      meta.Language,
			"defaultAudioLanguage": meta.Language,
		},
		"status": youtubeStatusJSON(meta),
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ytUploadURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("X-Upload-Content-Length", fmt.Sprintf("%d", size))
	req.Header.Set("X-Upload-Content-Type", "video/*")
	resp, err := a.http().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("resumable init HTTP %d: %s", resp.StatusCode, redact(string(b)))
	}
	session := resp.Header.Get("Location")
	if session == "" {
		return "", fmt.Errorf("resumable init: missing Location")
	}
	put, err := http.NewRequestWithContext(ctx, http.MethodPut, session, video)
	if err != nil {
		return "", err
	}
	put.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	put.Header.Set("Content-Type", "video/*")
	put.ContentLength = size
	putResp, err := a.http().Do(put)
	if err != nil {
		return "", err
	}
	defer putResp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(putResp.Body, 1<<20))
	if putResp.StatusCode < 200 || putResp.StatusCode >= 300 {
		return "", fmt.Errorf("resumable put HTTP %d: %s", putResp.StatusCode, redact(string(raw)))
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.ID == "" {
		return "", fmt.Errorf("resumable put: parse id: %w", err)
	}
	return out.ID, nil
}

func youtubeStatusJSON(meta VideoMeta) map[string]any {
	st := map[string]any{
		"privacyStatus":           meta.PrivacyStatus,
		"selfDeclaredMadeForKids": meta.MadeForKids,
		"containsSyntheticMedia":  meta.ContainsSynthetic,
	}
	if meta.PaidPromotion {
		st["containsPaidProductPlacement"] = true
	}
	if meta.PublishAt != nil {
		st["privacyStatus"] = "private"
		st["publishAt"] = meta.PublishAt.UTC().Format(time.RFC3339)
	}
	return st
}

func (a *HTTPYouTubeAPI) SetThumbnail(ctx context.Context, tok *oauth2.Token, videoID string, thumb io.Reader, size int64) error {
	url := ytThumbURL + "?videoId=" + videoID
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, thumb)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = size
	resp, err := a.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("thumbnail HTTP %d: %s", resp.StatusCode, redact(string(b)))
	}
	return nil
}

func (a *HTTPYouTubeAPI) UploadCaption(ctx context.Context, tok *oauth2.Token, videoID, language string, srt io.Reader, size int64) error {
	// Multipart: metadata JSON + srt body. Keep simple binary path for tests via fake.
	meta, _ := json.Marshal(map[string]any{
		"snippet": map[string]any{
			"videoId":  videoID,
			"language": language,
			"name":     language,
		},
	})
	var buf bytes.Buffer
	boundary := "mayank2_caption"
	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString("Content-Type: application/json; charset=UTF-8\r\n\r\n")
	buf.Write(meta)
	buf.WriteString("\r\n--" + boundary + "\r\n")
	buf.WriteString("Content-Type: text/plain\r\n\r\n")
	if _, err := io.Copy(&buf, srt); err != nil {
		return err
	}
	buf.WriteString("\r\n--" + boundary + "--\r\n")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ytCaptionsURL, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Content-Type", "multipart/related; boundary="+boundary)
	resp, err := a.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("captions HTTP %d: %s", resp.StatusCode, redact(string(b)))
	}
	_ = size
	return nil
}

func (a *HTTPYouTubeAPI) AddToPlaylist(ctx context.Context, tok *oauth2.Token, playlistID, videoID string) error {
	body, _ := json.Marshal(map[string]any{
		"snippet": map[string]any{
			"playlistId": playlistID,
			"resourceId": map[string]any{
				"kind":    "youtube#video",
				"videoId": videoID,
			},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ytPlaylistURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("playlistItems HTTP %d: %s", resp.StatusCode, redact(string(b)))
	}
	return nil
}

func redact(s string) string {
	// Never echo bearer-shaped substrings from API error bodies.
	if i := strings.Index(strings.ToLower(s), "bearer "); i >= 0 {
		return s[:i] + "bearer [REDACTED]"
	}
	return s
}
