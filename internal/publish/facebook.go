// Facebook Page publisher (M2-302): Meta Graph video_reels start → rupload file_url → finish.
// Page-scoped token via secrets vault; RequireApproved; idempotent; daily cap ≤ 3.
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
	JobPublishFacebook = "publish.facebook"

	fbGraphVersion = "v21.0"
	fbDailyCap     = 3 // COMPLIANCE §4

	fbAffiliateDisclosure = "#ad Paid partnership"
	fbSyntheticDisclosure = "Includes AI-generated media"
)

// ErrReauthNeeded means the Graph API rejected the page-scoped token itself
// (expired/invalid/password-changed/missing-permission), not a transient or
// content-specific failure. Callers should stop retrying and prompt Mayank to
// re-link the Page via the OAuth flow (internal/secrets), instead of looping.
// Check with errors.Is(err, publish.ErrReauthNeeded); the distinction is also
// carried in the message for logs/UI, but errors.Is is the reliable check.
var ErrReauthNeeded = errors.New("publish/facebook: reauth needed")

// ErrDailyCapExceeded means the Facebook daily post cap (COMPLIANCE §4,
// fbDailyCap) was already spent for this account's UTC day; Publish fails
// closed rather than posting past it. Check with errors.Is(err, publish.ErrDailyCapExceeded).
var ErrDailyCapExceeded = errors.New("publish/facebook: daily cap exceeded")

// FacebookAPI is the Graph / rupload surface (fakeable in tests).
// Official Page Reels flow (Meta Video API “Reels publishing” guide, Graph v21):
//  1. POST /{page-id}/video_reels upload_phase=start → video_id
//  2. POST https://rupload.facebook.com/video-upload/{version}/{video_id} with file_url header
//  3. POST /{page-id}/video_reels upload_phase=finish video_state=PUBLISHED
//
// This is distinct from Instagram’s container → media_publish two-step.
type FacebookAPI interface {
	StartReel(ctx context.Context, tok *oauth2.Token, pageID string) (videoID string, err error)
	UploadHosted(ctx context.Context, tok *oauth2.Token, videoID, fileURL string) error
	FinishReel(ctx context.Context, tok *oauth2.Token, pageID, videoID, title, description string) (postID string, err error)
}

// fbPresigner mints R2 presigned GET URLs (D20 / M2-213). Meta’s file_url upload needs a hosted URL.
type fbPresigner interface {
	PresignGET(ctx context.Context, key string, expiry time.Duration) (url string, got time.Duration, err error)
}

// Facebook publishes Reels/video to a Facebook Page using a page-scoped access token.
//
// Token acquisition (Meta): long-lived User token → GET /me/accounts → Page Access Token
// for the target Page ID, stored in the DPAPI vault via internal/secrets (oauth_tokens.account
// = Page ID, not the user). Page tokens are long-lived but can be invalidated by a password
// change or permission review; Graph auth errors (codes 190/102/10/200/283) surface as
// "reauth needed" and are queue.Permanent — never a silent retry loop.
//
// The IG Business Account linked to this Page may share Meta app credentials, but the
// publisher loads a page-scoped token keyed by Page ID separately from Instagram’s IG user id.
type Facebook struct {
	DB            *sql.DB
	Tokens        TokenSourceFor // page-scoped token keyed by Page ID (oauth_tokens.account)
	API           FacebookAPI
	Presign       fbPresigner
	Now           func() time.Time
	Log           *slog.Logger
	R2KeyResolver func(ctx context.Context, contentID, videoPath string) (string, error)
}

func (f *Facebook) Platform() string { return "facebook" }

func (f *Facebook) now() time.Time {
	if f != nil && f.Now != nil {
		return f.Now().UTC()
	}
	return time.Now().UTC()
}

func (f *Facebook) log() *slog.Logger {
	if f != nil && f.Log != nil {
		return f.Log
	}
	return slog.Default()
}

