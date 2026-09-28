// LinkedIn publisher (M2-402): posts the approved, LinkedIn-native draft
// produced by internal/blog/repurpose_linkedin.go to Mayank's personal
// LinkedIn member profile via LinkedIn's official Posts API. Official APIs
// only; RequireApproved before any call; idempotent on
// publications.idempotency_key; fails closed on the COMPLIANCE §4 daily cap
// (LinkedIn ≤ 1 post/day) rather than queuing past it.
//
// API-SHAPE UNCERTAINTY (ticket M2-402 Notes — verify against live LinkedIn
// docs before this ever talks to a real app):
//   - Endpoint: this implements the newer "Posts API" (POST /rest/posts),
//     LinkedIn's documented replacement for the legacy /v2/ugcPosts endpoint.
//     If the app is only approved for the legacy endpoint, HTTPLinkedInAPI
//     is the one place to change (postsURL + buildPostBody + response
//     parsing) — LinkedInAPI stays the same.
//   - Headers: "LinkedIn-Version: <YYYYMM>" and "X-Restli-Protocol-Version:
//     2.0.0" are required by the Posts API as of last known docs; the
//     version string here (linkedInAPIVersion) is a placeholder and will go
//     stale — confirm the current value.
//   - Scopes: secrets/platform.go currently requests "openid profile
//     w_member_social". w_member_social (member-initiated posts) needs
//     Marketing Developer Platform / "Share on LinkedIn" product approval;
//     confirm current app-review tier requirements before relying on this.
//   - Author URN: resolved via the standard OIDC "GET /v2/userinfo" call
//     (WhoAmI) using the "openid profile" scopes — this part follows the
//     OIDC spec rather than a LinkedIn-specific guess, so it's the most
//     stable piece here. Response "sub" becomes "urn:li:person:{sub}".
//   - Response id / permalink: the Posts API returns the created post's URN
//     via the "x-restli-id" response header (not always in the JSON body);
//     CreatePost checks the header first, then falls back to a body "id"
//     field. There is no documented public-permalink field on create —
//     PublishResult.URL is built from the well-known
//     "https://www.linkedin.com/feed/update/{urn}/" pattern, which is
//     LinkedIn's common share-permalink shape but is not confirmed for
//     every post/visibility combination.
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
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"mayank2/internal/content"
	"mayank2/internal/queue"
)

const (
	// JobPublishLinkedIn is the queue job type (SPEC §5: publish.linkedin).
	JobPublishLinkedIn = "publish.linkedin"

	linkedInDailyCap = 1 // COMPLIANCE §4: LinkedIn <= 1 post/day

	linkedInAPIBase = "https://api.linkedin.com"
	// *(verify)* placeholder version header — see file-level doc comment.
	linkedInAPIVersion = "202401"
)

// LinkedInAPI is the remote surface LinkedIn needs (fakeable in tests).
type LinkedInAPI interface {
	// WhoAmI resolves the authenticated member's URN via OIDC userinfo.
	WhoAmI(ctx context.Context, tok *oauth2.Token) (authorURN string, err error)
	// CreatePost creates one text share and returns its URN.
	CreatePost(ctx context.Context, tok *oauth2.Token, authorURN, text string) (postURN string, err error)
}

// LinkedIn is the LinkedIn Posts API publisher for Mayank's personal
// account (CONTEXT D12: a single personal LinkedIn account is in scope).
type LinkedIn struct {
	DB     *sql.DB
	Tokens TokenSourceFor // secrets vault by account ref — never .env, never logged
	API    LinkedInAPI
	Now    func() time.Time
	Log    *slog.Logger
}

func (l *LinkedIn) Platform() string { return "linkedin" }

func (l *LinkedIn) now() time.Time {
	if l != nil && l.Now != nil {
		return l.Now().UTC()
	}
	return time.Now().UTC()
}

func (l *LinkedIn) log() *slog.Logger {
	if l != nil && l.Log != nil {
		return l.Log
	}
	return slog.Default()
}

