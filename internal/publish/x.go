// X (business) publisher (M2-303): chunked media upload + POST /2/tweets.
// Reads render from local disk (no R2). Business account only — never personal (D12).
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
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/oauth2"

	"mayank2/internal/content"
	"mayank2/internal/queue"
)

const (
	JobPublishX = "publish.x"

	xDailyCap      = 5 // COMPLIANCE §4: X business ≤ 5/day
	xMaxTweetRunes = 280
	xChunkSize     = 4 << 20 // 4 MiB

	xDefaultPollInterval = 2 * time.Second
	xDefaultPollTimeout  = 5 * time.Minute
)

// XAPI is the X media + tweets surface (fakeable in tests).
type XAPI interface {
	InitUpload(ctx context.Context, tok *oauth2.Token, mediaType string, totalBytes int64) (mediaID string, err error)
	AppendUpload(ctx context.Context, tok *oauth2.Token, mediaID string, segmentIndex int, chunk []byte) error
	FinalizeUpload(ctx context.Context, tok *oauth2.Token, mediaID string) (processing bool, checkAfterSecs int, err error)
	UploadStatus(ctx context.Context, tok *oauth2.Token, mediaID string) (state string, checkAfterSecs int, err error)
	CreateTweet(ctx context.Context, tok *oauth2.Token, text, mediaID string) (tweetID string, err error)
}

// X publishes short-form video to the X business account only.
type X struct {
	DB           *sql.DB
	Tokens       TokenSourceFor // business account ref only
	API          XAPI
	Now          func() time.Time
	Log          *slog.Logger
	DataRoot     string
	PollInterval time.Duration
	PollTimeout  time.Duration
}

func (x *X) Platform() string { return "x" }

func (x *X) now() time.Time {
	if x != nil && x.Now != nil {
		return x.Now().UTC()
	}
	return time.Now().UTC()
}

func (x *X) log() *slog.Logger {
	if x != nil && x.Log != nil {
		return x.Log
	}
	return slog.Default()
}

func (x *X) pollInterval() time.Duration {
	if x != nil && x.PollInterval > 0 {
		return x.PollInterval
	}
	return xDefaultPollInterval
}

func (x *X) pollTimeout() time.Duration {
	if x != nil && x.PollTimeout > 0 {
		return x.PollTimeout
	}
	return xDefaultPollTimeout
}

// Publish uploads video from local disk and posts a tweet. Idempotent on IdempotencyKey.
func (x *X) Publish(ctx context.Context, p PublishRequest) (PublishResult, error) {
	var zero PublishResult
	if x == nil || x.DB == nil {
		return zero, fmt.Errorf("publish/x: nil service/db")
	}
	if p.ContentID == "" || p.Account == "" {
		return zero, fmt.Errorf("publish/x: content_id and account required")
	}
	if p.IdempotencyKey == "" {
		return zero, fmt.Errorf("publish/x: idempotency_key required")
	}
	// Hard rule (D12 / COMPLIANCE): never use personal X credentials.
	if isPersonalXAccount(p.Account) {
		return zero, fmt.Errorf("publish/x: refusing personal account ref %q (business only)", p.Account)
	}
	if err := content.RequireApproved(ctx, x.DB, p.ContentID); err != nil {
		return zero, err
	}
	if res, ok, err := x.lookupPublished(ctx, p); err != nil {
		return zero, err
	} else if ok {
		return res, nil
	}

	text, err := xTweetText(p.Description)
	if err != nil {
		return zero, err
	}

	if err := x.reserveCap(ctx, p.Account); err != nil {
		_ = x.failPublication(ctx, p, err.Error())
		return zero, err
	}
	if err := x.markPublishing(ctx, p); err != nil {
		return zero, err
	}

	tok, err := x.token(ctx, p.Account)
	if err != nil {
		_ = x.failPublication(ctx, p, "oauth token unavailable")
		return zero, err
	}

	videoPath, err := x.confine(p.VideoPath)
	if err != nil {
		_ = x.failPublication(ctx, p, err.Error())
		return zero, err
	}
	data, err := os.ReadFile(videoPath)
	if err != nil {
		_ = x.failPublication(ctx, p, err.Error())
		return zero, fmt.Errorf("publish/x: read video: %w", err)
	}

	api := x.api()
	mediaID, err := api.InitUpload(ctx, tok, "video/mp4", int64(len(data)))
	if err != nil {
		msg := xRedact(err.Error())
		_ = x.failPublication(ctx, p, msg)
		return zero, fmt.Errorf("publish/x: init: %w", err)
	}
	for i, off := 0, 0; off < len(data); i++ {
		end := off + xChunkSize
		if end > len(data) {
			end = len(data)
		}
		if err := api.AppendUpload(ctx, tok, mediaID, i, data[off:end]); err != nil {
			msg := xRedact(err.Error())
			_ = x.failPublication(ctx, p, msg)
			return zero, fmt.Errorf("publish/x: append: %w", err)
		}
		off = end
	}
	processing, checkAfter, err := api.FinalizeUpload(ctx, tok, mediaID)
	if err != nil {
		msg := xRedact(err.Error())
		_ = x.failPublication(ctx, p, msg)
		return zero, fmt.Errorf("publish/x: finalize: %w", err)
	}
	if processing {
		if err := x.waitMediaReady(ctx, tok, mediaID, checkAfter); err != nil {
			msg := xRedact(err.Error())
			_ = x.failPublication(ctx, p, msg)
			return zero, fmt.Errorf("publish/x: media status: %w", err)
		}
	}

	tweetID, err := api.CreateTweet(ctx, tok, text, mediaID)
	if err != nil {
		msg := xRedact(err.Error())
		_ = x.failPublication(ctx, p, msg)
		return zero, fmt.Errorf("publish/x: tweet: %w", err)
	}
	tweetURL := "https://x.com/i/status/" + tweetID
	if err := x.completePublication(ctx, p, tweetID, tweetURL); err != nil {
		return zero, err
	}
	x.log().Info("publish/x: published", "account", p.Account, "content_id", p.ContentID, "tweet_id", tweetID)
	return PublishResult{ExternalID: tweetID, URL: tweetURL}, nil
}

