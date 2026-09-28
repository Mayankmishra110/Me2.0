package publish

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"mayank2/internal/content"
)

type fakeFBPresign struct{}

func (f *fakeFBPresign) PresignGET(ctx context.Context, key string, expiry time.Duration) (string, time.Duration, error) {
	return "https://r2.example/" + key, expiry, nil
}

type fakeFBAPI struct {
	starts, uploads, finishes int
	authFail                  bool
	genericFail               bool
	lastDesc                  string
}

func (f *fakeFBAPI) StartReel(ctx context.Context, tok *oauth2.Token, pageID string) (string, error) {
	f.starts++
	if f.authFail {
		return "", fbWrapGraph(190, "Invalid OAuth access token")
	}
	if f.genericFail {
		// Not an auth code: e.g. "temporarily blocked for policies violations" (code 368).
		return "", fbWrapGraph(368, "Temporarily blocked for policies violations")
	}
	return "vid-fb-1", nil
}
func (f *fakeFBAPI) UploadHosted(ctx context.Context, tok *oauth2.Token, videoID, fileURL string) error {
	f.uploads++
	return nil
}
func (f *fakeFBAPI) FinishReel(ctx context.Context, tok *oauth2.Token, pageID, videoID, title, description string) (string, error) {
	f.finishes++
	f.lastDesc = description
	return "post-fb-1", nil
}

func seedFB(t *testing.T, contentID, account, pubID, key string) *Facebook {
	t.Helper()
	sqlDB := openPubDB(t)
	seedApproved(t, sqlDB, contentID, "ch-"+contentID)
	mustExec(t, sqlDB, `
INSERT INTO publications (id, content_id, platform, account, scheduled_at, status, idempotency_key)
VALUES (?, ?, 'facebook', ?, ?, 'scheduled', ?)`,
		pubID, contentID, account, time.Now().UTC().Format(time.RFC3339Nano), key)
	return &Facebook{
		DB: sqlDB, Tokens: staticToken, API: &fakeFBAPI{}, Presign: &fakeFBPresign{},
		Now: func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) },
	}
}

