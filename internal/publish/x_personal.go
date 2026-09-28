// X personal-account publisher (M2-403): posts a text-only thread produced
// by internal/blog/repurpose_x.go via X API v2's tweet-creation endpoint.
//
// Deliberately simpler than M2-303's business-account video publisher
// (internal/publish/x.go): a blog thread is text only, so there is no
// INIT/APPEND/FINALIZE chunked media-upload flow here — just one
// POST /2/tweets call per tweet, reply-chained into a thread.
//
// Credential isolation (CLAUDE.md non-negotiable; CONTEXT D12; COMPLIANCE §1
// X row: "Business and personal X accounts never share posts"; F5):
//
// M2-303 (internal/publish/x.go) was not yet built when this file's design
// started (see ticket Notes for the exact verification: `git log m2/M2-303`
// showed no commits beyond the shared phase base at the time). It landed
// mid-session; internal/publish/x.go's real, load-bearing convention —
// documented in its own file header and locked in by its
// TestX_AccountRefNamingConvention test — turned out to be: internal/secrets
// keys tokens by (platform="x", account) for BOTH accounts (there is only
// one registered "x" platform entry in internal/secrets/platform.go, and
// `mayank2 auth` only knows that key), with the account_ref string as the
// sole separator, defended at runtime by x.go's isPersonalXAccount(account)
// guard, which refuses any account ref containing "personal".
//
// This file therefore resolves its token under SecretsPlatform ("x", same
// key x.go uses — there is no other registered platform to authorize
// against) and an account_ref that MUST contain "personal"
// (DefaultAccountRef = "x-personal", the exact example x.go's own comment
// names for this ticket) so x.go's guard — and any future guard like it —
// keeps rejecting cross-account use by construction. This file does not
// import, read, or reference internal/publish/x.go in any way; the
// isolation tests below encode the convention as data (verified against
// x.go's real, merged source), not a dependency on it.
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
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/oauth2"

	"mayank2/internal/content"
)

const (
	// JobPublishXPersonal is this ticket's own queue job type.
	//
	// SPEC §5's job list only has "publish.x" (business); it does not list a
	// personal-account variant, and there is no shared per-platform publish
	// dispatcher in this repo yet. A distinct, platform-qualified job type
	// avoids colliding with M2-303's "publish.x" job registration — flagged
	// for a follow-up SPEC update once the blog-repurpose publishers land.
	JobPublishXPersonal = "publish.x_personal"

	// SecretsPlatform is the internal/secrets platform key for the personal
	// X account's token — "x", the same key x.go uses. See the isolation
	// note in the file doc comment for why this is a shared platform key
	// with a mandatory-"personal" account_ref, not a distinct platform key.
	SecretsPlatform = "x"

	// DefaultAccountRef is used when a caller doesn't supply
	// PublishRequest.Account. It is the exact example internal/publish/x.go's
	// header names for this ticket, and satisfies its isPersonalXAccount
	// guard (contains "personal").
	DefaultAccountRef = "x-personal"

	// DefaultXPersonalDailyCap is the COMPLIANCE §4 cap: X personal ≤ 3
	// posts/day. One thread counts as one post against this cap (ticket
	// M2-403 Notes: the "1 thread = 1 post" unit is an assumption, not
	// stated explicitly anywhere in the docs — flagged for Mayank).
	DefaultXPersonalDailyCap = 3

	xTweetMaxChars  = 280
	xThreadMaxCount = 12

	// xTweetsURL matches internal/publish/x.go's researched current API
	// host (X's API moved off api.twitter.com to api.x.com).
	xTweetsURL = "https://api.x.com/2/tweets"
)

// TokenStore is the internal/secrets surface this publisher needs
// (satisfied by *internal/secrets.Store.Get). Kept as a narrow interface so
// tests can fake token storage without real DPAPI.
type TokenStore interface {
	Get(ctx context.Context, platform, account string) (*oauth2.Token, error)
}

