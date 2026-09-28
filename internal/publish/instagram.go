// Instagram Reels publisher (M2-301): Meta Graph API container → poll → media_publish.
// Official APIs only; RequireApproved before any Graph call; idempotent on publications.idempotency_key.
package publish

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"mayank2/internal/content"
	"mayank2/internal/queue"
)

const (
	JobPublishInstagram = "publish.instagram"

	igGraphVersion = "v21.0"
	igDailyCap     = 3 // COMPLIANCE §4: Instagram Reels ≤ 3/day per account

	igAffiliateDisclosure = "#ad Paid partnership"
	igSyntheticDisclosure = "Includes AI-generated media"

	// Meta docs: check status once per minute, for no more than ~5 minutes.
	igDefaultPollInterval = time.Minute
	igDefaultPollTimeout  = 5 * time.Minute
)

// InstagramAPI is the Graph surface used by Instagram (fakeable in tests).
type InstagramAPI interface {
	CreateReelContainer(ctx context.Context, tok *oauth2.Token, igUserID, videoURL, caption string) (containerID string, err error)
	ContainerStatus(ctx context.Context, tok *oauth2.Token, containerID string) (statusCode string, err error)
	PublishContainer(ctx context.Context, tok *oauth2.Token, igUserID, containerID string) (mediaID string, err error)
	MediaPermalink(ctx context.Context, tok *oauth2.Token, mediaID string) (permalink string, err error)
}

// igPresigner mints temporary public GET URLs for R2 object keys (M2-213 / D20).
type igPresigner interface {
	PresignGET(ctx context.Context, key string, expiry time.Duration) (url string, got time.Duration, err error)
}

// Instagram publishes Reels to an IG Professional account linked to a Facebook Page.
// Account is the IG Business Account ID (oauth_tokens.account for platform "meta"), not the Page ID.
type Instagram struct {
	DB           *sql.DB
	Tokens       TokenSourceFor // secrets vault by account ref — never .env, never logged
	API          InstagramAPI
	Presign      igPresigner
	Now          func() time.Time
	Log          *slog.Logger
	PollInterval time.Duration
	PollTimeout  time.Duration
	// R2KeyResolver maps content_id → R2 object key. Nil → use VideoPath as the key.
	R2KeyResolver func(ctx context.Context, contentID, videoPath string) (string, error)
}

func (i *Instagram) Platform() string { return "instagram" }

func (i *Instagram) now() time.Time {
	if i != nil && i.Now != nil {
		return i.Now().UTC()
	}
	return time.Now().UTC()
}

func (i *Instagram) log() *slog.Logger {
	if i != nil && i.Log != nil {
		return i.Log
	}
	return slog.Default()
}

func (i *Instagram) pollInterval() time.Duration {
	if i != nil && i.PollInterval > 0 {
		return i.PollInterval
	}
	return igDefaultPollInterval
}

func (i *Instagram) pollTimeout() time.Duration {
	if i != nil && i.PollTimeout > 0 {
		return i.PollTimeout
	}
	return igDefaultPollTimeout
}

// Publish creates and publishes one Reel. Idempotent on IdempotencyKey.
func (i *Instagram) Publish(ctx context.Context, p PublishRequest) (PublishResult, error) {
	var zero PublishResult
	if i == nil || i.DB == nil {
		return zero, fmt.Errorf("publish/instagram: nil service/db")
	}
	if p.ContentID == "" || p.Account == "" {
		return zero, fmt.Errorf("publish/instagram: content_id and account required")
	}
	if p.IdempotencyKey == "" {
		return zero, fmt.Errorf("publish/instagram: idempotency_key required")
	}
	if err := content.RequireApproved(ctx, i.DB, p.ContentID); err != nil {
		return zero, err
	}
	if res, ok, err := i.lookupPublished(ctx, p); err != nil {
		return zero, err
	} else if ok {
		return res, nil
	}
	if err := i.reserveCap(ctx, p.Account); err != nil {
		_ = i.failPublication(ctx, p, err.Error())
		return zero, err
	}
	if err := i.markPublishing(ctx, p); err != nil {
		return zero, err
	}

	tok, err := i.token(ctx, p.Account)
	if err != nil {
		_ = i.failPublication(ctx, p, "oauth token unavailable")
		return zero, err
	}

	videoURL, err := i.resolvePublicURL(ctx, p)
	if err != nil {
		_ = i.failPublication(ctx, p, err.Error())
		return zero, err
	}

	caption := igCaption(p.Description, p.PaidPromotion, p.ContainsSynthetic)
	api := i.api()
	containerID, err := api.CreateReelContainer(ctx, tok, p.Account, videoURL, caption)
	if err != nil {
		msg := igRedact(err.Error())
		_ = i.failPublication(ctx, p, msg)
		i.log().Error("publish/instagram: create container", "account", p.Account, "content_id", p.ContentID, "error", msg)
		return zero, fmt.Errorf("publish/instagram: create container: %s", msg)
	}

	if err := i.waitContainerFinished(ctx, tok, containerID); err != nil {
		msg := igRedact(err.Error())
		_ = i.failPublication(ctx, p, msg)
		return zero, fmt.Errorf("publish/instagram: container: %s", msg)
	}

	mediaID, err := api.PublishContainer(ctx, tok, p.Account, containerID)
	if err != nil {
		msg := igRedact(err.Error())
		_ = i.failPublication(ctx, p, msg)
		return zero, fmt.Errorf("publish/instagram: media_publish: %s", msg)
	}

	permalink, _ := api.MediaPermalink(ctx, tok, mediaID)
	if permalink == "" {
		permalink = "https://www.instagram.com/reel/" + mediaID + "/"
	}
	if err := i.completePublication(ctx, p, mediaID, permalink); err != nil {
		return zero, err
	}
	i.log().Info("publish/instagram: published", "account", p.Account, "content_id", p.ContentID, "media_id", mediaID)
	return PublishResult{ExternalID: mediaID, URL: permalink}, nil
}

