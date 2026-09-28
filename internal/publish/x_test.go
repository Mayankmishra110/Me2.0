package publish

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"golang.org/x/oauth2"

	"mayank2/internal/content"
)

type fakeXAPI struct {
	inits, tweets int
	status        string
	stuck         bool
	failed        bool
	processing    bool
}

func (f *fakeXAPI) InitUpload(ctx context.Context, tok *oauth2.Token, mediaType string, totalBytes int64) (string, error) {
	f.inits++
	return "media-x-1", nil
}
func (f *fakeXAPI) AppendUpload(ctx context.Context, tok *oauth2.Token, mediaID string, segmentIndex int, chunk []byte) error {
	return nil
}
func (f *fakeXAPI) FinalizeUpload(ctx context.Context, tok *oauth2.Token, mediaID string) (bool, int, error) {
	return f.processing || f.stuck || f.failed, 0, nil
}
func (f *fakeXAPI) UploadStatus(ctx context.Context, tok *oauth2.Token, mediaID string) (string, int, error) {
	if f.failed {
		return "failed", 0, nil
	}
	if f.stuck {
		return "in_progress", 0, nil
	}
	if f.status != "" {
		return f.status, 0, nil
	}
	return "succeeded", 0, nil
}
func (f *fakeXAPI) CreateTweet(ctx context.Context, tok *oauth2.Token, text, mediaID string) (string, error) {
	f.tweets++
	return "tweet-1", nil
}

func seedX(t *testing.T, contentID, account, pubID, key string) (*X, string) {
	t.Helper()
	sqlDB := openPubDB(t)
	seedApproved(t, sqlDB, contentID, "ch-"+contentID)
	mustExec(t, sqlDB, `
INSERT INTO publications (id, content_id, platform, account, scheduled_at, status, idempotency_key)
VALUES (?, ?, 'x', ?, ?, 'scheduled', ?)`,
		pubID, contentID, account, time.Now().UTC().Format(time.RFC3339Nano), key)
	video := filepath.Join(t.TempDir(), "short.mp4")
	if err := os.WriteFile(video, []byte("fake-video-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &X{
		DB: sqlDB, Tokens: staticToken, API: &fakeXAPI{processing: true},
		Now:          func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) },
		PollInterval: time.Millisecond,
		PollTimeout:  40 * time.Millisecond,
	}, video
}