// Publish posts one LinkedIn share. Idempotent on IdempotencyKey.
func (l *LinkedIn) Publish(ctx context.Context, p PublishRequest) (PublishResult, error) {
	var zero PublishResult
	if l == nil || l.DB == nil {
		return zero, fmt.Errorf("publish/linkedin: nil service/db")
	}
	if p.ContentID == "" || p.Account == "" {
		return zero, fmt.Errorf("publish/linkedin: content_id and account required")
	}
	if p.IdempotencyKey == "" {
		return zero, fmt.Errorf("publish/linkedin: idempotency_key required")
	}
	text := strings.TrimSpace(p.Description)
	if text == "" {
		return zero, fmt.Errorf("publish/linkedin: empty post text")
	}
	if err := content.RequireApproved(ctx, l.DB, p.ContentID); err != nil {
		return zero, err
	}

	// Idempotency: already published → return stored identity.
	if res, ok, err := l.lookupPublished(ctx, p); err != nil {
		return zero, err
	} else if ok {
		return res, nil
	}

	if err := l.reserveCap(ctx, p.Account); err != nil {
		_ = l.failPublication(ctx, p, err.Error())
		return zero, err
	}
	if err := l.markPublishing(ctx, p); err != nil {
		return zero, err
	}

	tok, err := l.token(ctx, p.Account)
	if err != nil {
		_ = l.failPublication(ctx, p, "oauth token unavailable")
		return zero, err
	}

	api := l.api()
	authorURN, err := api.WhoAmI(ctx, tok)
	if err != nil {
		msg := liRedact(err.Error())
		_ = l.failPublication(ctx, p, msg)
		l.log().Error("publish/linkedin: whoami", "account", p.Account, "content_id", p.ContentID, "error", msg)
		return zero, fmt.Errorf("publish/linkedin: whoami: %w", err)
	}
	if strings.TrimSpace(authorURN) == "" {
		_ = l.failPublication(ctx, p, "empty author urn")
		return zero, fmt.Errorf("publish/linkedin: empty author urn")
	}

	postURN, err := api.CreatePost(ctx, tok, authorURN, text)
	if err != nil {
		msg := liRedact(err.Error())
		_ = l.failPublication(ctx, p, msg)
		l.log().Error("publish/linkedin: create post", "account", p.Account, "content_id", p.ContentID, "error", msg)
		return zero, fmt.Errorf("publish/linkedin: create post: %w", err)
	}
	if strings.TrimSpace(postURN) == "" {
		_ = l.failPublication(ctx, p, "empty post urn")
		return zero, fmt.Errorf("publish/linkedin: empty post urn")
	}

	permalink := "https://www.linkedin.com/feed/update/" + url.PathEscape(postURN) + "/"
	if err := l.completePublication(ctx, p, postURN, permalink); err != nil {
		return zero, err
	}
	l.log().Info("publish/linkedin: published", "account", p.Account, "content_id", p.ContentID, "post_urn", postURN)
	return PublishResult{ExternalID: postURN, URL: permalink}, nil
}

func (l *LinkedIn) token(ctx context.Context, account string) (*oauth2.Token, error) {
	if l.Tokens == nil {
		return nil, fmt.Errorf("publish/linkedin: token source not configured")
	}
	ts, err := l.Tokens(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("publish/linkedin: token for %s: %w", account, err)
	}
	tok, err := ts.Token()
	if err != nil {
		return nil, fmt.Errorf("publish/linkedin: refresh token for %s: %w", account, err)
	}
	if tok == nil || tok.AccessToken == "" {
		return nil, fmt.Errorf("publish/linkedin: empty access token for %s", account)
	}
	return tok, nil
}

func (l *LinkedIn) api() LinkedInAPI {
	if l.API != nil {
		return l.API
	}
	return &HTTPLinkedInAPI{HTTP: http.DefaultClient, Base: linkedInAPIBase}
}

