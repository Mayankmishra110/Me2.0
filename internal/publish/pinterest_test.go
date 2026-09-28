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

	"golang.org/x/oauth2"

	"mayank2/internal/content"
)

type fakePinPresign struct{}

func (f *fakePinPresign) PresignGET(ctx context.Context, key string, expiry time.Duration) (string, time.Duration, error) {
	return "https://r2.example/" + key, expiry, nil
}

type fakePinAPI struct {
	registers, creates int
	lastDesc           string
	lastLink           string
}

func (f *fakePinAPI) RegisterMedia(ctx context.Context, tok *oauth2.Token) (string, string, map[string]string, error) {
	f.registers++
	return "media-1", "https://upload.example/", map[string]string{"key": "k"}, nil
}
func (f *fakePinAPI) UploadMediaFile(ctx context.Context, uploadURL string, params map[string]string, filename string, data []byte) error {
	return nil
}
func (f *fakePinAPI) MediaStatus(ctx context.Context, tok *oauth2.Token, mediaID string) (string, error) {
	return "succeeded", nil
}
func (f *fakePinAPI) CreatePin(ctx context.Context, tok *oauth2.Token, boardID, title, description, link, mediaID, coverURL string) (string, string, error) {
	f.creates++
	f.lastDesc = description
	f.lastLink = link
	return "pin-1", "https://www.pinterest.com/pin/pin-1/", nil
}

func seedPin(t *testing.T, contentID, account, pubID, key string) (*Pinterest, string) {
	t.Helper()
	sqlDB := openPubDB(t)
	seedApproved(t, sqlDB, contentID, "ch-"+contentID)
	mustExec(t, sqlDB, `
INSERT INTO publications (id, content_id, platform, account, scheduled_at, status, idempotency_key)
VALUES (?, ?, 'pinterest', ?, ?, 'scheduled', ?)`,
		pubID, contentID, account, time.Now().UTC().Format(time.RFC3339Nano), key)
	video := filepath.Join(t.TempDir(), "short.mp4")
	if err := os.WriteFile(video, []byte("fake-video"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &Pinterest{
		DB: sqlDB, Tokens: staticToken, API: &fakePinAPI{}, Presign: &fakePinPresign{},
		Now:          func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) },
		PollInterval: time.Millisecond,
		PollTimeout:  40 * time.Millisecond,
	}, video
}