// Publish uploads one Page Reel. Idempotent on IdempotencyKey.
func (f *Facebook) Publish(ctx context.Context, p PublishRequest) (PublishResult, error) {
	var zero PublishResult
	if f == nil || f.DB == nil {
		return zero, fmt.Errorf("publish/facebook: nil service/db")
	}
	if p.ContentID == "" || p.Account == "" {
		return zero, fmt.Errorf("publish/facebook: content_id and account required")
	}
	if p.IdempotencyKey == "" {
		return zero, fmt.Errorf("publish/facebook: idempotency_key required")
	}
	if err := content.RequireApproved(ctx, f.DB, p.ContentID); err != nil {
		return zero, err
	}
	if res, ok, err := f.lookupPublished(ctx, p); err != nil {
		return zero, err
	} else if ok {
		return res, nil
	}
	if err := f.reserveCap(ctx, p.Account); err != nil {
		_ = f.failPublication(ctx, p, err.Error())
		return zero, err
	}
	if err := f.markPublishing(ctx, p); err != nil {
		return zero, err
	}

	tok, err := f.token(ctx, p.Account)
	if err != nil {
		_ = f.failPublication(ctx, p, "oauth token unavailable")
		return zero, err
	}

	fileURL, err := f.resolvePublicURL(ctx, p)
	if err != nil {
		_ = f.failPublication(ctx, p, err.Error())
		return zero, err
	}

	api := f.api()
	videoID, err := api.StartReel(ctx, tok, p.Account)
	if err != nil {
		msg := fbRedact(err.Error())
		_ = f.failPublication(ctx, p, msg)
		return zero, fmt.Errorf("publish/facebook: start: %w", err)
	}
	if err := api.UploadHosted(ctx, tok, videoID, fileURL); err != nil {
		msg := fbRedact(err.Error())
		_ = f.failPublication(ctx, p, msg)
		return zero, fmt.Errorf("publish/facebook: upload: %w", err)
	}
	desc := fbCaption(p.Description, p.PaidPromotion, p.ContainsSynthetic)
	postID, err := api.FinishReel(ctx, tok, p.Account, videoID, p.Title, desc)
	if err != nil {
		msg := fbRedact(err.Error())
		_ = f.failPublication(ctx, p, msg)
		return zero, fmt.Errorf("publish/facebook: finish: %w", err)
	}
	if postID == "" {
		postID = videoID
	}
	postURL := "https://www.facebook.com/reel/" + postID
	if err := f.completePublication(ctx, p, postID, postURL); err != nil {
		return zero, err
	}
	f.log().Info("publish/facebook: published", "account", p.Account, "content_id", p.ContentID, "post_id", postID)
	return PublishResult{ExternalID: postID, URL: postURL}, nil
}

func (f *Facebook) resolvePublicURL(ctx context.Context, p PublishRequest) (string, error) {
	if f.Presign == nil {
		return "", fmt.Errorf("publish/facebook: R2 presigner not configured")
	}
	key := strings.TrimSpace(p.VideoPath)
	if f.R2KeyResolver != nil {
		k, err := f.R2KeyResolver(ctx, p.ContentID, p.VideoPath)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(k) != "" {
			key = k
		}
	}
	if key == "" {
		return "", fmt.Errorf("publish/facebook: empty R2 key")
	}
	u, _, err := f.Presign.PresignGET(ctx, key, time.Hour)
	if err != nil {
		return "", fmt.Errorf("publish/facebook: presign: %w", err)
	}
	if strings.TrimSpace(u) == "" {
		return "", fmt.Errorf("publish/facebook: empty presigned URL")
	}
	return u, nil
}

func (f *Facebook) token(ctx context.Context, account string) (*oauth2.Token, error) {
	if f.Tokens == nil {
		return nil, fmt.Errorf("publish/facebook: token source not configured")
	}
	ts, err := f.Tokens(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("publish/facebook: token for %s: %w", account, err)
	}
	tok, err := ts.Token()
	if err != nil {
		return nil, fmt.Errorf("publish/facebook: refresh token for %s: %w", account, err)
	}
	if tok == nil || tok.AccessToken == "" {
		return nil, fmt.Errorf("publish/facebook: empty access token for %s", account)
	}
	return tok, nil
}

func (f *Facebook) api() FacebookAPI {
	if f != nil && f.API != nil {
		return f.API
	}
	return &HTTPFacebookAPI{
		HTTP:       http.DefaultClient,
		GraphBase:  "https://graph.facebook.com/" + fbGraphVersion,
		UploadBase: "https://rupload.facebook.com/video-upload/" + fbGraphVersion,
	}
}

func (f *Facebook) lookupPublished(ctx context.Context, p PublishRequest) (PublishResult, bool, error) {
	var ext, u, status string
	err := f.DB.QueryRowContext(ctx, `
SELECT COALESCE(external_id,''), COALESCE(url,''), status
FROM publications WHERE idempotency_key=?`, p.IdempotencyKey).Scan(&ext, &u, &status)
	if errors.Is(err, sql.ErrNoRows) {
		if p.PublicationID == "" {
			return PublishResult{}, false, nil
		}
		err = f.DB.QueryRowContext(ctx, `
SELECT COALESCE(external_id,''), COALESCE(url,''), status
FROM publications WHERE id=?`, p.PublicationID).Scan(&ext, &u, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return PublishResult{}, false, nil
		}
	}
	if err != nil {
		return PublishResult{}, false, fmt.Errorf("publish/facebook: lookup: %w", err)
	}
	if status == "published" && ext != "" {
		return PublishResult{ExternalID: ext, URL: u}, true, nil
	}
	return PublishResult{}, false, nil
}

