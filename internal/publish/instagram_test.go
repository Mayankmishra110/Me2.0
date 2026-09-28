package publish

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"mayank2/internal/content"
)

type fakeIGPresign struct {
	url string
	n   int
}

func (f *fakeIGPresign) PresignGET(ctx context.Context, key string, expiry time.Duration) (string, time.Duration, error) {
	f.n++
	if f.url == "" {
		return "https://r2.example/" + key, expiry, nil
	}
	return f.url, expiry, nil
}

type fakeIGAPI struct {
	creates   int
	publishes int
	status    string
	stuck     bool
	errStatus bool
	calls     int
}

func (f *fakeIGAPI) CreateReelContainer(ctx context.Context, tok *oauth2.Token, igUserID, videoURL, caption string) (string, error) {
	f.creates++
	if tok == nil || tok.AccessToken == "" {
		return "", errors.New("no token")
	}
	if videoURL == "" {
		return "", errors.New("empty video url")
	}
	return "ctr-1", nil
}

func (f *fakeIGAPI) ContainerStatus(ctx context.Context, tok *oauth2.Token, containerID string) (string, error) {
	f.calls++
	if f.errStatus {
		return "ERROR", nil
	}
	if f.stuck {
		return "IN_PROGRESS", nil
	}
	if f.status != "" {
		return f.status, nil
	}
	return "FINISHED", nil
}

func (f *fakeIGAPI) PublishContainer(ctx context.Context, tok *oauth2.Token, igUserID, containerID string) (string, error) {
	f.publishes++
	return "media-1", nil
}

func (f *fakeIGAPI) MediaPermalink(ctx context.Context, tok *oauth2.Token, mediaID string) (string, error) {
	return "https://www.instagram.com/reel/" + mediaID + "/", nil
}

func seedIGPub(t *testing.T, contentID, account, pubID, key string) *Instagram {
	t.Helper()
	sqlDB := openPubDB(t)
	seedApproved(t, sqlDB, contentID, "ch-"+contentID)
	mustExec(t, sqlDB, `
INSERT INTO publications (id, content_id, platform, account, scheduled_at, status, idempotency_key)
VALUES (?, ?, 'instagram', ?, ?, 'scheduled', ?)`,
		pubID, contentID, account, time.Now().UTC().Format(time.RFC3339Nano), key)
	return &Instagram{
		DB:           sqlDB,
		Tokens:       staticToken,
		API:          &fakeIGAPI{},
		Presign:      &fakeIGPresign{},
		Now:          func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) },
		PollInterval: time.Millisecond,
		PollTimeout:  50 * time.Millisecond,
	}
}