func TestX_PublishHappyPath(t *testing.T) {
	x, video := seedX(t, "c-x1", "x-business", "pub-x1", "c-x1:x:x-business")
	api := x.API.(*fakeXAPI)
	res, err := x.Publish(context.Background(), PublishRequest{
		PublicationID: "pub-x1", ContentID: "c-x1", Account: "x-business",
		IdempotencyKey: "c-x1:x:x-business", VideoPath: video, Description: "hello business",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if res.ExternalID != "tweet-1" || api.inits != 1 || api.tweets != 1 {
		t.Fatalf("res=%+v api=%+v", res, api)
	}
}

func TestX_Idempotent(t *testing.T) {
	x, video := seedX(t, "c-x2", "x-business-2", "pub-x2", "c-x2:x:x-business-2")
	api := x.API.(*fakeXAPI)
	req := PublishRequest{
		PublicationID: "pub-x2", ContentID: "c-x2", Account: "x-business-2",
		IdempotencyKey: "c-x2:x:x-business-2", VideoPath: video, Description: "hi",
	}
	if _, err := x.Publish(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Publish(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if api.inits != 1 {
		t.Fatalf("want 1 init, got %d", api.inits)
	}
}

func TestX_MissingApproval(t *testing.T) {
	sqlDB := openPubDB(t)
	warmup := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339Nano)
	mustExec(t, sqlDB, `
INSERT INTO channels (id, platform, handle, language, niche, account_ref, status, warmup_started_at)
VALUES ('ch-x', 'x', 'h', 'en', 'money', 'xb', 'active', ?)`, warmup)
	mustExec(t, sqlDB, `
INSERT INTO content_items (id, channel_id, kind, language, stage, created_at)
VALUES ('c-no', 'ch-x', 'short', 'en', 'rendered', ?)`, time.Now().UTC().Format(time.RFC3339Nano))
	video := filepath.Join(t.TempDir(), "v.mp4")
	_ = os.WriteFile(video, []byte("v"), 0o644)
	x := &X{DB: sqlDB, Tokens: staticToken, API: &fakeXAPI{}}
	_, err := x.Publish(context.Background(), PublishRequest{
		ContentID: "c-no", Account: "xb", IdempotencyKey: "k", VideoPath: video, Description: "hi",
	})
	if !errors.Is(err, content.ErrNotApproved) {
		t.Fatalf("want ErrNotApproved, got %v", err)
	}
}

func TestX_MediaStuck(t *testing.T) {
	x, video := seedX(t, "c-x3", "x-business-3", "pub-x3", "c-x3:x:x-business-3")
	x.API = &fakeXAPI{stuck: true, processing: true}
	_, err := x.Publish(context.Background(), PublishRequest{
		PublicationID: "pub-x3", ContentID: "c-x3", Account: "x-business-3",
		IdempotencyKey: "c-x3:x:x-business-3", VideoPath: video, Description: "hi",
	})
	if err == nil || !strings.Contains(err.Error(), "stuck") {
		t.Fatalf("want stuck, got %v", err)
	}
}

func TestX_MediaFailed(t *testing.T) {
	x, video := seedX(t, "c-x4", "x-business-4", "pub-x4", "c-x4:x:x-business-4")
	x.API = &fakeXAPI{failed: true, processing: true}
	_, err := x.Publish(context.Background(), PublishRequest{
		PublicationID: "pub-x4", ContentID: "c-x4", Account: "x-business-4",
		IdempotencyKey: "c-x4:x:x-business-4", VideoPath: video, Description: "hi",
	})
	if err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("want failed, got %v", err)
	}
}

func TestX_OverLengthCaption(t *testing.T) {
	long := strings.Repeat("word ", 100) // well over 280 runes with spaces — truncatable
	text, err := xTweetText(long)
	if err != nil {
		t.Fatalf("truncatable should succeed: %v", err)
	}
	if utf8.RuneCountInString(text) > xMaxTweetRunes {
		t.Fatalf("still too long: %d", utf8.RuneCountInString(text))
	}
	// No whitespace near the cut → reject
	noSpace := strings.Repeat("a", 400)
	if _, err := xTweetText(noSpace); err == nil || !strings.Contains(err.Error(), "over length") {
		t.Fatalf("want over length reject, got %v", err)
	}

	x, video := seedX(t, "c-x-ol", "x-business-ol", "pub-x-ol", "c-x-ol:x:x-business-ol")
	api := x.API.(*fakeXAPI)
	_, err = x.Publish(context.Background(), PublishRequest{
		PublicationID: "pub-x-ol", ContentID: "c-x-ol", Account: "x-business-ol",
		IdempotencyKey: "c-x-ol:x:x-business-ol", VideoPath: video, Description: noSpace,
	})
	if err == nil || !strings.Contains(err.Error(), "over length") {
		t.Fatalf("want Publish over length, got %v", err)
	}
	if api.inits != 0 {
		t.Fatalf("must not call X API when caption rejected, inits=%d", api.inits)
	}
}

func TestX_DailyCap(t *testing.T) {
	x, video := seedX(t, "c-x-cap", "x-business-cap", "pub-x-cap", "c-x-cap:x:x-business-cap")
	ws := x.now().UTC().Truncate(24 * time.Hour).Format(time.RFC3339Nano)
	mustExec(t, x.DB, `
INSERT INTO quotas (provider, window_start, used, "limit") VALUES (?, ?, ?, ?)`,
		"x:x-business-cap", ws, xDailyCap, xDailyCap)
	api := x.API.(*fakeXAPI)
	_, err := x.Publish(context.Background(), PublishRequest{
		PublicationID: "pub-x-cap", ContentID: "c-x-cap", Account: "x-business-cap",
		IdempotencyKey: "c-x-cap:x:x-business-cap", VideoPath: video, Description: "hi",
	})
	if err == nil || !strings.Contains(err.Error(), "daily cap exceeded") {
		t.Fatalf("want daily cap, got %v", err)
	}
	if api.inits != 0 {
		t.Fatalf("must not call X API past cap, inits=%d", api.inits)
	}
}

// TestX_AccountRefNamingConvention locks in the account_ref naming convention
// this business publisher relies on to stay separated from a future personal
// publisher (M2-403): internal/secrets keys tokens by (platform="x", account),
// so the account_ref string is the only thing keeping the two accounts apart.
// Business-style refs (no "personal" substring, e.g. "x-business...") must be
// accepted; any ref containing "personal" — which M2-403's implementer MUST
// use for the personal account, e.g. "x-personal" — must be refused here.
func TestX_AccountRefNamingConvention(t *testing.T) {
	cases := []struct {
		account string
		isPers  bool
	}{
		{"x-business", false},
		{"x-business-money-en", false},
		{"xb", false},
		{"x_business", false},
		{"personal", true},
		{"x-personal", true},
		{"x_personal", true},
		{"X-PERSONAL", true},
		{"mayank-personal", true},
		{"x-business-personal-blend", true}, // any "personal" substring is refused, by design
	}
	for _, tc := range cases {
		t.Run(tc.account, func(t *testing.T) {
			if got := isPersonalXAccount(tc.account); got != tc.isPers {
				t.Fatalf("isPersonalXAccount(%q) = %v, want %v", tc.account, got, tc.isPers)
			}
		})
	}
}

func TestX_RefusePersonalAccount(t *testing.T) {
	x, video := seedX(t, "c-x5", "x-business-5", "pub-x5", "c-x5:x:x-business-5")
	_, err := x.Publish(context.Background(), PublishRequest{
		PublicationID: "pub-x5", ContentID: "c-x5", Account: "x-personal",
		IdempotencyKey: "c-x5:x:x-personal", VideoPath: video, Description: "hi",
	})
	if err == nil || !strings.Contains(err.Error(), "personal") {
		t.Fatalf("want personal refuse, got %v", err)
	}
}

func TestX_HTTPHappyPath(t *testing.T) {
	var tweets atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/initialize"):
			_, _ = w.Write([]byte(`{"data":{"id":"m-http"}}`))
		case strings.HasSuffix(r.URL.Path, "/append"):
			w.WriteHeader(200)
		case strings.HasSuffix(r.URL.Path, "/finalize"):
			_, _ = w.Write([]byte(`{"data":{"processing_info":{"state":"pending","check_after_secs":0}}}`))
		case strings.Contains(r.URL.RawQuery, "command=STATUS"):
			_, _ = w.Write([]byte(`{"data":{"processing_info":{"state":"succeeded"}}}`))
		case strings.HasSuffix(r.URL.Path, "/2/tweets"):
			tweets.Add(1)
			_, _ = w.Write([]byte(`{"data":{"id":"tw-http"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	x, video := seedX(t, "c-x6", "x-business-6", "pub-x6", "c-x6:x:x-business-6")
	x.API = &HTTPXAPI{HTTP: srv.Client(), Base: srv.URL}
	x.PollInterval = time.Millisecond
	res, err := x.Publish(context.Background(), PublishRequest{
		PublicationID: "pub-x6", ContentID: "c-x6", Account: "x-business-6",
		IdempotencyKey: "c-x6:x:x-business-6", VideoPath: video, Description: "http path",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if res.ExternalID != "tw-http" || tweets.Load() != 1 {
		t.Fatalf("res=%+v tweets=%d", res, tweets.Load())
	}
}
