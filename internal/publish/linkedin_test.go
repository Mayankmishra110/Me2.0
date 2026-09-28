package publish

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"mayank2/internal/queue"
)

// fakeLinkedInAPI is the in-memory LinkedInAPI double for unit tests that
// don't need a real HTTP round trip.
type fakeLinkedInAPI struct {
	whoAmICalls int
	postCalls   int
	authorURN   string
	postURN     string
	failWhoAmI  error
	failPost    error
	lastText    string
}

func (f *fakeLinkedInAPI) WhoAmI(ctx context.Context, tok *oauth2.Token) (string, error) {
	f.whoAmICalls++
	if f.failWhoAmI != nil {
		return "", f.failWhoAmI
	}
	if tok == nil || tok.AccessToken == "" {
		return "", errors.New("no token")
	}
	if f.authorURN == "" {
		return "urn:li:person:mayank", nil
	}
	return f.authorURN, nil
}

func (f *fakeLinkedInAPI) CreatePost(ctx context.Context, tok *oauth2.Token, authorURN, text string) (string, error) {
	f.postCalls++
	f.lastText = text
	if f.failPost != nil {
		return "", f.failPost
	}
	if f.postURN == "" {
		return "urn:li:share:12345", nil
	}
	return f.postURN, nil
}

// seedApprovedBlog seeds a channels + content_items('blog') + approvals row,
// mirroring seedApproved (youtube_test.go) but for a non-video kind since
// content_items.kind='short' there doesn't fit the LinkedIn repurpose leg.
func seedApprovedBlog(t *testing.T, sqlDB *sql.DB, contentID, channelID string) {
	t.Helper()
	mustExec(t, sqlDB, `
INSERT INTO channels (id, platform, handle, language, niche, account_ref, status)
VALUES (?, 'mayankbuilt', '', 'en', 'blog', '', 'active')`, channelID)
	mustExec(t, sqlDB, `
INSERT INTO content_items (id, channel_id, kind, language, stage, created_at)
VALUES (?, ?, 'blog', 'en', 'live', ?)`, contentID, channelID, time.Now().UTC().Format(time.RFC3339Nano))
	mustExec(t, sqlDB, `
INSERT INTO approvals (id, content_id, kind, summary, status, nonce, decided_at)
VALUES (?, ?, 'linkedin', 'ok', 'approved', '', ?)`, "ap-"+contentID, contentID, time.Now().UTC().Format(time.RFC3339Nano))
}