func (i *Instagram) resolvePublicURL(ctx context.Context, p PublishRequest) (string, error) {
	if i.Presign == nil {
		return "", fmt.Errorf("publish/instagram: R2 presigner not configured")
	}
	key := strings.TrimSpace(p.VideoPath)
	if i.R2KeyResolver != nil {
		k, err := i.R2KeyResolver(ctx, p.ContentID, p.VideoPath)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(k) != "" {
			key = k
		}
	}
	if key == "" {
		return "", fmt.Errorf("publish/instagram: empty R2 key / video path")
	}
	u, _, err := i.Presign.PresignGET(ctx, key, time.Hour)
	if err != nil {
		return "", fmt.Errorf("publish/instagram: presign: %w", err)
	}
	if u == "" {
		return "", fmt.Errorf("publish/instagram: empty presigned URL")
	}
	return u, nil
}

func (i *Instagram) waitContainerFinished(ctx context.Context, tok *oauth2.Token, containerID string) error {
	// Wall clock for the poll deadline — i.Now is for DB timestamps/quota windows and
	// may be frozen in tests; hanging here would pin a worker forever.
	deadline := time.Now().UTC().Add(i.pollTimeout())
	api := i.api()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		status, err := api.ContainerStatus(ctx, tok, containerID)
		if err != nil {
			return err
		}
		switch strings.ToUpper(status) {
		case "FINISHED", "PUBLISHED":
			return nil
		case "ERROR", "EXPIRED":
			return fmt.Errorf("container status %s", status)
		}
		if !time.Now().UTC().Before(deadline) {
			return fmt.Errorf("container stuck in %q past timeout", status)
		}
		t := time.NewTimer(i.pollInterval())
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

func (i *Instagram) token(ctx context.Context, account string) (*oauth2.Token, error) {
	if i.Tokens == nil {
		return nil, fmt.Errorf("publish/instagram: token source not configured")
	}
	ts, err := i.Tokens(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("publish/instagram: token for %s: %w", account, err)
	}
	tok, err := ts.Token()
	if err != nil {
		return nil, fmt.Errorf("publish/instagram: refresh token for %s: %w", account, err)
	}
	if tok == nil || tok.AccessToken == "" {
		return nil, fmt.Errorf("publish/instagram: empty access token for %s", account)
	}
	return tok, nil
}

func (i *Instagram) api() InstagramAPI {
	if i != nil && i.API != nil {
		return i.API
	}
	return &HTTPInstagramAPI{HTTP: http.DefaultClient, Base: "https://graph.facebook.com/" + igGraphVersion}
}

func (i *Instagram) lookupPublished(ctx context.Context, p PublishRequest) (PublishResult, bool, error) {
	var ext, u, status string
	err := i.DB.QueryRowContext(ctx, `
SELECT COALESCE(external_id,''), COALESCE(url,''), status
FROM publications WHERE idempotency_key=?`, p.IdempotencyKey).Scan(&ext, &u, &status)
	if errors.Is(err, sql.ErrNoRows) {
		if p.PublicationID == "" {
			return PublishResult{}, false, nil
		}
		err = i.DB.QueryRowContext(ctx, `
SELECT COALESCE(external_id,''), COALESCE(url,''), status
FROM publications WHERE id=?`, p.PublicationID).Scan(&ext, &u, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return PublishResult{}, false, nil
		}
	}
	if err != nil {
		return PublishResult{}, false, fmt.Errorf("publish/instagram: lookup: %w", err)
	}
	if status == "published" && ext != "" {
		return PublishResult{ExternalID: ext, URL: u}, true, nil
	}
	return PublishResult{}, false, nil
}

func (i *Instagram) markPublishing(ctx context.Context, p PublishRequest) error {
	if p.PublicationID == "" {
		return nil
	}
	_, err := i.DB.ExecContext(ctx, `
UPDATE publications SET status='publishing', error=NULL
WHERE id=? AND status IN ('scheduled','publishing','failed')`, p.PublicationID)
	return err
}

func (i *Instagram) completePublication(ctx context.Context, p PublishRequest, mediaID, permalink string) error {
	at := i.now().Format(time.RFC3339Nano)
	if p.PublicationID != "" {
		_, err := i.DB.ExecContext(ctx, `
UPDATE publications
SET status='published', external_id=?, url=?, published_at=?, error=NULL
WHERE id=?`, mediaID, permalink, at, p.PublicationID)
		return err
	}
	_, err := i.DB.ExecContext(ctx, `
UPDATE publications
SET status='published', external_id=?, url=?, published_at=?, error=NULL
WHERE idempotency_key=?`, mediaID, permalink, at, p.IdempotencyKey)
	return err
}

func (i *Instagram) failPublication(ctx context.Context, p PublishRequest, msg string) error {
	if p.PublicationID == "" {
		return nil
	}
	_, err := i.DB.ExecContext(ctx, `
UPDATE publications SET status='failed', error=? WHERE id=?`, msg, p.PublicationID)
	return err
}

func (i *Instagram) reserveCap(ctx context.Context, account string) error {
	provider := "instagram:" + account
	ws := i.now().UTC().Truncate(24 * time.Hour).Format(time.RFC3339Nano)
	res, err := i.DB.ExecContext(ctx, `
INSERT INTO quotas (provider, window_start, used, "limit")
VALUES (?, ?, 1, ?)
ON CONFLICT(provider, window_start) DO UPDATE SET
  used = used + 1
WHERE quotas.used + 1 <= quotas."limit"`, provider, ws, igDailyCap)
	if err != nil {
		return fmt.Errorf("publish/instagram: daily cap reserve: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("publish/instagram: daily cap exceeded")
	}
	return nil
}

// RegisterHandler wires publish.instagram onto the net resource class.
func (i *Instagram) RegisterHandler(q *queue.Queue) {
	if q == nil || i == nil {
		return
	}
	q.Register(JobPublishInstagram, queue.ResourceNet, 5, i.Handle)
}

type igJobPayload struct {
	PublicationID     string `json:"publication_id"`
	ContentID         string `json:"content_id"`
	Account           string `json:"account"`
	IdempotencyKey    string `json:"idempotency_key"`
	VideoPath         string `json:"video_path"`
	Description       string `json:"description"`
	ContainsSynthetic bool   `json:"contains_synthetic"`
	PaidPromotion     bool   `json:"paid_promotion"`
}

// Handle runs a publish.instagram job.
func (i *Instagram) Handle(ctx context.Context, job queue.Job) (json.RawMessage, error) {
	var p igJobPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return nil, queue.Permanent(fmt.Errorf("publish/instagram: payload: %w", err))
	}
	req := PublishRequest{
		PublicationID:     p.PublicationID,
		ContentID:         p.ContentID,
		Account:           p.Account,
		IdempotencyKey:    p.IdempotencyKey,
		VideoPath:         p.VideoPath,
		Description:       p.Description,
		ContainsSynthetic: p.ContainsSynthetic,
		PaidPromotion:     p.PaidPromotion,
	}
	if req.ContentID == "" && job.ContentID != nil {
		req.ContentID = *job.ContentID
	}
	res, err := i.Publish(ctx, req)
	if err != nil {
		if errors.Is(err, content.ErrNotApproved) {
			return nil, queue.Permanent(err)
		}
		if strings.Contains(err.Error(), "daily cap exceeded") || strings.Contains(err.Error(), "reauth needed") {
			return nil, queue.Permanent(err)
		}
		return nil, err
	}
	raw, _ := json.Marshal(res)
	return raw, nil
}

func igCaption(desc string, paid, synthetic bool) string {
	parts := []string{strings.TrimSpace(desc)}
	if paid {
		parts = append(parts, igAffiliateDisclosure)
	}
	if synthetic {
		parts = append(parts, igSyntheticDisclosure)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func igRedact(s string) string {
	lower := strings.ToLower(s)
	for _, needle := range []string{"access_token=", "bearer ", "oauth "} {
		searchFrom := 0
		for {
			idx := strings.Index(lower[searchFrom:], needle)
			if idx < 0 {
				break
			}
			i := searchFrom + idx
			start := i + len(needle)
			if start <= len(s) && strings.HasPrefix(s[start:], "[redacted]") {
				searchFrom = start + len("[redacted]")
				continue
			}
			end := start
			for end < len(s) && s[end] != ' ' && s[end] != '&' && s[end] != '"' {
				end++
			}
			s = s[:start] + "[redacted]" + s[end:]
			lower = strings.ToLower(s)
			searchFrom = start + len("[redacted]")
		}
	}
	return s
}

func igWrapGraph(code int, message string) error {
	msg := strings.TrimSpace(message)
	if code == 190 || code == 102 || code == 10 || code == 200 || code == 283 {
		return fmt.Errorf("publish/instagram: reauth needed: graph code=%d message=%s", code, msg)
	}
	return fmt.Errorf("graph code=%d message=%s", code, msg)
}

func igDoJSON(client *http.Client, req *http.Request, dest any) error {
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	body = []byte(igRedact(string(body)))
	if dest != nil {
		if err := json.Unmarshal(body, dest); err != nil {
			return fmt.Errorf("decode graph response (http %d): %w", res.StatusCode, err)
		}
	}
	if res.StatusCode >= 400 {
		var ge struct {
			Error *struct {
				Message string `json:"message"`
				Code    int    `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &ge)
		if ge.Error != nil {
			return igWrapGraph(ge.Error.Code, ge.Error.Message)
		}
		return fmt.Errorf("graph http %d: %s", res.StatusCode, igRedact(string(body)))
	}
	return nil
}

// HTTPInstagramAPI talks to the Meta Graph API over HTTPS.
type HTTPInstagramAPI struct {
	HTTP *http.Client
	Base string
}

func (a *HTTPInstagramAPI) http() *http.Client {
	if a != nil && a.HTTP != nil {
		return a.HTTP
	}
	return http.DefaultClient
}

func (a *HTTPInstagramAPI) base() string {
	if a != nil && strings.TrimSpace(a.Base) != "" {
		return strings.TrimRight(a.Base, "/")
	}
	return "https://graph.facebook.com/" + igGraphVersion
}

func (a *HTTPInstagramAPI) CreateReelContainer(ctx context.Context, tok *oauth2.Token, igUserID, videoURL, caption string) (string, error) {
	form := url.Values{}
	form.Set("media_type", "REELS")
	form.Set("video_url", videoURL)
	form.Set("caption", caption)
	form.Set("access_token", tok.AccessToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base()+"/"+igUserID+"/media", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var out struct {
		ID    string `json:"id"`
		Error *struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}
	if err := igDoJSON(a.http(), req, &out); err != nil {
		return "", err
	}
	if out.Error != nil {
		return "", igWrapGraph(out.Error.Code, out.Error.Message)
	}
	if out.ID == "" {
		return "", fmt.Errorf("empty container id")
	}
	return out.ID, nil
}

func (a *HTTPInstagramAPI) ContainerStatus(ctx context.Context, tok *oauth2.Token, containerID string) (string, error) {
	u := a.base() + "/" + containerID + "?fields=status_code&access_token=" + url.QueryEscape(tok.AccessToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	var out struct {
		StatusCode string `json:"status_code"`
		Error      *struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}
	if err := igDoJSON(a.http(), req, &out); err != nil {
		return "", err
	}
	if out.Error != nil {
		return "", igWrapGraph(out.Error.Code, out.Error.Message)
	}
	return out.StatusCode, nil
}

func (a *HTTPInstagramAPI) PublishContainer(ctx context.Context, tok *oauth2.Token, igUserID, containerID string) (string, error) {
	form := url.Values{}
	form.Set("creation_id", containerID)
	form.Set("access_token", tok.AccessToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base()+"/"+igUserID+"/media_publish", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var out struct {
		ID    string `json:"id"`
		Error *struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}
	if err := igDoJSON(a.http(), req, &out); err != nil {
		return "", err
	}
	if out.Error != nil {
		return "", igWrapGraph(out.Error.Code, out.Error.Message)
	}
	if out.ID == "" {
		return "", fmt.Errorf("empty media id")
	}
	return out.ID, nil
}

func (a *HTTPInstagramAPI) MediaPermalink(ctx context.Context, tok *oauth2.Token, mediaID string) (string, error) {
	u := a.base() + "/" + mediaID + "?fields=permalink&access_token=" + url.QueryEscape(tok.AccessToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	var out struct {
		Permalink string `json:"permalink"`
	}
	if err := igDoJSON(a.http(), req, &out); err != nil {
		return "", err
	}
	return out.Permalink, nil
}