func TestFacebook_PublishHappyPath(t *testing.T) {
	fb := seedFB(t, "c-fb1", "page-1", "pub-fb1", "c-fb1:facebook:page-1")
	api := fb.API.(*fakeFBAPI)
	res, err := fb.Publish(context.Background(), PublishRequest{
		PublicationID: "pub-fb1", ContentID: "c-fb1", Account: "page-1",
		IdempotencyKey: "c-fb1:facebook:page-1", VideoPath: "renders/a.mp4",
		Title: "T", Description: "D", PaidPromotion: true, ContainsSynthetic: true,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if res.ExternalID != "post-fb-1" || api.starts != 1 || api.uploads != 1 || api.finishes != 1 {
		t.Fatalf("res=%+v api=%+v", res, api)
	}
	if !strings.Contains(api.lastDesc, fbAffiliateDisclosure) || !strings.Contains(api.lastDesc, fbSyntheticDisclosure) {
		t.Fatalf("description missing disclosures: %q", api.lastDesc)
	}
}

func TestFacebook_Idempotent(t *testing.T) {
	fb := seedFB(t, "c-fb2", "page-2", "pub-fb2", "c-fb2:facebook:page-2")
	api := fb.API.(*fakeFBAPI)
	req := PublishRequest{
		PublicationID: "pub-fb2", ContentID: "c-fb2", Account: "page-2",
		IdempotencyKey: "c-fb2:facebook:page-2", VideoPath: "k",
	}
	if _, err := fb.Publish(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := fb.Publish(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if api.starts != 1 {
		t.Fatalf("want 1 start, got %d", api.starts)
	}
}

func TestFacebook_MissingApproval(t *testing.T) {
	sqlDB := openPubDB(t)
	warmup := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339Nano)
	mustExec(t, sqlDB, `
INSERT INTO channels (id, platform, handle, language, niche, account_ref, status, warmup_started_at)
VALUES ('ch-fb', 'facebook', 'h', 'en', 'money', 'p', 'active', ?)`, warmup)
	mustExec(t, sqlDB, `
INSERT INTO content_items (id, channel_id, kind, language, stage, created_at)
VALUES ('c-no', 'ch-fb', 'short', 'en', 'rendered', ?)`, time.Now().UTC().Format(time.RFC3339Nano))
	fb := &Facebook{DB: sqlDB, Tokens: staticToken, API: &fakeFBAPI{}, Presign: &fakeFBPresign{}}
	_, err := fb.Publish(context.Background(), PublishRequest{
		ContentID: "c-no", Account: "p", IdempotencyKey: "k", VideoPath: "v",
	})
	if !errors.Is(err, content.ErrNotApproved) {
		t.Fatalf("want ErrNotApproved, got %v", err)
	}
}

func TestFacebook_AuthFailureDistinct(t *testing.T) {
	fb := seedFB(t, "c-fb3", "page-3", "pub-fb3", "c-fb3:facebook:page-3")
	fb.API = &fakeFBAPI{authFail: true}
	_, err := fb.Publish(context.Background(), PublishRequest{
		PublicationID: "pub-fb3", ContentID: "c-fb3", Account: "page-3",
		IdempotencyKey: "c-fb3:facebook:page-3", VideoPath: "k",
	})
	if err == nil || !strings.Contains(err.Error(), "reauth needed") {
		t.Fatalf("want reauth needed, got %v", err)
	}
	if !errors.Is(err, ErrReauthNeeded) {
		t.Fatalf("want errors.Is(err, ErrReauthNeeded), got %v", err)
	}
	if errors.Is(err, ErrDailyCapExceeded) {
		t.Fatalf("auth failure must not also match ErrDailyCapExceeded: %v", err)
	}
}

// TestFacebook_GenericAPIErrorNotReauth proves the reauth/generic distinction
// goes both ways: a non-auth Graph error (e.g. a policy block) must NOT be
// classified as ErrReauthNeeded, so a caller doesn't wrongly prompt Mayank to
// re-link the Page over an unrelated failure.
func TestFacebook_GenericAPIErrorNotReauth(t *testing.T) {
	fb := seedFB(t, "c-fb5", "page-5", "pub-fb5", "c-fb5:facebook:page-5")
	fb.API = &fakeFBAPI{genericFail: true}
	_, err := fb.Publish(context.Background(), PublishRequest{
		PublicationID: "pub-fb5", ContentID: "c-fb5", Account: "page-5",
		IdempotencyKey: "c-fb5:facebook:page-5", VideoPath: "k",
	})
	if err == nil {
		t.Fatal("want error")
	}
	if errors.Is(err, ErrReauthNeeded) {
		t.Fatalf("generic API error must not match ErrReauthNeeded: %v", err)
	}
	if errors.Is(err, ErrDailyCapExceeded) {
		t.Fatalf("generic API error must not match ErrDailyCapExceeded: %v", err)
	}
	if !strings.Contains(err.Error(), "graph code=368") {
		t.Fatalf("want graph code in error, got %v", err)
	}
}

func TestFacebook_DailyCap(t *testing.T) {
	fb := seedFB(t, "c-fb-cap", "page-cap", "pub-fb-cap", "c-fb-cap:facebook:page-cap")
	api := fb.API.(*fakeFBAPI)
	for i := 0; i < fbDailyCap; i++ {
		key := fmt.Sprintf("c-fb-cap:facebook:page-cap:%d", i)
		mustExec(t, fb.DB, `
INSERT INTO publications (id, content_id, platform, account, scheduled_at, status, idempotency_key)
VALUES (?, 'c-fb-cap', 'facebook', 'page-cap', ?, 'scheduled', ?)`,
			fmt.Sprintf("pub-cap-%d", i), time.Now().UTC().Format(time.RFC3339Nano), key)
		_, err := fb.Publish(context.Background(), PublishRequest{
			PublicationID: fmt.Sprintf("pub-cap-%d", i), ContentID: "c-fb-cap", Account: "page-cap",
			IdempotencyKey: key, VideoPath: "k",
		})
		if err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}
	_, err := fb.Publish(context.Background(), PublishRequest{
		PublicationID: "pub-fb-cap", ContentID: "c-fb-cap", Account: "page-cap",
		IdempotencyKey: "c-fb-cap:facebook:page-cap", VideoPath: "k",
	})
	if err == nil || !strings.Contains(err.Error(), "daily cap exceeded") {
		t.Fatalf("want daily cap exceeded, got %v", err)
	}
	if !errors.Is(err, ErrDailyCapExceeded) {
		t.Fatalf("want errors.Is(err, ErrDailyCapExceeded), got %v", err)
	}
	if errors.Is(err, ErrReauthNeeded) {
		t.Fatalf("cap failure must not also match ErrReauthNeeded: %v", err)
	}
	if api.starts != fbDailyCap {
		t.Fatalf("want %d Graph starts, got %d", fbDailyCap, api.starts)
	}
}

func TestFacebook_RedactOAuthMessage(t *testing.T) {
	in := "graph code=190 message=Invalid OAuth access token"
	out := fbRedact(in)
	if !strings.Contains(out, "[redacted]") {
		t.Fatalf("expected redaction, got %q", out)
	}
	if strings.Contains(strings.ToLower(out), "access token") && !strings.Contains(out, "[redacted]") {
		t.Fatalf("token words not redacted: %q", out)
	}
}

func TestFacebook_HTTPHappyPath(t *testing.T) {
	var finishes atomic.Int32
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/video_reels") {
			_ = r.ParseForm()
			switch r.Form.Get("upload_phase") {
			case "start":
				_, _ = w.Write([]byte(`{"video_id":"vid-http"}`))
			case "finish":
				finishes.Add(1)
				_, _ = w.Write([]byte(`{"success":true,"post_id":"post-http"}`))
			default:
				http.Error(w, "bad phase", 400)
			}
			return
		}
		if strings.Contains(r.URL.Path, "/video-upload/") || strings.Contains(r.URL.Path, "vid-http") {
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"success":true}`))
			return
		}
		http.NotFound(w, r)
	})

	fb := seedFB(t, "c-fb4", "page-4", "pub-fb4", "c-fb4:facebook:page-4")
	fb.API = &HTTPFacebookAPI{HTTP: srv.Client(), GraphBase: srv.URL, UploadBase: srv.URL + "/video-upload"}
	res, err := fb.Publish(context.Background(), PublishRequest{
		PublicationID: "pub-fb4", ContentID: "c-fb4", Account: "page-4",
		IdempotencyKey: "c-fb4:facebook:page-4", VideoPath: "renders/a.mp4", Title: "t", Description: "d",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if res.ExternalID != "post-http" || finishes.Load() != 1 {
		t.Fatalf("res=%+v finishes=%d", res, finishes.Load())
	}
}