func (f *Facebook) markPublishing(ctx context.Context, p PublishRequest) error {
	if p.PublicationID == "" {
		return nil
	}
	_, err := f.DB.ExecContext(ctx, `
UPDATE publications SET status='publishing', error=NULL
WHERE id=? AND status IN ('scheduled','publishing','failed')`, p.PublicationID)
	return err
}

func (f *Facebook) completePublication(ctx context.Context, p PublishRequest, id, postURL string) error {
	at := f.now().Format(time.RFC3339Nano)
	if p.PublicationID != "" {
		_, err := f.DB.ExecContext(ctx, `
UPDATE publications SET status='published', external_id=?, url=?, published_at=?, error=NULL WHERE id=?`,
			id, postURL, at, p.PublicationID)
		return err
	}
	_, err := f.DB.ExecContext(ctx, `
UPDATE publications SET status='published', external_id=?, url=?, published_at=?, error=NULL WHERE idempotency_key=?`,
		id, postURL, at, p.IdempotencyKey)
	return err
}

func (f *Facebook) failPublication(ctx context.Context, p PublishRequest, msg string) error {
	if p.PublicationID == "" {
		return nil
	}
	_, err := f.DB.ExecContext(ctx, `UPDATE publications SET status='failed', error=? WHERE id=?`, msg, p.PublicationID)
	return err
}

func (f *Facebook) reserveCap(ctx context.Context, account string) error {
	provider := "facebook:" + account
	ws := f.now().UTC().Truncate(24 * time.Hour).Format(time.RFC3339Nano)
	res, err := f.DB.ExecContext(ctx, `
INSERT INTO quotas (provider, window_start, used, "limit")
VALUES (?, ?, 1, ?)
ON CONFLICT(provider, window_start) DO UPDATE SET
  used = used + 1
WHERE quotas.used + 1 <= quotas."limit"`, provider, ws, fbDailyCap)
	if err != nil {
		return fmt.Errorf("publish/facebook: daily cap reserve: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w for account %s", ErrDailyCapExceeded, account)
	}
	return nil
}

// RegisterHandler wires publish.facebook onto the net resource class.
func (f *Facebook) RegisterHandler(q *queue.Queue) {
	if q == nil || f == nil {
		return
	}
	q.Register(JobPublishFacebook, queue.ResourceNet, 5, f.Handle)
}

type fbJobPayload struct {
	PublicationID     string `json:"publication_id"`
	ContentID         string `json:"content_id"`
	Account           string `json:"account"`
	IdempotencyKey    string `json:"idempotency_key"`
	VideoPath         string `json:"video_path"`
	Title             string `json:"title"`
	Description       string `json:"description"`
	ContainsSynthetic bool   `json:"contains_synthetic"`
	PaidPromotion     bool   `json:"paid_promotion"`
}

// Handle runs a publish.facebook job.
func (f *Facebook) Handle(ctx context.Context, job queue.Job) (json.RawMessage, error) {
	var p fbJobPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return nil, queue.Permanent(fmt.Errorf("publish/facebook: payload: %w", err))
	}
	req := PublishRequest{
		PublicationID: p.PublicationID, ContentID: p.ContentID, Account: p.Account,
		IdempotencyKey: p.IdempotencyKey, VideoPath: p.VideoPath, Title: p.Title,
		Description: p.Description, ContainsSynthetic: p.ContainsSynthetic, PaidPromotion: p.PaidPromotion,
	}
	if req.ContentID == "" && job.ContentID != nil {
		req.ContentID = *job.ContentID
	}
	res, err := f.Publish(ctx, req)
	if err != nil {
		if errors.Is(err, content.ErrNotApproved) || errors.Is(err, ErrDailyCapExceeded) || errors.Is(err, ErrReauthNeeded) {
			return nil, queue.Permanent(err)
		}
		return nil, err
	}
	raw, _ := json.Marshal(res)
	return raw, nil
}