// XPersonalAPI is the remote surface used by XPersonal (fakeable in tests).
type XPersonalAPI interface {
	// CreateTweet posts one tweet, optionally as a reply (thread chaining).
	// replyToID == "" means a standalone/root tweet.
	CreateTweet(ctx context.Context, tok *oauth2.Token, text, replyToID string) (tweetID string, err error)
}

// XPersonal is the X API v2 publisher for Mayank's personal account.
type XPersonal struct {
	DB     *sql.DB
	Tokens TokenStore
	API    XPersonalAPI
	Cap    int // daily post cap; defaults to DefaultXPersonalDailyCap
	Now    func() time.Time
	Log    *slog.Logger
}

func (x *XPersonal) Platform() string { return PlatformXPersonal }

// PlatformXPersonal mirrors internal/blog.PlatformXPersonal so this package
// does not need to import internal/blog just for the string constant.
const PlatformXPersonal = "x_personal"

func (x *XPersonal) now() time.Time {
	if x != nil && x.Now != nil {
		return x.Now().UTC()
	}
	return time.Now().UTC()
}

func (x *XPersonal) log() *slog.Logger {
	if x != nil && x.Log != nil {
		return x.Log
	}
	return slog.Default()
}

func (x *XPersonal) dailyCap() int {
	if x != nil && x.Cap > 0 {
		return x.Cap
	}
	return DefaultXPersonalDailyCap
}

// Publish posts the thread in p.Description (a JSON array of tweet texts,
// thread order) via POST /2/tweets per tweet, reply-chained. Idempotent on
// p.IdempotencyKey: a completed thread's replay is a no-op that returns the
// stored result; a crash mid-thread resumes from the last successfully
// posted tweet (recorded progressively in publications.external_id as a
// JSON array while status='publishing') rather than reposting from tweet 1.
func (x *XPersonal) Publish(ctx context.Context, p PublishRequest) (PublishResult, error) {
	var zero PublishResult
	if x == nil || x.DB == nil {
		return zero, fmt.Errorf("publish/x_personal: nil service/db")
	}
	if p.ContentID == "" {
		return zero, fmt.Errorf("publish/x_personal: content_id required")
	}
	account := strings.TrimSpace(p.Account)
	if account == "" {
		account = DefaultAccountRef
	}
	// Defense in depth, symmetric to internal/publish/x.go's
	// isPersonalXAccount guard: this publisher refuses to run against an
	// account_ref that does not look personal, exactly as x.go refuses to
	// run against one that does. Independently reimplemented (not imported)
	// so the two files stay decoupled; both encode the same documented
	// convention (see file doc comment).
	if !looksPersonalXAccount(account) {
		return zero, fmt.Errorf("publish/x_personal: refusing non-personal account ref %q (personal only)", account)
	}
	if p.IdempotencyKey == "" {
		return zero, fmt.Errorf("publish/x_personal: idempotency_key required")
	}
	if err := content.RequireApproved(ctx, x.DB, p.ContentID); err != nil {
		return zero, err
	}

	tweets, err := decodeThread(p.Description)
	if err != nil {
		return zero, fmt.Errorf("publish/x_personal: %w", err)
	}

	if res, done, err := x.lookupPublished(ctx, p); err != nil {
		return zero, err
	} else if done {
		return res, nil
	}

	posted, err := x.loadProgress(ctx, p)
	if err != nil {
		return zero, err
	}
	if len(posted) > len(tweets) {
		return zero, fmt.Errorf("publish/x_personal: stored progress (%d posted) exceeds thread length (%d)", len(posted), len(tweets))
	}

	if err := x.markPublishing(ctx, p); err != nil {
		return zero, err
	}

	if len(posted) == 0 {
		if err := x.reserveDailyCap(ctx, account); err != nil {
			_ = x.failPublication(ctx, p, err.Error())
			return zero, err
		}
	}

	tok, err := x.token(ctx, account)
	if err != nil {
		_ = x.failPublication(ctx, p, "oauth token unavailable")
		return zero, err
	}

	for i := len(posted); i < len(tweets); i++ {
		replyTo := ""
		if len(posted) > 0 {
			replyTo = posted[len(posted)-1]
		}
		id, err := x.API.CreateTweet(ctx, tok, tweets[i], replyTo)
		if err != nil {
			_ = x.failPublication(ctx, p, "post failed")
			x.log().Error("publish/x_personal: post", "content_id", p.ContentID, "tweet_index", i, "error", err)
			return zero, fmt.Errorf("publish/x_personal: post tweet %d: %w", i, err)
		}
		posted = append(posted, id)
		if err := x.saveProgress(ctx, p, posted); err != nil {
			return zero, err
		}
	}

	root := posted[0]
	url := "https://x.com/i/status/" + root
	if err := x.completePublication(ctx, p, root, url); err != nil {
		return zero, err
	}
	x.log().Info("publish/x_personal: published", "content_id", p.ContentID, "root_tweet_id", root, "tweets", len(posted))
	return PublishResult{ExternalID: root, URL: url}, nil
}