func (l *LinkedIn) lookupPublished(ctx context.Context, p PublishRequest) (PublishResult, bool, error) {
	var ext, u, status string
	err := l.DB.QueryRowContext(ctx, `
SELECT COALESCE(external_id,''), COALESCE(url,''), status
FROM publications WHERE idempotency_key=?`, p.IdempotencyKey).Scan(&ext, &u, &status)
	if errors.Is(err, sql.ErrNoRows) {
		if p.PublicationID == "" {
			return PublishResult{}, false, nil
		}
		err = l.DB.QueryRowContext(ctx, `
SELECT COALESCE(external_id,''), COALESCE(url,''), status
FROM publications WHERE id=?`, p.PublicationID).Scan(&ext, &u, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return PublishResult{}, false, nil
		}
	}
	if err != nil {
		return PublishResult{}, false, fmt.Errorf("publish/linkedin: lookup: %w", err)
	}
	if status == "published" && ext != "" {
		return PublishResult{ExternalID: ext, URL: u}, true, nil
	}
	return PublishResult{}, false, nil
}

func (l *LinkedIn) markPublishing(ctx context.Context, p PublishRequest) error {
	if p.PublicationID == "" {
		return nil
	}
	_, err := l.DB.ExecContext(ctx, `
UPDATE publications SET status='publishing', error=NULL
WHERE id=? AND status IN ('scheduled','publishing','failed')`, p.PublicationID)
	return err
}

func (l *LinkedIn) completePublication(ctx context.Context, p PublishRequest, postURN, permalink string) error {
	at := l.now().Format(time.RFC3339Nano)
	if p.PublicationID != "" {
		_, err := l.DB.ExecContext(ctx, `
UPDATE publications
SET status='published', external_id=?, url=?, published_at=?, error=NULL
WHERE id=?`, postURN, permalink, at, p.PublicationID)
		return err
	}
	_, err := l.DB.ExecContext(ctx, `
UPDATE publications
SET status='published', external_id=?, url=?, published_at=?, error=NULL
WHERE idempotency_key=?`, postURN, permalink, at, p.IdempotencyKey)
	return err
}

func (l *LinkedIn) failPublication(ctx context.Context, p PublishRequest, msg string) error {
	if p.PublicationID == "" {
		return nil
	}
	_, err := l.DB.ExecContext(ctx, `
UPDATE publications SET status='failed', error=? WHERE id=?`, msg, p.PublicationID)
	return err
}

// reserveCap fails closed at the COMPLIANCE §4 daily cap (<=1/day) instead
// of queuing the post for a later slot — same ON CONFLICT ... WHERE guard
// pattern as YouTube's quota reserve / Instagram's reserveCap.
func (l *LinkedIn) reserveCap(ctx context.Context, account string) error {
	provider := "linkedin:" + account
	ws := l.now().UTC().Truncate(24 * time.Hour).Format(time.RFC3339Nano)
	res, err := l.DB.ExecContext(ctx, `
INSERT INTO quotas (provider, window_start, used, "limit")
VALUES (?, ?, 1, ?)
ON CONFLICT(provider, window_start) DO UPDATE SET
  used = used + 1
WHERE quotas.used + 1 <= quotas."limit"`, provider, ws, linkedInDailyCap)
	if err != nil {
		return fmt.Errorf("publish/linkedin: daily cap reserve: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("publish/linkedin: daily cap exceeded")
	}
	return nil
}

// RegisterHandler wires publish.linkedin onto the net resource class.
func (l *LinkedIn) RegisterHandler(q *queue.Queue) {
	if q == nil || l == nil {
		return
	}
	q.Register(JobPublishLinkedIn, queue.ResourceNet, 5, l.Handle)
}

type linkedInJobPayload struct {
	PublicationID  string `json:"publication_id"`
	ContentID      string `json:"content_id"`
	Account        string `json:"account"`
	IdempotencyKey string `json:"idempotency_key"`
	Text           string `json:"text"`
}

// Handle runs a publish.linkedin job.
func (l *LinkedIn) Handle(ctx context.Context, job queue.Job) (json.RawMessage, error) {
	var p linkedInJobPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return nil, queue.Permanent(fmt.Errorf("publish/linkedin: payload: %w", err))
	}
	req := PublishRequest{
		PublicationID:  p.PublicationID,
		ContentID:      p.ContentID,
		Account:        p.Account,
		IdempotencyKey: p.IdempotencyKey,
		Description:    p.Text,
	}
	if req.ContentID == "" && job.ContentID != nil {
		req.ContentID = *job.ContentID
	}
	res, err := l.Publish(ctx, req)
	if err != nil {
		if errors.Is(err, content.ErrNotApproved) {
			return nil, queue.Permanent(err)
		}
		if strings.Contains(err.Error(), "daily cap exceeded") {
			return nil, queue.Permanent(err)
		}
		return nil, err
	}
	raw, _ := json.Marshal(res)
	return raw, nil
}