func TestLinkedIn_PublishHappyPath(t *testing.T) {
	sqlDB := openPubDB(t)
	seedApprovedBlog(t, sqlDB, "c1", "blog-en")
	mustExec(t, sqlDB, `
INSERT INTO publications (id, content_id, platform, account, status, idempotency_key)
VALUES ('pub1', 'c1', 'linkedin', 'mayank-personal', 'scheduled', 'c1:linkedin:mayank-personal')`)

	api := &fakeLinkedInAPI{}
	li := &LinkedIn{
		DB: sqlDB, Tokens: staticToken, API: api,
		Now: func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) },
	}
	res, err := li.Publish(context.Background(), PublishRequest{
		PublicationID:  "pub1",
		ContentID:      "c1",
		Account:        "mayank-personal",
		IdempotencyKey: "c1:linkedin:mayank-personal",
		Description:    "A punchy LinkedIn draft. https://mayank.dev/blog/x",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if res.ExternalID != "urn:li:share:12345" {
		t.Fatalf("ExternalID = %q", res.ExternalID)
	}
	if !strings.Contains(res.URL, "urn:li:share:12345") {
		t.Fatalf("URL = %q", res.URL)
	}
	if api.whoAmICalls != 1 || api.postCalls != 1 {
		t.Fatalf("whoAmI=%d post=%d", api.whoAmICalls, api.postCalls)
	}
	if api.lastText != "A punchy LinkedIn draft. https://mayank.dev/blog/x" {
		t.Fatalf("post text = %q", api.lastText)
	}

	var st, ext string
	if err := sqlDB.QueryRow(`SELECT status, external_id FROM publications WHERE id='pub1'`).Scan(&st, &ext); err != nil {
		t.Fatalf("query: %v", err)
	}
	if st != "published" || ext != "urn:li:share:12345" {
		t.Fatalf("pub status=%q ext=%q", st, ext)
	}
	var used, limit int
	if err := sqlDB.QueryRow(`SELECT used, "limit" FROM quotas WHERE provider='linkedin:mayank-personal'`).Scan(&used, &limit); err != nil {
		t.Fatalf("quota query: %v", err)
	}
	if used != 1 || limit != linkedInDailyCap {
		t.Fatalf("quota used=%d limit=%d", used, limit)
	}
}

func TestLinkedIn_Idempotent(t *testing.T) {
	sqlDB := openPubDB(t)
	seedApprovedBlog(t, sqlDB, "c1", "blog-en")
	mustExec(t, sqlDB, `
INSERT INTO publications (id, content_id, platform, account, status, external_id, url, idempotency_key, published_at)
VALUES ('pub1', 'c1', 'linkedin', 'mayank-personal', 'published', 'urn:li:share:old', 'https://www.linkedin.com/feed/update/urn:li:share:old/', 'key1', ?)`,
		time.Now().UTC().Format(time.RFC3339Nano))
	api := &fakeLinkedInAPI{}
	li := &LinkedIn{DB: sqlDB, Tokens: staticToken, API: api}
	res, err := li.Publish(context.Background(), PublishRequest{
		PublicationID: "pub1", ContentID: "c1", Account: "mayank-personal", IdempotencyKey: "key1",
		Description: "text",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if res.ExternalID != "urn:li:share:old" || api.postCalls != 0 {
		t.Fatalf("idempotent fail res=%+v postCalls=%d", res, api.postCalls)
	}
}

func TestLinkedIn_RequiresApproval(t *testing.T) {
	sqlDB := openPubDB(t)
	mustExec(t, sqlDB, `
INSERT INTO channels (id, platform, language, niche, status) VALUES ('blog-en', 'mayankbuilt', 'en', 'blog', 'active')`)
	mustExec(t, sqlDB, `
INSERT INTO content_items (id, channel_id, kind, language, stage, created_at)
VALUES ('c1', 'blog-en', 'blog', 'en', 'live', ?)`, time.Now().UTC().Format(time.RFC3339Nano))
	li := &LinkedIn{DB: sqlDB, Tokens: staticToken, API: &fakeLinkedInAPI{}}
	_, err := li.Publish(context.Background(), PublishRequest{
		ContentID: "c1", Account: "mayank-personal", IdempotencyKey: "k",
		Description: "text",
	})
	if err == nil || !strings.Contains(err.Error(), "no approved approval") {
		t.Fatalf("want ErrNotApproved, got %v", err)
	}
}

func TestLinkedIn_DailyCapExceeded(t *testing.T) {
	sqlDB := openPubDB(t)
	seedApprovedBlog(t, sqlDB, "c1", "blog-en")
	mustExec(t, sqlDB, `
INSERT INTO publications (id, content_id, platform, account, status, idempotency_key)
VALUES ('pub1', 'c1', 'linkedin', 'mayank-personal', 'scheduled', 'k1')`)
	ws := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	mustExec(t, sqlDB, `INSERT INTO quotas (provider, window_start, used, "limit") VALUES ('linkedin:mayank-personal', ?, 1, 1)`, ws)
	api := &fakeLinkedInAPI{}
	li := &LinkedIn{
		DB: sqlDB, Tokens: staticToken, API: api,
		Now: func() time.Time { return time.Date(2026, 9, 28, 15, 0, 0, 0, time.UTC) },
	}
	_, err := li.Publish(context.Background(), PublishRequest{
		PublicationID: "pub1", ContentID: "c1", Account: "mayank-personal", IdempotencyKey: "k1",
		Description: "text",
	})
	if err == nil || !strings.Contains(err.Error(), "daily cap exceeded") {
		t.Fatalf("want daily cap error, got %v", err)
	}
	if api.postCalls != 0 {
		t.Fatal("must not post when over the daily cap")
	}
	var st, lastErr string
	_ = sqlDB.QueryRow(`SELECT status, error FROM publications WHERE id='pub1'`).Scan(&st, &lastErr)
	if st != "failed" || !strings.Contains(lastErr, "daily cap") {
		t.Fatalf("pub status=%q error=%q", st, lastErr)
	}
}

func TestLinkedIn_EmptyText(t *testing.T) {
	sqlDB := openPubDB(t)
	seedApprovedBlog(t, sqlDB, "c1", "blog-en")
	li := &LinkedIn{DB: sqlDB, Tokens: staticToken, API: &fakeLinkedInAPI{}}
	_, err := li.Publish(context.Background(), PublishRequest{
		ContentID: "c1", Account: "mayank-personal", IdempotencyKey: "k", Description: "   ",
	})
	if err == nil || !strings.Contains(err.Error(), "empty post text") {
		t.Fatalf("want empty post text error, got %v", err)
	}
}

func TestLinkedIn_CreatePostAPIError(t *testing.T) {
	sqlDB := openPubDB(t)
	seedApprovedBlog(t, sqlDB, "c1", "blog-en")
	mustExec(t, sqlDB, `
INSERT INTO publications (id, content_id, platform, account, status, idempotency_key)
VALUES ('pub1', 'c1', 'linkedin', 'mayank-personal', 'scheduled', 'k1')`)
	api := &fakeLinkedInAPI{failPost: errors.New("posts HTTP 401: Bearer sometoken invalid")}
	li := &LinkedIn{DB: sqlDB, Tokens: staticToken, API: api}
	_, err := li.Publish(context.Background(), PublishRequest{
		PublicationID: "pub1", ContentID: "c1", Account: "mayank-personal", IdempotencyKey: "k1",
		Description: "text",
	})
	if err == nil {
		t.Fatal("want error")
	}
	var lastErr string
	_ = sqlDB.QueryRow(`SELECT error FROM publications WHERE id='pub1'`).Scan(&lastErr)
	if strings.Contains(lastErr, "sometoken") {
		t.Fatalf("token leaked into last_error: %q", lastErr)
	}
	if !strings.Contains(lastErr, "[redacted]") {
		t.Fatalf("expected redaction marker in last_error: %q", lastErr)
	}
}

func TestLinkedIn_HandleJob(t *testing.T) {
	sqlDB := openPubDB(t)
	seedApprovedBlog(t, sqlDB, "c1", "blog-en")
	api := &fakeLinkedInAPI{}
	li := &LinkedIn{DB: sqlDB, Tokens: staticToken, API: api}
	q := queue.New(sqlDB, queue.WithWorkerName("li-test"))
	li.RegisterHandler(q)
	payload, _ := json.Marshal(linkedInJobPayload{
		ContentID: "c1", Account: "mayank-personal", IdempotencyKey: "job-key", Text: "hello",
	})
	raw, err := li.Handle(context.Background(), queue.Job{Type: JobPublishLinkedIn, Payload: payload})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	var res PublishResult
	if err := json.Unmarshal(raw, &res); err != nil || res.ExternalID == "" {
		t.Fatalf("result %s err=%v", raw, err)
	}
}

func TestLinkedIn_HandleJob_NotApprovedIsPermanent(t *testing.T) {
	sqlDB := openPubDB(t)
	mustExec(t, sqlDB, `
INSERT INTO channels (id, platform, language, niche, status) VALUES ('blog-en', 'mayankbuilt', 'en', 'blog', 'active')`)
	mustExec(t, sqlDB, `
INSERT INTO content_items (id, channel_id, kind, language, stage, created_at)
VALUES ('c1', 'blog-en', 'blog', 'en', 'live', ?)`, time.Now().UTC().Format(time.RFC3339Nano))
	li := &LinkedIn{DB: sqlDB, Tokens: staticToken, API: &fakeLinkedInAPI{}}
	q := queue.New(sqlDB, queue.WithWorkerName("li-test"))
	li.RegisterHandler(q)
	payload, _ := json.Marshal(linkedInJobPayload{ContentID: "c1", Account: "a", IdempotencyKey: "k", Text: "x"})
	_, err := li.Handle(context.Background(), queue.Job{Type: JobPublishLinkedIn, Payload: payload})
	if err == nil {
		t.Fatal("want error")
	}
	if !queue.IsPermanent(err) {
		t.Fatalf("want a queue.Permanent error, got %T: %v", err, err)
	}
}

// TestLiRedact guards against the specific infinite-loop bug this had during
// development: the needle ("bearer ", etc.) is never consumed by the
// replacement, so a naive "keep searching the whole string" loop never
// terminates once the text stabilizes at "[redacted]". This test's presence
// matters more than its assertions — `go test -timeout` turns a regression
// back into a hard failure instead of a silently hung worker.
func TestLiRedact(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bearer token", "posts HTTP 401: Bearer sometoken invalid", "posts HTTP 401: Bearer [redacted] invalid"},
		{"access_token query param", "GET /x?access_token=abcdef123&foo=bar", "GET /x?access_token=[redacted]&foo=bar"},
		{"oauth prefix", `error: oauth secret123 rejected`, "error: oauth [redacted] rejected"},
		{"no match", "plain error message", "plain error message"},
		{"token at end of string", "Bearer abc", "Bearer [redacted]"},
		{"two bearer occurrences", "Bearer aaa and later Bearer bbb", "Bearer [redacted] and later Bearer [redacted]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			done := make(chan string, 1)
			go func() { done <- liRedact(tt.in) }()
			select {
			case got := <-done:
				if got != tt.want {
					t.Fatalf("liRedact(%q) = %q, want %q", tt.in, got, tt.want)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("liRedact(%q) did not return within 5s (infinite loop regression)", tt.in)
			}
		})
	}
}

// --- HTTP client against httptest ---------------------------------------

func TestHTTPLinkedInAPI_WhoAmI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/userinfo" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-tok" {
			t.Fatalf("Authorization header = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sub":"abc123"}`))
	}))
	defer srv.Close()

	api := &HTTPLinkedInAPI{HTTP: srv.Client(), Base: srv.URL}
	urn, err := api.WhoAmI(context.Background(), &oauth2.Token{AccessToken: "test-tok"})
	if err != nil {
		t.Fatalf("WhoAmI: %v", err)
	}
	if urn != "urn:li:person:abc123" {
		t.Fatalf("urn = %q", urn)
	}
}

func TestHTTPLinkedInAPI_WhoAmI_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"invalid token, bearer abc.def.ghi rejected"}`))
	}))
	defer srv.Close()

	api := &HTTPLinkedInAPI{HTTP: srv.Client(), Base: srv.URL}
	_, err := api.WhoAmI(context.Background(), &oauth2.Token{AccessToken: "test-tok"})
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), "abc.def.ghi") {
		t.Fatalf("token leaked in error: %v", err)
	}
}