func (x *XPersonal) token(ctx context.Context, account string) (*oauth2.Token, error) {
	if x.Tokens == nil {
		return nil, fmt.Errorf("publish/x_personal: token store not configured")
	}
	tok, err := x.Tokens.Get(ctx, SecretsPlatform, account)
	if err != nil {
		return nil, fmt.Errorf("publish/x_personal: token for %s/%s: %w", SecretsPlatform, account, err)
	}
	if tok == nil || tok.AccessToken == "" {
		return nil, fmt.Errorf("publish/x_personal: empty access token for %s/%s", SecretsPlatform, account)
	}
	return tok, nil
}

// looksPersonalXAccount independently reimplements internal/publish/x.go's
// isPersonalXAccount predicate (not imported — see file doc comment) so
// this file can refuse to run against a non-personal-looking account_ref,
// symmetric to x.go refusing a personal-looking one.
func looksPersonalXAccount(account string) bool {
	a := strings.ToLower(strings.TrimSpace(account))
	return a == "personal" || a == "x-personal" || a == "x_personal" ||
		strings.Contains(a, "personal") || strings.HasSuffix(a, "-personal")
}

// decodeThread parses PublishRequest.Description as a JSON array of tweet
// texts. This encoding (rather than a typed field) is a deliberate choice:
// the shared PublishRequest struct is owned by M2-211 (internal/publish/youtube.go),
// outside this ticket's touches, and has no thread-specific field. Flagged
// in ticket Notes for a follow-up once more text-thread publishers exist.
func decodeThread(desc string) ([]string, error) {
	desc = strings.TrimSpace(desc)
	if desc == "" {
		return nil, fmt.Errorf("empty thread (PublishRequest.Description)")
	}
	var tweets []string
	if err := json.Unmarshal([]byte(desc), &tweets); err != nil {
		return nil, fmt.Errorf("thread is not a JSON array of strings: %w", err)
	}
	if len(tweets) == 0 {
		return nil, fmt.Errorf("empty thread")
	}
	if len(tweets) > xThreadMaxCount {
		return nil, fmt.Errorf("thread has %d tweets (>%d)", len(tweets), xThreadMaxCount)
	}
	for i, tw := range tweets {
		tw = strings.TrimSpace(tw)
		if tw == "" {
			return nil, fmt.Errorf("tweet %d is empty", i)
		}
		if utf8.RuneCountInString(tw) > xTweetMaxChars {
			return nil, fmt.Errorf("tweet %d is %d chars (>%d); repurpose step must truncate/reject before publish", i, utf8.RuneCountInString(tw), xTweetMaxChars)
		}
		tweets[i] = tw
	}
	return tweets, nil
}

func (x *XPersonal) lookupPublished(ctx context.Context, p PublishRequest) (PublishResult, bool, error) {
	ext, url, status, err := x.loadRow(ctx, p)
	if err != nil {
		return PublishResult{}, false, err
	}
	if status == "published" && ext != "" {
		return PublishResult{ExternalID: ext, URL: url}, true, nil
	}
	return PublishResult{}, false, nil
}

