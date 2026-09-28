// Pinterest publisher (M2-304): API v5 media register/upload/poll → POST /v5/pins.
// No link cloakers; affiliate disclosure; ≤5 pins/day; one destination link/day max.
package publish

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
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
	JobPublishPinterest = "publish.pinterest"

	pinDailyCap = 5 // COMPLIANCE §4

	pinAffiliateDisclosure = "#ad This pin contains affiliate links."

	pinDefaultPollInterval = 2 * time.Second
	pinDefaultPollTimeout  = 5 * time.Minute
)

// PinterestAPI is the v5 surface (fakeable in tests).
type PinterestAPI interface {
	RegisterMedia(ctx context.Context, tok *oauth2.Token) (mediaID, uploadURL string, uploadParams map[string]string, err error)
	UploadMediaFile(ctx context.Context, uploadURL string, params map[string]string, filename string, data []byte) error
	MediaStatus(ctx context.Context, tok *oauth2.Token, mediaID string) (status string, err error)
	CreatePin(ctx context.Context, tok *oauth2.Token, boardID, title, description, link, mediaID, coverURL string) (pinID, pinURL string, err error)
}

// pinPresigner mints R2 URLs for cover images (D20).
type pinPresigner interface {
	PresignGET(ctx context.Context, key string, expiry time.Duration) (url string, got time.Duration, err error)
}

// Pinterest creates Idea/video pins on a configured board (PlaylistID = board_id).
// Destination link is the first http(s) Tag, or empty.
type Pinterest struct {
	DB           *sql.DB
	Tokens       TokenSourceFor
	API          PinterestAPI
	Presign      pinPresigner
	Now          func() time.Time
	Log          *slog.Logger
	DataRoot     string
	PollInterval time.Duration
	PollTimeout  time.Duration
}

func (p *Pinterest) Platform() string { return "pinterest" }

func (p *Pinterest) now() time.Time {
	if p != nil && p.Now != nil {
		return p.Now().UTC()
	}
	return time.Now().UTC()
}

func (p *Pinterest) log() *slog.Logger {
	if p != nil && p.Log != nil {
		return p.Log
	}
	return slog.Default()
}

func (p *Pinterest) pollInterval() time.Duration {
	if p != nil && p.PollInterval > 0 {
		return p.PollInterval
	}
	return pinDefaultPollInterval
}

func (p *Pinterest) pollTimeout() time.Duration {
	if p != nil && p.PollTimeout > 0 {
		return p.PollTimeout
	}
	return pinDefaultPollTimeout
}