func (x *X) waitMediaReady(ctx context.Context, tok *oauth2.Token, mediaID string, checkAfter int) error {
	// Wall clock for the poll deadline — x.Now is for DB timestamps/quota windows and
	// may be frozen in tests; hanging here would pin a worker forever.
	deadline := time.Now().UTC().Add(x.pollTimeout())
	api := x.api()
	wait := time.Duration(checkAfter) * time.Second
	if wait <= 0 {
		wait = x.pollInterval()
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
		state, next, err := api.UploadStatus(ctx, tok, mediaID)
		if err != nil {
			return err
		}
		switch strings.ToLower(state) {
		case "succeeded":
			return nil
		case "failed":
			return fmt.Errorf("media processing failed")
		case "pending", "in_progress":
			if !time.Now().UTC().Before(deadline) {
				return fmt.Errorf("media processing stuck in %q past timeout", state)
			}
			wait = x.pollInterval()
			if next > 0 {
				wait = time.Duration(next) * time.Second
			}
		default:
			if !time.Now().UTC().Before(deadline) {
				return fmt.Errorf("media processing stuck in %q past timeout", state)
			}
		}
	}
}

func (x *X) confine(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("publish/x: empty media path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	root := strings.TrimSpace(x.DataRoot)
	if root == "" {
		return abs, nil
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absRoot, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("publish/x: path escapes data root")
	}
	return abs, nil
}

func (x *X) token(ctx context.Context, account string) (*oauth2.Token, error) {
	if x.Tokens == nil {
		return nil, fmt.Errorf("publish/x: token source not configured")
	}
	ts, err := x.Tokens(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("publish/x: token for %s: %w", account, err)
	}
	tok, err := ts.Token()
	if err != nil {
		return nil, fmt.Errorf("publish/x: refresh token for %s: %w", account, err)
	}
	if tok == nil || tok.AccessToken == "" {
		return nil, fmt.Errorf("publish/x: empty access token for %s", account)
	}
	return tok, nil
}

func (x *X) api() XAPI {
	if x != nil && x.API != nil {
		return x.API
	}
	return &HTTPXAPI{HTTP: http.DefaultClient, Base: "https://api.x.com"}
}

func (x *X) lookupPublished(ctx context.Context, p PublishRequest) (PublishResult, bool, error) {
	var ext, u, status string
	err := x.DB.QueryRowContext(ctx, `
SELECT COALESCE(external_id,''), COALESCE(url,''), status
FROM publications WHERE idempotency_key=?`, p.IdempotencyKey).Scan(&ext, &u, &status)
	if errors.Is(err, sql.ErrNoRows) {
		if p.PublicationID == "" {
			return PublishResult{}, false, nil
		}
		err = x.DB.QueryRowContext(ctx, `
SELECT COALESCE(external_id,''), COALESCE(url,''), status
FROM publications WHERE id=?`, p.PublicationID).Scan(&ext, &u, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return PublishResult{}, false, nil
		}
	}
	if err != nil {
		return PublishResult{}, false, fmt.Errorf("publish/x: lookup: %w", err)
	}
	if status == "published" && ext != "" {
		return PublishResult{ExternalID: ext, URL: u}, true, nil
	}
	return PublishResult{}, false, nil
}

func (x *X) markPublishing(ctx context.Context, p PublishRequest) error {
	if p.PublicationID == "" {
		return nil
	}
	_, err := x.DB.ExecContext(ctx, `
UPDATE publications SET status='publishing', error=NULL
WHERE id=? AND status IN ('scheduled','publishing','failed')`, p.PublicationID)
	return err
}

func (x *X) completePublication(ctx context.Context, p PublishRequest, id, tweetURL string) error {
	at := x.now().Format(time.RFC3339Nano)
	if p.PublicationID != "" {
		_, err := x.DB.ExecContext(ctx, `
UPDATE publications SET status='published', external_id=?, url=?, published_at=?, error=NULL WHERE id=?`,
			id, tweetURL, at, p.PublicationID)
		return err
	}
	_, err := x.DB.ExecContext(ctx, `
UPDATE publications SET status='published', external_id=?, url=?, published_at=?, error=NULL WHERE idempotency_key=?`,
		id, tweetURL, at, p.IdempotencyKey)
	return err
}

func (x *X) failPublication(ctx context.Context, p PublishRequest, msg string) error {
	if p.PublicationID == "" {
		return nil
	}
	_, err := x.DB.ExecContext(ctx, `UPDATE publications SET status='failed', error=? WHERE id=?`, msg, p.PublicationID)
	return err
}

func (x *X) reserveCap(ctx context.Context, account string) error {
	provider := "x:" + account
	ws := x.now().UTC().Truncate(24 * time.Hour).Format(time.RFC3339Nano)
	res, err := x.DB.ExecContext(ctx, `
INSERT INTO quotas (provider, window_start, used, "limit")
VALUES (?, ?, 1, ?)
ON CONFLICT(provider, window_start) DO UPDATE SET
  used = used + 1
WHERE quotas.used + 1 <= quotas."limit"`, provider, ws, xDailyCap)
	if err != nil {
		return fmt.Errorf("publish/x: daily cap reserve: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("publish/x: daily cap exceeded")
	}
	return nil
}

// RegisterHandler wires publish.x onto the net resource class.
func (x *X) RegisterHandler(q *queue.Queue) {
	if q == nil || x == nil {
		return
	}
	q.Register(JobPublishX, queue.ResourceNet, 5, x.Handle)
}

type xJobPayload struct {
	PublicationID  string `json:"publication_id"`
	ContentID      string `json:"content_id"`
	Account        string `json:"account"`
	IdempotencyKey string `json:"idempotency_key"`
	VideoPath      string `json:"video_path"`
	Description    string `json:"description"`
}

// Handle runs a publish.x job.
func (x *X) Handle(ctx context.Context, job queue.Job) (json.RawMessage, error) {
	var p xJobPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return nil, queue.Permanent(fmt.Errorf("publish/x: payload: %w", err))
	}
	req := PublishRequest{
		PublicationID: p.PublicationID, ContentID: p.ContentID, Account: p.Account,
		IdempotencyKey: p.IdempotencyKey, VideoPath: p.VideoPath, Description: p.Description,
	}
	if req.ContentID == "" && job.ContentID != nil {
		req.ContentID = *job.ContentID
	}
	res, err := x.Publish(ctx, req)
	if err != nil {
		if errors.Is(err, content.ErrNotApproved) {
			return nil, queue.Permanent(err)
		}
		if strings.Contains(err.Error(), "daily cap exceeded") ||
			strings.Contains(err.Error(), "personal account") ||
			strings.Contains(err.Error(), "over length") {
			return nil, queue.Permanent(err)
		}
		return nil, err
	}
	raw, _ := json.Marshal(res)
	return raw, nil
}

func isPersonalXAccount(account string) bool {
	a := strings.ToLower(strings.TrimSpace(account))
	return a == "personal" || a == "x-personal" || a == "x_personal" ||
		strings.Contains(a, "personal") || strings.HasSuffix(a, "-personal")
}

// xTweetText enforces the 280-rune limit without cutting mid-word when truncating is needed.
// Over-length text that cannot be cleanly shortened is rejected.
func xTweetText(desc string) (string, error) {
	s := strings.TrimSpace(desc)
	if s == "" {
		return "", fmt.Errorf("publish/x: empty tweet text")
	}
	if utf8.RuneCountInString(s) <= xMaxTweetRunes {
		return s, nil
	}
	runes := []rune(s)
	cut := runes[:xMaxTweetRunes]
	// Prefer last whitespace boundary.
	str := string(cut)
	if i := strings.LastIndexFunc(str, func(r rune) bool { return r == ' ' || r == '\n' || r == '\t' }); i > xMaxTweetRunes/2 {
		str = strings.TrimSpace(str[:i])
		return str, nil
	}
	return "", fmt.Errorf("publish/x: tweet text over length (%d > %d) and cannot truncate at word boundary", utf8.RuneCountInString(s), xMaxTweetRunes)
}

func xRedact(s string) string {
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

// HTTPXAPI talks to X API v2 media upload + tweets.
type HTTPXAPI struct {
	HTTP *http.Client
	Base string
}

func (a *HTTPXAPI) http() *http.Client {
	if a != nil && a.HTTP != nil {
		return a.HTTP
	}
	return http.DefaultClient
}

func (a *HTTPXAPI) base() string {
	if a != nil && strings.TrimSpace(a.Base) != "" {
		return strings.TrimRight(a.Base, "/")
	}
	return "https://api.x.com"
}

func (a *HTTPXAPI) InitUpload(ctx context.Context, tok *oauth2.Token, mediaType string, totalBytes int64) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"media_category": "tweet_video",
		"media_type":     mediaType,
		"total_bytes":    totalBytes,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base()+"/2/media/upload/initialize", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
		ID string `json:"id"`
	}
	if err := xDoJSON(a.http(), req, &out); err != nil {
		return "", err
	}
	id := out.Data.ID
	if id == "" {
		id = out.ID
	}
	if id == "" {
		return "", fmt.Errorf("empty media id")
	}
	return id, nil
}