// loadProgress returns the partially-posted tweet IDs (thread order) from a
// prior attempt, or nil if there is none.
func (x *XPersonal) loadProgress(ctx context.Context, p PublishRequest) ([]string, error) {
	ext, _, status, err := x.loadRow(ctx, p)
	if err != nil {
		return nil, err
	}
	if status != "publishing" || ext == "" {
		return nil, nil
	}
	var posted []string
	if err := json.Unmarshal([]byte(ext), &posted); err != nil {
		// Not a progress array (e.g. stale/foreign data) — treat as no progress.
		return nil, nil
	}
	return posted, nil
}

func (x *XPersonal) loadRow(ctx context.Context, p PublishRequest) (ext, url, status string, err error) {
	row := x.DB.QueryRowContext(ctx, `
SELECT COALESCE(external_id,''), COALESCE(url,''), status
FROM publications WHERE idempotency_key=?`, p.IdempotencyKey)
	err = row.Scan(&ext, &url, &status)
	if errors.Is(err, sql.ErrNoRows) {
		if p.PublicationID == "" {
			return "", "", "", nil
		}
		err = x.DB.QueryRowContext(ctx, `
SELECT COALESCE(external_id,''), COALESCE(url,''), status
FROM publications WHERE id=?`, p.PublicationID).Scan(&ext, &url, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", "", nil
		}
	}
	if err != nil {
		return "", "", "", fmt.Errorf("publish/x_personal: lookup: %w", err)
	}
	return ext, url, status, nil
}

func (x *XPersonal) markPublishing(ctx context.Context, p PublishRequest) error {
	if p.PublicationID == "" {
		return nil
	}
	_, err := x.DB.ExecContext(ctx, `
UPDATE publications SET status='publishing', error=NULL
WHERE id=? AND status IN ('scheduled','publishing','failed')`, p.PublicationID)
	return err
}

// saveProgress persists posted tweet IDs so a crash mid-thread can resume
// instead of reposting from the start. Status stays 'publishing'.
func (x *XPersonal) saveProgress(ctx context.Context, p PublishRequest, posted []string) error {
	raw, err := json.Marshal(posted)
	if err != nil {
		return fmt.Errorf("publish/x_personal: marshal progress: %w", err)
	}
	if p.PublicationID != "" {
		_, err = x.DB.ExecContext(ctx, `
UPDATE publications SET external_id=?, status='publishing' WHERE id=?`, string(raw), p.PublicationID)
	} else {
		_, err = x.DB.ExecContext(ctx, `
UPDATE publications SET external_id=?, status='publishing' WHERE idempotency_key=?`, string(raw), p.IdempotencyKey)
	}
	if err != nil {
		return fmt.Errorf("publish/x_personal: save progress: %w", err)
	}
	return nil
}

func (x *XPersonal) completePublication(ctx context.Context, p PublishRequest, rootID, url string) error {
	at := x.now().Format(time.RFC3339Nano)
	var err error
	if p.PublicationID != "" {
		_, err = x.DB.ExecContext(ctx, `
UPDATE publications
SET status='published', external_id=?, url=?, published_at=?, error=NULL
WHERE id=?`, rootID, url, at, p.PublicationID)
	} else {
		_, err = x.DB.ExecContext(ctx, `
UPDATE publications
SET status='published', external_id=?, url=?, published_at=?, error=NULL
WHERE idempotency_key=?`, rootID, url, at, p.IdempotencyKey)
	}
	if err != nil {
		return fmt.Errorf("publish/x_personal: complete: %w", err)
	}
	return nil
}

// failPublication records the error without clobbering any posted-tweet
// progress in external_id, so a retry can still resume.
func (x *XPersonal) failPublication(ctx context.Context, p PublishRequest, msg string) error {
	if p.PublicationID == "" {
		return nil
	}
	_, err := x.DB.ExecContext(ctx, `
UPDATE publications SET status='failed', error=? WHERE id=?`, msg, p.PublicationID)
	return err
}