// Publish creates one pin. Idempotent on IdempotencyKey.
func (p *Pinterest) Publish(ctx context.Context, req PublishRequest) (PublishResult, error) {
	var zero PublishResult
	if p == nil || p.DB == nil {
		return zero, fmt.Errorf("publish/pinterest: nil service/db")
	}
	if req.ContentID == "" || req.Account == "" {
		return zero, fmt.Errorf("publish/pinterest: content_id and account required")
	}
	if req.IdempotencyKey == "" {
		return zero, fmt.Errorf("publish/pinterest: idempotency_key required")
	}
	boardID := strings.TrimSpace(req.PlaylistID)
	if boardID == "" {
		return zero, fmt.Errorf("publish/pinterest: board_id required (PlaylistID)")
	}
	if err := content.RequireApproved(ctx, p.DB, req.ContentID); err != nil {
		return zero, err
	}
	if res, ok, err := p.lookupPublished(ctx, req); err != nil {
		return zero, err
	} else if ok {
		return res, nil
	}

	link := pinDestinationLink(req.Tags)
	if link != "" && looksLikeCloaker(link) {
		return zero, fmt.Errorf("publish/pinterest: link cloakers are not allowed")
	}
	if link != "" {
		if err := p.reserveDest(ctx, req.Account, link); err != nil {
			_ = p.failPublication(ctx, req, err.Error())
			return zero, err
		}
	}
	if err := p.reserveCap(ctx, req.Account); err != nil {
		_ = p.failPublication(ctx, req, err.Error())
		return zero, err
	}
	if err := p.markPublishing(ctx, req); err != nil {
		return zero, err
	}

	tok, err := p.token(ctx, req.Account)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "token") {
			_ = p.failPublication(ctx, req, "oauth token unavailable")
		}
		return zero, err
	}

	videoPath, err := p.confine(req.VideoPath)
	if err != nil {
		_ = p.failPublication(ctx, req, err.Error())
		return zero, err
	}
	data, err := os.ReadFile(videoPath)
	if err != nil {
		_ = p.failPublication(ctx, req, err.Error())
		return zero, fmt.Errorf("publish/pinterest: read video: %w", err)
	}

	coverURL, err := p.resolveCoverURL(ctx, req)
	if err != nil {
		_ = p.failPublication(ctx, req, err.Error())
		return zero, err
	}

	api := p.api()
	mediaID, uploadURL, params, err := api.RegisterMedia(ctx, tok)
	if err != nil {
		msg := pinRedact(err.Error())
		_ = p.failPublication(ctx, req, msg)
		return zero, fmt.Errorf("publish/pinterest: register media: %w", err)
	}
	if err := api.UploadMediaFile(ctx, uploadURL, params, filepath.Base(videoPath), data); err != nil {
		msg := pinRedact(err.Error())
		_ = p.failPublication(ctx, req, msg)
		return zero, fmt.Errorf("publish/pinterest: upload media: %w", err)
	}
	if err := p.waitMedia(ctx, tok, mediaID); err != nil {
		msg := pinRedact(err.Error())
		_ = p.failPublication(ctx, req, msg)
		return zero, fmt.Errorf("publish/pinterest: media status: %w", err)
	}

	desc := pinDescription(req.Description, req.PaidPromotion)
	pinID, pinURL, err := api.CreatePin(ctx, tok, boardID, req.Title, desc, link, mediaID, coverURL)
	if err != nil {
		msg := pinRedact(err.Error())
		_ = p.failPublication(ctx, req, msg)
		return zero, fmt.Errorf("publish/pinterest: create pin: %w", err)
	}
	if pinURL == "" {
		pinURL = "https://www.pinterest.com/pin/" + pinID + "/"
	}
	if err := p.completePublication(ctx, req, pinID, pinURL); err != nil {
		return zero, err
	}
	p.log().Info("publish/pinterest: published", "account", req.Account, "content_id", req.ContentID, "pin_id", pinID)
	return PublishResult{ExternalID: pinID, URL: pinURL}, nil
}

func (p *Pinterest) resolveCoverURL(ctx context.Context, req PublishRequest) (string, error) {
	key := strings.TrimSpace(req.ThumbnailPath)
	if key == "" {
		return "", fmt.Errorf("publish/pinterest: cover ThumbnailPath / R2 key required")
	}
	if strings.HasPrefix(key, "http://") || strings.HasPrefix(key, "https://") {
		return key, nil
	}
	if p.Presign == nil {
		return "", fmt.Errorf("publish/pinterest: R2 presigner not configured for cover")
	}
	u, _, err := p.Presign.PresignGET(ctx, key, time.Hour)
	if err != nil {
		return "", fmt.Errorf("publish/pinterest: cover presign: %w", err)
	}
	return u, nil
}