func (a *HTTPXAPI) AppendUpload(ctx context.Context, tok *oauth2.Token, mediaID string, segmentIndex int, chunk []byte) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("segment_index", fmt.Sprintf("%d", segmentIndex))
	part, err := w.CreateFormFile("media", "chunk.mp4")
	if err != nil {
		return err
	}
	if _, err := part.Write(chunk); err != nil {
		return err
	}
	_ = w.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base()+"/2/media/upload/"+mediaID+"/append", &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return xDoJSON(a.http(), req, nil)
}

func (a *HTTPXAPI) FinalizeUpload(ctx context.Context, tok *oauth2.Token, mediaID string) (bool, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base()+"/2/media/upload/"+mediaID+"/finalize", nil)
	if err != nil {
		return false, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	var out struct {
		Data struct {
			ProcessingInfo *struct {
				State          string `json:"state"`
				CheckAfterSecs int    `json:"check_after_secs"`
			} `json:"processing_info"`
		} `json:"data"`
		ProcessingInfo *struct {
			State          string `json:"state"`
			CheckAfterSecs int    `json:"check_after_secs"`
		} `json:"processing_info"`
	}
	if err := xDoJSON(a.http(), req, &out); err != nil {
		return false, 0, err
	}
	pi := out.Data.ProcessingInfo
	if pi == nil {
		pi = out.ProcessingInfo
	}
	if pi == nil {
		return false, 0, nil
	}
	switch strings.ToLower(pi.State) {
	case "succeeded":
		return false, 0, nil
	case "failed":
		return false, 0, fmt.Errorf("media processing failed")
	}
	return true, pi.CheckAfterSecs, nil
}