func (x *XPersonal) windowStart() string {
	return x.now().UTC().Truncate(24 * time.Hour).Format(time.RFC3339Nano)
}

// quotaProvider mirrors internal/publish/x.go's reserveCap provider key
// convention ("x:" + account) so both publishers' caps live in the same
// "x:" quotas namespace, keyed apart by account — consistent naming, no
// collision risk either way since the account strings never overlap
// (COMPLIANCE §1 X row).
func quotaProvider(account string) string {
	return "x:" + account
}

// reserveDailyCap atomically spends one unit of the X-personal daily cap
// (COMPLIANCE §4: ≤3 posts/day) for the UTC day window. Fails closed.
func (x *XPersonal) reserveDailyCap(ctx context.Context, account string) error {
	provider := quotaProvider(account)
	ws := x.windowStart()
	lim := x.dailyCap()
	res, err := x.DB.ExecContext(ctx, `
INSERT INTO quotas (provider, window_start, used, "limit")
VALUES (?, ?, 1, ?)
ON CONFLICT(provider, window_start) DO UPDATE SET
  used = used + 1
WHERE quotas.used + 1 <= quotas."limit"`,
		provider, ws, lim,
	)
	if err != nil {
		return fmt.Errorf("publish/x_personal: cap reserve: %w", err)
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		return nil
	}
	// Conflict path: row exists and the WHERE predicate failed, or the row
	// didn't exist yet and INSERT...ON CONFLICT DO UPDATE affected 0 rows in
	// some SQLite versions when the predicate fails on first insert attempt.
	var used, limit int
	err = x.DB.QueryRowContext(ctx, `
SELECT used, "limit" FROM quotas WHERE provider=? AND window_start=?`,
		provider, ws).Scan(&used, &limit)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = x.DB.ExecContext(ctx, `
INSERT INTO quotas (provider, window_start, used, "limit") VALUES (?, ?, 0, ?)`,
			provider, ws, lim)
		if err != nil {
			return fmt.Errorf("publish/x_personal: cap init: %w", err)
		}
		used, limit = 0, lim
	} else if err != nil {
		return fmt.Errorf("publish/x_personal: cap lookup: %w", err)
	}
	if used+1 > limit {
		return fmt.Errorf("publish/x_personal: daily cap exceeded (%d+1 > %d)", used, limit)
	}
	res, err = x.DB.ExecContext(ctx, `
UPDATE quotas SET used = used + 1
WHERE provider=? AND window_start=? AND used + 1 <= "limit"`,
		provider, ws)
	if err != nil {
		return fmt.Errorf("publish/x_personal: cap update: %w", err)
	}
	n, _ = res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("publish/x_personal: daily cap exceeded")
	}
	return nil
}

// --- HTTP X API v2 client ----------------------------------------------

// HTTPXPersonalAPI talks to the official X API v2 tweet-creation endpoint.
type HTTPXPersonalAPI struct {
	HTTP *http.Client
	// BaseURL overrides xTweetsURL; tests point this at httptest.Server.URL.
	BaseURL string
}

func (a *HTTPXPersonalAPI) http() *http.Client {
	if a != nil && a.HTTP != nil {
		return a.HTTP
	}
	return http.DefaultClient
}

func (a *HTTPXPersonalAPI) url() string {
	if a != nil && a.BaseURL != "" {
		return a.BaseURL
	}
	return xTweetsURL
}

func (a *HTTPXPersonalAPI) CreateTweet(ctx context.Context, tok *oauth2.Token, text, replyToID string) (string, error) {
	body := map[string]any{"text": text}
	if replyToID != "" {
		body["reply"] = map[string]any{"in_reply_to_tweet_id": replyToID}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.url(), strings.NewReader(string(raw)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.http().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw2, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("tweets HTTP %d: %s", resp.StatusCode, redact(string(raw2)))
	}
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw2, &out); err != nil || out.Data.ID == "" {
		return "", fmt.Errorf("tweets: parse response id: %w", err)
	}
	return out.Data.ID, nil
}