func (p *Pinterest) waitMedia(ctx context.Context, tok *oauth2.Token, mediaID string) error {
	// Wall clock so frozen Now() in tests still times out.
	deadline := time.Now().Add(p.pollTimeout())
	api := p.api()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		status, err := api.MediaStatus(ctx, tok, mediaID)
		if err != nil {
			return err
		}
		switch strings.ToLower(status) {
		case "succeeded":
			return nil
		case "failed":
			return fmt.Errorf("media status failed")
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("media stuck in %q past timeout", status)
		}
		t := time.NewTimer(p.pollInterval())
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

func (p *Pinterest) confine(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("publish/pinterest: empty media path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	root := strings.TrimSpace(p.DataRoot)
	if root == "" {
		return abs, nil
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absRoot, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("publish/pinterest: path escapes data root")
	}
	return abs, nil
}

func (p *Pinterest) token(ctx context.Context, account string) (*oauth2.Token, error) {
	if p.Tokens == nil {
		return nil, fmt.Errorf("publish/pinterest: token source not configured")
	}
	ts, err := p.Tokens(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("publish/pinterest: token for %s: %w", account, err)
	}
	tok, err := ts.Token()
	if err != nil {
		return nil, fmt.Errorf("publish/pinterest: reauth needed: refresh failed for %s: %w", account, err)
	}
	if tok == nil || tok.AccessToken == "" {
		return nil, fmt.Errorf("publish/pinterest: reauth needed: empty access token for %s", account)
	}
	return tok, nil
}

func (p *Pinterest) api() PinterestAPI {
	if p != nil && p.API != nil {
		return p.API
	}
	return &HTTPPinterestAPI{HTTP: http.DefaultClient, Base: "https://api.pinterest.com/v5"}
}

func (p *Pinterest) lookupPublished(ctx context.Context, req PublishRequest) (PublishResult, bool, error) {
	var ext, u, status string
	err := p.DB.QueryRowContext(ctx, `
SELECT COALESCE(external_id,''), COALESCE(url,''), status
FROM publications WHERE idempotency_key=?`, req.IdempotencyKey).Scan(&ext, &u, &status)
	if errors.Is(err, sql.ErrNoRows) {
		if req.PublicationID == "" {
			return PublishResult{}, false, nil
		}
		err = p.DB.QueryRowContext(ctx, `
SELECT COALESCE(external_id,''), COALESCE(url,''), status
FROM publications WHERE id=?`, req.PublicationID).Scan(&ext, &u, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return PublishResult{}, false, nil
		}
	}
	if err != nil {
		return PublishResult{}, false, fmt.Errorf("publish/pinterest: lookup: %w", err)
	}
	if status == "published" && ext != "" {
		return PublishResult{ExternalID: ext, URL: u}, true, nil
	}
	return PublishResult{}, false, nil
}

func (p *Pinterest) markPublishing(ctx context.Context, req PublishRequest) error {
	if req.PublicationID == "" {
		return nil
	}
	_, err := p.DB.ExecContext(ctx, `
UPDATE publications SET status='publishing', error=NULL
WHERE id=? AND status IN ('scheduled','publishing','failed')`, req.PublicationID)
	return err
}

func (p *Pinterest) completePublication(ctx context.Context, req PublishRequest, id, pinURL string) error {
	at := p.now().Format(time.RFC3339Nano)
	if req.PublicationID != "" {
		_, err := p.DB.ExecContext(ctx, `
UPDATE publications SET status='published', external_id=?, url=?, published_at=?, error=NULL WHERE id=?`,
			id, pinURL, at, req.PublicationID)
		return err
	}
	_, err := p.DB.ExecContext(ctx, `
UPDATE publications SET status='published', external_id=?, url=?, published_at=?, error=NULL WHERE idempotency_key=?`,
		id, pinURL, at, req.IdempotencyKey)
	return err
}

func (p *Pinterest) failPublication(ctx context.Context, req PublishRequest, msg string) error {
	if req.PublicationID == "" {
		return nil
	}
	_, err := p.DB.ExecContext(ctx, `UPDATE publications SET status='failed', error=? WHERE id=?`, msg, req.PublicationID)
	return err
}

func (p *Pinterest) reserveCap(ctx context.Context, account string) error {
	return p.reserveQuota(ctx, "pinterest:"+account, pinDailyCap, "daily cap exceeded")
}

func (p *Pinterest) reserveDest(ctx context.Context, account, link string) error {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(link))))
	provider := "pinterest_dest:" + account + ":" + hex.EncodeToString(sum[:8])
	return p.reserveQuota(ctx, provider, 1, "duplicate destination link same day")
}