func TestHTTPLinkedInAPI_CreatePost(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/posts" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("X-Restli-Protocol-Version"); got != "2.0.0" {
			t.Fatalf("X-Restli-Protocol-Version = %q", got)
		}
		if got := r.Header.Get("LinkedIn-Version"); got == "" {
			t.Fatal("missing LinkedIn-Version header")
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("x-restli-id", "urn:li:share:999")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	api := &HTTPLinkedInAPI{HTTP: srv.Client(), Base: srv.URL}
	urn, err := api.CreatePost(context.Background(), &oauth2.Token{AccessToken: "tok"}, "urn:li:person:x", "hello world")
	if err != nil {
		t.Fatalf("CreatePost: %v", err)
	}
	if urn != "urn:li:share:999" {
		t.Fatalf("urn = %q", urn)
	}
	if gotBody["author"] != "urn:li:person:x" || gotBody["commentary"] != "hello world" {
		t.Fatalf("body = %+v", gotBody)
	}
}

func TestHTTPLinkedInAPI_CreatePost_BodyID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"urn:li:share:body-id"}`))
	}))
	defer srv.Close()
	api := &HTTPLinkedInAPI{HTTP: srv.Client(), Base: srv.URL}
	urn, err := api.CreatePost(context.Background(), &oauth2.Token{AccessToken: "tok"}, "urn:li:person:x", "hello")
	if err != nil {
		t.Fatalf("CreatePost: %v", err)
	}
	if urn != "urn:li:share:body-id" {
		t.Fatalf("urn = %q", urn)
	}
}

func TestHTTPLinkedInAPI_CreatePost_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"not enough permissions"}`))
	}))
	defer srv.Close()
	api := &HTTPLinkedInAPI{HTTP: srv.Client(), Base: srv.URL}
	_, err := api.CreatePost(context.Background(), &oauth2.Token{AccessToken: "tok"}, "urn:li:person:x", "hello")
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("want HTTP 403 error, got %v", err)
	}
}

func TestLinkedIn_PublishViaRealHTTPFakeServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/userinfo":
			_, _ = w.Write([]byte(`{"sub":"mayank123"}`))
		case "/rest/posts":
			w.Header().Set("x-restli-id", "urn:li:share:e2e")
			w.WriteHeader(http.StatusCreated)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	sqlDB := openPubDB(t)
	seedApprovedBlog(t, sqlDB, "c1", "blog-en")
	mustExec(t, sqlDB, `
INSERT INTO publications (id, content_id, platform, account, status, idempotency_key)
VALUES ('pub1', 'c1', 'linkedin', 'mayank-personal', 'scheduled', 'e2e-key')`)

	li := &LinkedIn{
		DB: sqlDB, Tokens: staticToken,
		API: &HTTPLinkedInAPI{HTTP: srv.Client(), Base: srv.URL},
	}
	res, err := li.Publish(context.Background(), PublishRequest{
		PublicationID: "pub1", ContentID: "c1", Account: "mayank-personal", IdempotencyKey: "e2e-key",
		Description: "end to end draft text",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if res.ExternalID != "urn:li:share:e2e" {
		t.Fatalf("ExternalID = %q", res.ExternalID)
	}
}