// liRedact strips bearer/access-token-shaped substrings from API error
// bodies before they ever reach failPublication/logs (never log tokens).
func liRedact(s string) string {
	const mark = "[redacted]"
	for _, needle := range []string{"access_token=", "bearer ", "oauth "} {
		lower := strings.ToLower(s)
		search := 0
		for search < len(lower) {
			idx := strings.Index(lower[search:], needle)
			if idx < 0 {
				break
			}
			i := search + idx
			start := i + len(needle)
			end := start
			for end < len(s) && s[end] != ' ' && s[end] != '&' && s[end] != '"' {
				end++
			}
			s = s[:start] + mark + s[end:]
			lower = strings.ToLower(s)
			// Advance strictly past this replacement so the same already-redacted
			// span can never be found again — without this the needle (never
			// itself removed) keeps matching at the same position forever.
			search = start + len(mark)
		}
	}
	return s
}

// --- HTTP LinkedIn Posts API client ------------------------------------

// HTTPLinkedInAPI talks to the official LinkedIn API over HTTPS.
type HTTPLinkedInAPI struct {
	HTTP *http.Client
	Base string
}

func (a *HTTPLinkedInAPI) http() *http.Client {
	if a != nil && a.HTTP != nil {
		return a.HTTP
	}
	return http.DefaultClient
}

func (a *HTTPLinkedInAPI) base() string {
	if a != nil && strings.TrimSpace(a.Base) != "" {
		return strings.TrimRight(a.Base, "/")
	}
	return linkedInAPIBase
}

// WhoAmI resolves the authenticated member's URN via the standard OIDC
// userinfo endpoint (requires the "openid profile" scopes).
func (a *HTTPLinkedInAPI) WhoAmI(ctx context.Context, tok *oauth2.Token) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.base()+"/v2/userinfo", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	resp, err := a.http().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("userinfo HTTP %d: %s", resp.StatusCode, liRedact(string(body)))
	}
	var out struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("userinfo: parse: %w", err)
	}
	if out.Sub == "" {
		return "", fmt.Errorf("userinfo: empty sub")
	}
	return "urn:li:person:" + out.Sub, nil
}

// CreatePost creates one text share via the Posts API.
func (a *HTTPLinkedInAPI) CreatePost(ctx context.Context, tok *oauth2.Token, authorURN, text string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"author":     authorURN,
		"commentary": text,
		"visibility": "PUBLIC",
		"distribution": map[string]any{
			"feedDistribution": "MAIN_FEED",
		},
		"lifecycleState":            "PUBLISHED",
		"isReshareDisabledByAuthor": false,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base()+"/rest/posts", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Restli-Protocol-Version", "2.0.0")
	req.Header.Set("LinkedIn-Version", linkedInAPIVersion)
	resp, err := a.http().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("posts HTTP %d: %s", resp.StatusCode, liRedact(string(raw)))
	}
	if id := resp.Header.Get("x-restli-id"); strings.TrimSpace(id) != "" {
		return id, nil
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.ID == "" {
		return "", fmt.Errorf("posts: no id in header or body")
	}
	return out.ID, nil
}