func (p *Pinterest) reserveQuota(ctx context.Context, provider string, limit int, exceedMsg string) error {
	ws := p.now().UTC().Truncate(24 * time.Hour).Format(time.RFC3339Nano)
	res, err := p.DB.ExecContext(ctx, `
INSERT INTO quotas (provider, window_start, used, "limit")
VALUES (?, ?, 1, ?)
ON CONFLICT(provider, window_start) DO UPDATE SET
  used = used + 1
WHERE quotas.used + 1 <= quotas."limit"`, provider, ws, limit)
	if err != nil {
		return fmt.Errorf("publish/pinterest: quota reserve: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("publish/pinterest: %s", exceedMsg)
	}
	return nil
}

// RegisterHandler wires publish.pinterest onto the net resource class.
func (p *Pinterest) RegisterHandler(q *queue.Queue) {
	if q == nil || p == nil {
		return
	}
	q.Register(JobPublishPinterest, queue.ResourceNet, 5, p.Handle)
}

type pinJobPayload struct {
	PublicationID  string   `json:"publication_id"`
	ContentID      string   `json:"content_id"`
	Account        string   `json:"account"`
	IdempotencyKey string   `json:"idempotency_key"`
	VideoPath      string   `json:"video_path"`
	ThumbnailPath  string   `json:"thumbnail_path"`
	PlaylistID     string   `json:"playlist_id"` // board_id
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	Tags           []string `json:"tags"` // first http(s) = destination link
	PaidPromotion  bool     `json:"paid_promotion"`
}

// Handle runs a publish.pinterest job.
func (p *Pinterest) Handle(ctx context.Context, job queue.Job) (json.RawMessage, error) {
	var pl pinJobPayload
	if err := json.Unmarshal(job.Payload, &pl); err != nil {
		return nil, queue.Permanent(fmt.Errorf("publish/pinterest: payload: %w", err))
	}
	req := PublishRequest{
		PublicationID: pl.PublicationID, ContentID: pl.ContentID, Account: pl.Account,
		IdempotencyKey: pl.IdempotencyKey, VideoPath: pl.VideoPath, ThumbnailPath: pl.ThumbnailPath,
		PlaylistID: pl.PlaylistID, Title: pl.Title, Description: pl.Description,
		Tags: pl.Tags, PaidPromotion: pl.PaidPromotion,
	}
	if req.ContentID == "" && job.ContentID != nil {
		req.ContentID = *job.ContentID
	}
	res, err := p.Publish(ctx, req)
	if err != nil {
		if errors.Is(err, content.ErrNotApproved) {
			return nil, queue.Permanent(err)
		}
		if strings.Contains(err.Error(), "daily cap exceeded") ||
			strings.Contains(err.Error(), "duplicate destination") ||
			strings.Contains(err.Error(), "reauth needed") ||
			strings.Contains(err.Error(), "cloakers") {
			return nil, queue.Permanent(err)
		}
		return nil, err
	}
	raw, _ := json.Marshal(res)
	return raw, nil
}

func pinDestinationLink(tags []string) string {
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if strings.HasPrefix(t, "http://") || strings.HasPrefix(t, "https://") {
			return t
		}
	}
	return ""
}

func looksLikeCloaker(link string) bool {
	lower := strings.ToLower(link)
	for _, host := range []string{"bit.ly/", "tinyurl.com/", "t.co/", "goo.gl/", "ow.ly/", "buff.ly/"} {
		if strings.Contains(lower, host) {
			return true
		}
	}
	return false
}