func fbCaption(desc string, paid, synthetic bool) string {
	parts := []string{strings.TrimSpace(desc)}
	if paid {
		parts = append(parts, fbAffiliateDisclosure)
	}
	if synthetic {
		parts = append(parts, fbSyntheticDisclosure)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func fbRedact(s string) string {
	// Advance past each replacement so Graph messages like
	// "Invalid OAuth access token" cannot spin forever on the "oauth " needle.
	out := s
	lower := strings.ToLower(out)
	for _, needle := range []string{"access_token=", "bearer ", "oauth "} {
		startSearch := 0
		for {
			rel := strings.Index(lower[startSearch:], needle)
			if rel < 0 {
				break
			}
			i := startSearch + rel
			start := i + len(needle)
			end := start
			for end < len(out) && out[end] != ' ' && out[end] != '&' && out[end] != '"' {
				end++
			}
			out = out[:start] + "[redacted]" + out[end:]
			lower = strings.ToLower(out)
			startSearch = start + len("[redacted]")
		}
	}
	return out
}

// fbWrapGraph turns a Graph API error{code,message} into a typed Go error.
// Codes 190 (OAuthException — expired/invalid/revoked token), 102 (invalid
// session), 10/200/283 (permission missing/changed, which a fresh page token
// with the right scopes fixes the same way an expired token does) all mean
// "this token can no longer be used" — wrapped as ErrReauthNeeded so callers
// can errors.Is() it instead of parsing strings. VERIFY LIVE: exact code set
// for page-token invalidation should be checked against current Meta docs
// (Graph API error codes reference) before relying on this in production;
// 190 is the well-documented one, the others are a reasonable but unverified
// best guess (ticket Notes).
func fbWrapGraph(code int, message string) error {
	msg := strings.TrimSpace(message)
	if code == 190 || code == 102 || code == 10 || code == 200 || code == 283 {
		return fmt.Errorf("%w: graph code=%d message=%s", ErrReauthNeeded, code, msg)
	}
	return fmt.Errorf("publish/facebook: graph api error: graph code=%d message=%s", code, msg)
}

func fbDoJSON(client *http.Client, req *http.Request, dest any) error {
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	body = []byte(fbRedact(string(body)))
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
			return fbWrapGraph(ge.Error.Code, ge.Error.Message)
		}
		return fmt.Errorf("graph http %d: %s", res.StatusCode, fbRedact(string(body)))
	}
	return nil
}

// HTTPFacebookAPI talks to Graph + rupload for Page Reels.
type HTTPFacebookAPI struct {
	HTTP       *http.Client
	GraphBase  string
	UploadBase string
}

func (a *HTTPFacebookAPI) http() *http.Client {
	if a != nil && a.HTTP != nil {
		return a.HTTP
	}
	return http.DefaultClient
}

func (a *HTTPFacebookAPI) StartReel(ctx context.Context, tok *oauth2.Token, pageID string) (string, error) {
	form := url.Values{}
	form.Set("upload_phase", "start")
	form.Set("access_token", tok.AccessToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.GraphBase, "/")+"/"+pageID+"/video_reels", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var out struct {
		VideoID string `json:"video_id"`
		Error   *struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}
	if err := fbDoJSON(a.http(), req, &out); err != nil {
		return "", err
	}
	if out.Error != nil {
		return "", fbWrapGraph(out.Error.Code, out.Error.Message)
	}
	if out.VideoID == "" {
		return "", fmt.Errorf("empty video_id")
	}
	return out.VideoID, nil
}

func (a *HTTPFacebookAPI) UploadHosted(ctx context.Context, tok *oauth2.Token, videoID, fileURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.UploadBase, "/")+"/"+videoID, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "OAuth "+tok.AccessToken)
	req.Header.Set("file_url", fileURL)
	res, err := a.http().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 400 {
		var ge struct {
			Error *struct {
				Message string `json:"message"`
				Code    int    `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &ge)
		if ge.Error != nil {
			return fbWrapGraph(ge.Error.Code, ge.Error.Message)
		}
		return fmt.Errorf("rupload http %d: %s", res.StatusCode, fbRedact(string(body)))
	}
	return nil
}

func (a *HTTPFacebookAPI) FinishReel(ctx context.Context, tok *oauth2.Token, pageID, videoID, title, description string) (string, error) {
	form := url.Values{}
	form.Set("upload_phase", "finish")
	form.Set("video_state", "PUBLISHED")
	form.Set("video_id", videoID)
	form.Set("title", title)
	form.Set("description", description)
	form.Set("access_token", tok.AccessToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.GraphBase, "/")+"/"+pageID+"/video_reels", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var out struct {
		Success bool   `json:"success"`
		PostID  string `json:"post_id"`
		ID      string `json:"id"`
		Error   *struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}
	if err := fbDoJSON(a.http(), req, &out); err != nil {
		return "", err
	}
	if out.Error != nil {
		return "", fbWrapGraph(out.Error.Code, out.Error.Message)
	}
	if out.PostID != "" {
		return out.PostID, nil
	}
	if out.ID != "" {
		return out.ID, nil
	}
	return videoID, nil
}