func (a *HTTPXAPI) UploadStatus(ctx context.Context, tok *oauth2.Token, mediaID string) (string, int, error) {
	q := url.Values{}
	q.Set("command", "STATUS")
	q.Set("media_id", mediaID)
	u := a.base() + "/2/media/upload?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	var out struct {
		Data struct {
			ProcessingInfo struct {
				State          string `json:"state"`
				CheckAfterSecs int    `json:"check_after_secs"`
			} `json:"processing_info"`
		} `json:"data"`
		ProcessingInfo struct {
			State          string `json:"state"`
			CheckAfterSecs int    `json:"check_after_secs"`
		} `json:"processing_info"`
	}
	if err := xDoJSON(a.http(), req, &out); err != nil {
		return "", 0, err
	}
	state := out.Data.ProcessingInfo.State
	check := out.Data.ProcessingInfo.CheckAfterSecs
	if state == "" {
		state = out.ProcessingInfo.State
		check = out.ProcessingInfo.CheckAfterSecs
	}
	return state, check, nil
}

func (a *HTTPXAPI) CreateTweet(ctx context.Context, tok *oauth2.Token, text, mediaID string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"text": text,
		"media": map[string]any{
			"media_ids": []string{mediaID},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base()+"/2/tweets", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := xDoJSON(a.http(), req, &out); err != nil {
		return "", err
	}
	if out.Data.ID == "" {
		return "", fmt.Errorf("empty tweet id")
	}
	return out.Data.ID, nil
}

func xDoJSON(client *http.Client, req *http.Request, dest any) error {
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	body = []byte(xRedact(string(body)))
	if dest != nil && len(body) > 0 && body[0] == '{' {
		if err := json.Unmarshal(body, dest); err != nil {
			return fmt.Errorf("decode x response (http %d): %w", res.StatusCode, err)
		}
	}
	if res.StatusCode >= 400 {
		return fmt.Errorf("x http %d: %s", res.StatusCode, xRedact(string(body)))
	}
	return nil
}