func pinDescription(desc string, paid bool) string {
	parts := []string{strings.TrimSpace(desc)}
	if paid {
		parts = append(parts, pinAffiliateDisclosure)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func pinRedact(s string) string {
	lower := strings.ToLower(s)
	for _, needle := range []string{"access_token=", "bearer ", "oauth "} {
		for {
			i := strings.Index(lower, needle)
			if i < 0 {
				break
			}
			start := i + len(needle)
			end := start
			for end < len(s) && s[end] != ' ' && s[end] != '&' && s[end] != '"' {
				end++
			}
			s = s[:start] + "[redacted]" + s[end:]
			lower = strings.ToLower(s)
		}
	}
	return s
}

// HTTPPinterestAPI talks to Pinterest API v5.
type HTTPPinterestAPI struct {
	HTTP *http.Client
	Base string
}

func (a *HTTPPinterestAPI) http() *http.Client {
	if a != nil && a.HTTP != nil {
		return a.HTTP
	}
	return http.DefaultClient
}

func (a *HTTPPinterestAPI) base() string {
	if a != nil && strings.TrimSpace(a.Base) != "" {
		return strings.TrimRight(a.Base, "/")
	}
	return "https://api.pinterest.com/v5"
}

func (a *HTTPPinterestAPI) RegisterMedia(ctx context.Context, tok *oauth2.Token) (string, string, map[string]string, error) {
	body, _ := json.Marshal(map[string]string{"media_type": "video"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base()+"/media", bytes.NewReader(body))
	if err != nil {
		return "", "", nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	var out struct {
		MediaID          string            `json:"media_id"`
		UploadURL        string            `json:"upload_url"`
		UploadParameters map[string]string `json:"upload_parameters"`
	}
	if err := pinDoJSON(a.http(), req, &out); err != nil {
		return "", "", nil, err
	}
	if out.MediaID == "" || out.UploadURL == "" {
		return "", "", nil, fmt.Errorf("incomplete media registration")
	}
	return out.MediaID, out.UploadURL, out.UploadParameters, nil
}

func (a *HTTPPinterestAPI) UploadMediaFile(ctx context.Context, uploadURL string, params map[string]string, filename string, data []byte) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range params {
		if strings.EqualFold(k, "Content-Type") {
			continue
		}
		_ = w.WriteField(k, v)
	}
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	_ = w.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	res, err := a.http().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 400 {
		return fmt.Errorf("pinterest upload http %d: %s", res.StatusCode, pinRedact(string(body)))
	}
	return nil
}

func (a *HTTPPinterestAPI) MediaStatus(ctx context.Context, tok *oauth2.Token, mediaID string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.base()+"/media/"+mediaID, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	var out struct {
		Status string `json:"status"`
	}
	if err := pinDoJSON(a.http(), req, &out); err != nil {
		return "", err
	}
	return out.Status, nil
}

func (a *HTTPPinterestAPI) CreatePin(ctx context.Context, tok *oauth2.Token, boardID, title, description, link, mediaID, coverURL string) (string, string, error) {
	payload := map[string]any{
		"board_id":    boardID,
		"title":       title,
		"description": description,
		"media_source": map[string]any{
			"source_type":     "video_id",
			"media_id":        mediaID,
			"cover_image_url": coverURL,
		},
	}
	if link != "" {
		payload["link"] = link // plain outbound URL — no cloakers
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base()+"/pins", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	var out struct {
		ID string `json:"id"`
	}
	if err := pinDoJSON(a.http(), req, &out); err != nil {
		return "", "", err
	}
	if out.ID == "" {
		return "", "", fmt.Errorf("empty pin id")
	}
	return out.ID, "https://www.pinterest.com/pin/" + out.ID + "/", nil
}

func pinDoJSON(client *http.Client, req *http.Request, dest any) error {
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	body = []byte(pinRedact(string(body)))
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return fmt.Errorf("publish/pinterest: reauth needed: http %d: %s", res.StatusCode, string(body))
	}
	if dest != nil && len(body) > 0 {
		if err := json.Unmarshal(body, dest); err != nil {
			return fmt.Errorf("decode pinterest response (http %d): %w", res.StatusCode, err)
		}
	}
	if res.StatusCode >= 400 {
		return fmt.Errorf("pinterest http %d: %s", res.StatusCode, string(body))
	}
	return nil
}