func TestInstagram_PublishHappyPath(t *testing.T) {
	ig := seedIGPub(t, "c-ig1", "ig-biz-1", "pub-ig1", "c-ig1:instagram:ig-biz-1")
	api := ig.API.(*fakeIGAPI)
	res, err := ig.Publish(context.Background(), PublishRequest{
		PublicationID:     "pub-ig1",
		ContentID:         "c-ig1",
		Account:           "ig-biz-1",
		IdempotencyKey:    "c-ig1:instagram:ig-biz-1",
		VideoPath:         "renders/short.mp4",
		Description:       "A tip",
		ContainsSynthetic: true,
		PaidPromotion:     true,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if res.ExternalID != "media-1" || !strings.Contains(res.URL, "media-1") {
		t.Fatalf("result=%+v", res)
	}
	if api.creates != 1 || api.publishes != 1 {
		t.Fatalf("creates=%d publishes=%d", api.creates, api.publishes)
	}
	cap := igCaption("A tip", true, true)
	if !strings.Contains(cap, igAffiliateDisclosure) || !strings.Contains(cap, igSyntheticDisclosure) {
		t.Fatalf("caption missing disclosures: %q", cap)
	}
}

func TestInstagram_Idempotent(t *testing.T) {
	ig := seedIGPub(t, "c-ig2", "ig-biz-2", "pub-ig2", "c-ig2:instagram:ig-biz-2")
	api := ig.API.(*fakeIGAPI)
	req := PublishRequest{
		PublicationID:  "pub-ig2",
		ContentID:      "c-ig2",
		Account:        "ig-biz-2",
		IdempotencyKey: "c-ig2:instagram:ig-biz-2",
		VideoPath:      "renders/short.mp4",
		Description:    "x",
	}
	if _, err := ig.Publish(context.Background(), req); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := ig.Publish(context.Background(), req); err != nil {
		t.Fatalf("second: %v", err)
	}
	if api.creates != 1 {
		t.Fatalf("want 1 create on replay, got %d", api.creates)
	}
}

func TestInstagram_MissingApproval(t *testing.T) {
	sqlDB := openPubDB(t)
	warmup := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339Nano)
	mustExec(t, sqlDB, `
INSERT INTO channels (id, platform, handle, language, niche, account_ref, status, warmup_started_at)
VALUES ('ch-x', 'instagram', 'h', 'en', 'money', 'ig', 'active', ?)`, warmup)
	mustExec(t, sqlDB, `
INSERT INTO content_items (id, channel_id, kind, language, stage, created_at)
VALUES ('c-no', 'ch-x', 'short', 'en', 'rendered', ?)`, time.Now().UTC().Format(time.RFC3339Nano))
	ig := &Instagram{DB: sqlDB, Tokens: staticToken, API: &fakeIGAPI{}, Presign: &fakeIGPresign{}}
	_, err := ig.Publish(context.Background(), PublishRequest{
		ContentID: "c-no", Account: "ig", IdempotencyKey: "c-no:instagram:ig", VideoPath: "k",
	})
	if !errors.Is(err, content.ErrNotApproved) {
		t.Fatalf("want ErrNotApproved, got %v", err)
	}
	if ig.API.(*fakeIGAPI).creates != 0 {
		t.Fatal("must not call Graph without approval")
	}
}

func TestInstagram_ContainerStuck(t *testing.T) {
	ig := seedIGPub(t, "c-ig3", "ig-biz-3", "pub-ig3", "c-ig3:instagram:ig-biz-3")
	ig.API = &fakeIGAPI{stuck: true}
	_, err := ig.Publish(context.Background(), PublishRequest{
		PublicationID: "pub-ig3", ContentID: "c-ig3", Account: "ig-biz-3",
		IdempotencyKey: "c-ig3:instagram:ig-biz-3", VideoPath: "k",
	})
	if err == nil || !strings.Contains(err.Error(), "stuck") {
		t.Fatalf("want stuck error, got %v", err)
	}
}

func TestInstagram_ContainerError(t *testing.T) {
	ig := seedIGPub(t, "c-ig4", "ig-biz-4", "pub-ig4", "c-ig4:instagram:ig-biz-4")
	ig.API = &fakeIGAPI{errStatus: true}
	_, err := ig.Publish(context.Background(), PublishRequest{
		PublicationID: "pub-ig4", ContentID: "c-ig4", Account: "ig-biz-4",
		IdempotencyKey: "c-ig4:instagram:ig-biz-4", VideoPath: "k",
	})
	if err == nil || !strings.Contains(err.Error(), "ERROR") {
		t.Fatalf("want ERROR status, got %v", err)
	}
}

func TestIgRedact(t *testing.T) {
	cases := []struct{ in, wantNot string }{
		{"graph code=190 message=bad access_token=SECRETTOKEN123", "SECRETTOKEN123"},
		{"unauthorized: Bearer abc.def.ghi expired", "abc.def.ghi"},
		{`{"access_token":"tok","oauth token=xyz999"}`, "xyz999"},
	}
	for _, c := range cases {
		got := igRedact(c.in)
		if strings.Contains(got, c.wantNot) {
			t.Fatalf("igRedact(%q) = %q, still contains secret %q", c.in, got, c.wantNot)
		}
	}
}

// tokenLeakIGAPI returns an error embedding a token-shaped substring, to prove
// failPublication/last_error never persists the raw token (COMPLIANCE / SPEC §3).
type tokenLeakIGAPI struct{ fakeIGAPI }

func (f *tokenLeakIGAPI) CreateReelContainer(ctx context.Context, tok *oauth2.Token, igUserID, videoURL, caption string) (string, error) {
	return "", errors.New("graph http 401: access_token=" + tok.AccessToken + " invalid")
}

func TestInstagram_TokenNeverLogged(t *testing.T) {
	ig := seedIGPub(t, "c-ig6", "ig-biz-6", "pub-ig6", "c-ig6:instagram:ig-biz-6")
	ig.API = &tokenLeakIGAPI{}
	_, err := ig.Publish(context.Background(), PublishRequest{
		PublicationID: "pub-ig6", ContentID: "c-ig6", Account: "ig-biz-6",
		IdempotencyKey: "c-ig6:instagram:ig-biz-6", VideoPath: "k",
	})
	if err == nil {
		t.Fatal("want error")
	}
	token := "test-access-ig-biz-6" // staticToken's shape, see staticToken() in youtube_test.go
	if strings.Contains(err.Error(), token) {
		t.Fatalf("Publish error contains raw token: %v", err)
	}
	var lastErr string
	if scanErr := ig.DB.QueryRow(`SELECT COALESCE(error,'') FROM publications WHERE id='pub-ig6'`).Scan(&lastErr); scanErr != nil {
		t.Fatalf("query last_error: %v", scanErr)
	}
	if strings.Contains(lastErr, token) {
		t.Fatalf("publications.error contains raw token: %q", lastErr)
	}
	if !strings.Contains(lastErr, "[redacted]") {
		t.Fatalf("publications.error missing redaction marker: %q", lastErr)
	}
}

func TestInstagram_HTTPHappyPath(t *testing.T) {
	var publishes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/media") && r.Method == http.MethodPost && !strings.Contains(r.URL.Path, "media_publish"):
			_, _ = w.Write([]byte(`{"id":"ctr-http"}`))
		case strings.Contains(r.URL.RawQuery, "fields=status_code"):
			_, _ = w.Write([]byte(`{"status_code":"FINISHED"}`))
		case strings.HasSuffix(r.URL.Path, "/media_publish"):
			publishes.Add(1)
			_, _ = w.Write([]byte(`{"id":"media-http"}`))
		case strings.Contains(r.URL.RawQuery, "fields=permalink"):
			_, _ = w.Write([]byte(`{"permalink":"https://www.instagram.com/reel/media-http/"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	ig := seedIGPub(t, "c-ig5", "ig-biz-5", "pub-ig5", "c-ig5:instagram:ig-biz-5")
	ig.API = &HTTPInstagramAPI{HTTP: srv.Client(), Base: srv.URL}
	res, err := ig.Publish(context.Background(), PublishRequest{
		PublicationID: "pub-ig5", ContentID: "c-ig5", Account: "ig-biz-5",
		IdempotencyKey: "c-ig5:instagram:ig-biz-5", VideoPath: "renders/a.mp4", Description: "hi",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if res.ExternalID != "media-http" || publishes.Load() != 1 {
		t.Fatalf("res=%+v publishes=%d", res, publishes.Load())
	}
}

func TestInstagram_DailyCap(t *testing.T) {
	ig := seedIGPub(t, "c-ig8", "ig-biz-8", "pub-ig8", "c-ig8:instagram:ig-biz-8")
	api := ig.API.(*fakeIGAPI)
	ws := ig.now().UTC().Truncate(24 * time.Hour).Format(time.RFC3339Nano)
	mustExec(t, ig.DB, `INSERT INTO quotas (provider, window_start, used, "limit") VALUES (?, ?, ?, ?)`,
		"instagram:ig-biz-8", ws, igDailyCap, igDailyCap)
	_, err := ig.Publish(context.Background(), PublishRequest{
		PublicationID: "pub-ig8", ContentID: "c-ig8", Account: "ig-biz-8",
		IdempotencyKey: "c-ig8:instagram:ig-biz-8", VideoPath: "k",
	})
	if err == nil || !strings.Contains(err.Error(), "daily cap exceeded") {
		t.Fatalf("want daily cap exceeded, got %v", err)
	}
	if api.creates != 0 {
		t.Fatal("must not call Graph past daily cap")
	}
}

func TestInstagram_TokenNotInLastError(t *testing.T) {
	const secret = "EAAB-super-secret-token-xyz"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/media") && r.Method == http.MethodPost {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"bad access_token=` + secret + ` in request","code":190}}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	ig := seedIGPub(t, "c-ig7", "ig-biz-7", "pub-ig7", "c-ig7:instagram:ig-biz-7")
	ig.Tokens = func(_ context.Context, account string) (oauth2.TokenSource, error) {
		return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: secret}), nil
	}
	ig.API = &HTTPInstagramAPI{HTTP: srv.Client(), Base: srv.URL}
	_, err := ig.Publish(context.Background(), PublishRequest{
		PublicationID: "pub-ig7", ContentID: "c-ig7", Account: "ig-biz-7",
		IdempotencyKey: "c-ig7:instagram:ig-biz-7", VideoPath: "renders/a.mp4",
	})
	if err == nil {
		t.Fatal("want graph error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("token leaked in returned error: %v", err)
	}
	var stored string
	if qerr := ig.DB.QueryRow(`SELECT COALESCE(error,'') FROM publications WHERE id=?`, "pub-ig7").Scan(&stored); qerr != nil {
		t.Fatalf("read last_error: %v", qerr)
	}
	if strings.Contains(stored, secret) {
		t.Fatalf("token leaked in publications.error: %q", stored)
	}
	if !strings.Contains(stored, "access_token=[redacted]") && !strings.Contains(stored, "reauth needed") {
		t.Fatalf("want redacted graph error in publications.error, got %q", stored)
	}
}