func TestPinterest_PublishHappyPath(t *testing.T) {
	pin, video := seedPin(t, "c-p1", "pin-acct", "pub-p1", "c-p1:pinterest:pin-acct")
	api := pin.API.(*fakePinAPI)
	res, err := pin.Publish(context.Background(), PublishRequest{
		PublicationID: "pub-p1", ContentID: "c-p1", Account: "pin-acct",
		IdempotencyKey: "c-p1:pinterest:pin-acct", VideoPath: video,
		ThumbnailPath: "thumbs/a.jpg", PlaylistID: "board-1",
		Title: "T", Description: "D", Tags: []string{"https://example.com/product"},
		PaidPromotion: true,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if res.ExternalID != "pin-1" || api.registers != 1 || api.creates != 1 {
		t.Fatalf("res=%+v api=%+v", res, api)
	}
	if !strings.Contains(api.lastDesc, pinAffiliateDisclosure) {
		t.Fatalf("missing affiliate disclosure: %q", api.lastDesc)
	}
	if api.lastLink != "https://example.com/product" {
		t.Fatalf("link=%q", api.lastLink)
	}
}

func TestPinterest_Idempotent(t *testing.T) {
	pin, video := seedPin(t, "c-p2", "pin-acct-2", "pub-p2", "c-p2:pinterest:pin-acct-2")
	api := pin.API.(*fakePinAPI)
	req := PublishRequest{
		PublicationID: "pub-p2", ContentID: "c-p2", Account: "pin-acct-2",
		IdempotencyKey: "c-p2:pinterest:pin-acct-2", VideoPath: video,
		ThumbnailPath: "t.jpg", PlaylistID: "board-1", Description: "d",
	}
	if _, err := pin.Publish(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := pin.Publish(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if api.registers != 1 {
		t.Fatalf("want 1 register, got %d", api.registers)
	}
}

func TestPinterest_MissingApproval(t *testing.T) {
	sqlDB := openPubDB(t)
	warmup := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339Nano)
	mustExec(t, sqlDB, `
INSERT INTO channels (id, platform, handle, language, niche, account_ref, status, warmup_started_at)
VALUES ('ch-p', 'pinterest', 'h', 'en', 'money', 'pa', 'active', ?)`, warmup)
	mustExec(t, sqlDB, `
INSERT INTO content_items (id, channel_id, kind, language, stage, created_at)
VALUES ('c-no', 'ch-p', 'short', 'en', 'rendered', ?)`, time.Now().UTC().Format(time.RFC3339Nano))
	video := filepath.Join(t.TempDir(), "v.mp4")
	_ = os.WriteFile(video, []byte("v"), 0o644)
	pin := &Pinterest{DB: sqlDB, Tokens: staticToken, API: &fakePinAPI{}, Presign: &fakePinPresign{}}
	_, err := pin.Publish(context.Background(), PublishRequest{
		ContentID: "c-no", Account: "pa", IdempotencyKey: "k", VideoPath: video,
		ThumbnailPath: "t", PlaylistID: "b",
	})
	if !errors.Is(err, content.ErrNotApproved) {
		t.Fatalf("want ErrNotApproved, got %v", err)
	}
}

func TestPinterest_AffiliateDisclosure(t *testing.T) {
	pin, video := seedPin(t, "c-p3", "pin-acct-3", "pub-p3", "c-p3:pinterest:pin-acct-3")
	api := pin.API.(*fakePinAPI)
	_, err := pin.Publish(context.Background(), PublishRequest{
		PublicationID: "pub-p3", ContentID: "c-p3", Account: "pin-acct-3",
		IdempotencyKey: "c-p3:pinterest:pin-acct-3", VideoPath: video,
		ThumbnailPath: "t.jpg", PlaylistID: "board-1", Description: "buy this",
		Tags: []string{"https://example.com/a"}, PaidPromotion: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(api.lastDesc, pinAffiliateDisclosure) {
		t.Fatalf("disclosure missing: %q", api.lastDesc)
	}
}

func TestPinterest_DuplicateDestinationSameDay(t *testing.T) {
	pin, video := seedPin(t, "c-p4", "pin-acct-4", "pub-p4", "c-p4:pinterest:pin-acct-4")
	link := "https://example.com/same"
	req1 := PublishRequest{
		PublicationID: "pub-p4", ContentID: "c-p4", Account: "pin-acct-4",
		IdempotencyKey: "c-p4:pinterest:pin-acct-4", VideoPath: video,
		ThumbnailPath: "t.jpg", PlaylistID: "board-1", Description: "d",
		Tags: []string{link},
	}
	if _, err := pin.Publish(context.Background(), req1); err != nil {
		t.Fatalf("first: %v", err)
	}
	// Second content, same destination, same day.
	seedApproved(t, pin.DB, "c-p4b", "ch-c-p4b")
	mustExec(t, pin.DB, `
INSERT INTO publications (id, content_id, platform, account, scheduled_at, status, idempotency_key)
VALUES ('pub-p4b', 'c-p4b', 'pinterest', 'pin-acct-4', ?, 'scheduled', 'c-p4b:pinterest:pin-acct-4')`,
		time.Now().UTC().Format(time.RFC3339Nano))
	_, err := pin.Publish(context.Background(), PublishRequest{
		PublicationID: "pub-p4b", ContentID: "c-p4b", Account: "pin-acct-4",
		IdempotencyKey: "c-p4b:pinterest:pin-acct-4", VideoPath: video,
		ThumbnailPath: "t.jpg", PlaylistID: "board-1", Description: "d2",
		Tags: []string{link},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate destination") {
		t.Fatalf("want duplicate destination, got %v", err)
	}
}

func TestPinterest_DailyCap(t *testing.T) {
	pin, video := seedPin(t, "c-p-cap", "pin-cap", "pub-p-cap", "c-p-cap:pinterest:pin-cap")
	api := pin.API.(*fakePinAPI)
	ws := pin.Now().UTC().Truncate(24 * time.Hour).Format(time.RFC3339Nano)
	mustExec(t, pin.DB, `
INSERT INTO quotas (provider, window_start, used, "limit")
VALUES (?, ?, 5, 5)`, "pinterest:pin-cap", ws)
	_, err := pin.Publish(context.Background(), PublishRequest{
		PublicationID: "pub-p-cap", ContentID: "c-p-cap", Account: "pin-cap",
		IdempotencyKey: "c-p-cap:pinterest:pin-cap", VideoPath: video,
		ThumbnailPath: "t.jpg", PlaylistID: "board-1", Description: "d",
	})
	if err == nil || !strings.Contains(err.Error(), "daily cap exceeded") {
		t.Fatalf("want daily cap, got %v", err)
	}
	if api.registers != 0 || api.creates != 0 {
		t.Fatal("must not call Pinterest API past daily cap")
	}
}

func TestPinterest_RejectsCloaker(t *testing.T) {
	pin, video := seedPin(t, "c-p-cl", "pin-cl", "pub-p-cl", "c-p-cl:pinterest:pin-cl")
	api := pin.API.(*fakePinAPI)
	_, err := pin.Publish(context.Background(), PublishRequest{
		PublicationID: "pub-p-cl", ContentID: "c-p-cl", Account: "pin-cl",
		IdempotencyKey: "c-p-cl:pinterest:pin-cl", VideoPath: video,
		ThumbnailPath: "t.jpg", PlaylistID: "board-1", Description: "d",
		Tags: []string{"https://bit.ly/abc123"},
	})
	if err == nil || !strings.Contains(err.Error(), "cloakers") {
		t.Fatalf("want cloaker rejection, got %v", err)
	}
	if api.registers != 0 {
		t.Fatal("must not call Pinterest API for cloaked links")
	}
}

func TestPinterest_HTTPHappyPath(t *testing.T) {
	var creates atomic.Int32
	uploadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	t.Cleanup(uploadSrv.Close)

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/media" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"media_id":"m-http","upload_url":"` + uploadSrv.URL + `","upload_parameters":{"key":"k"}}`))
		case strings.HasPrefix(r.URL.Path, "/media/") && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"status":"succeeded"}`))
		case r.URL.Path == "/pins" && r.Method == http.MethodPost:
			creates.Add(1)
			_, _ = w.Write([]byte(`{"id":"pin-http"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(apiSrv.Close)

	pin, video := seedPin(t, "c-p5", "pin-acct-5", "pub-p5", "c-p5:pinterest:pin-acct-5")
	pin.API = &HTTPPinterestAPI{HTTP: apiSrv.Client(), Base: apiSrv.URL}
	res, err := pin.Publish(context.Background(), PublishRequest{
		PublicationID: "pub-p5", ContentID: "c-p5", Account: "pin-acct-5",
		IdempotencyKey: "c-p5:pinterest:pin-acct-5", VideoPath: video,
		ThumbnailPath: "cover.jpg", PlaylistID: "board-1", Title: "t", Description: "d",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if res.ExternalID != "pin-http" || creates.Load() != 1 {
		t.Fatalf("res=%+v creates=%d", res, creates.Load())
	}
}
