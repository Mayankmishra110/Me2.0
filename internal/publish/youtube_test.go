package publish

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"mayank2/internal/db"
	"mayank2/internal/queue"
)

func openPubDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	sqlDB, err := db.Open(ctx, filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := db.Migrate(ctx, sqlDB); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return sqlDB
}

type fakeYT struct {
	uploads    int
	thumbs     int
	captions   int
	playlists  int
	lastMeta   VideoMeta
	failUpload bool
}

func (f *fakeYT) ResumableUpload(ctx context.Context, tok *oauth2.Token, meta VideoMeta, video io.Reader, size int64) (string, error) {
	f.uploads++
	f.lastMeta = meta
	_, _ = io.Copy(io.Discard, video)
	if f.failUpload {
		return "", context.DeadlineExceeded
	}
	if tok == nil || tok.AccessToken == "" {
		return "", io.ErrUnexpectedEOF
	}
	return "vid-1", nil
}
func (f *fakeYT) SetThumbnail(ctx context.Context, tok *oauth2.Token, videoID string, thumb io.Reader, size int64) error {
	f.thumbs++
	_, _ = io.Copy(io.Discard, thumb)
	return nil
}
func (f *fakeYT) UploadCaption(ctx context.Context, tok *oauth2.Token, videoID, language string, srt io.Reader, size int64) error {
	f.captions++
	_, _ = io.Copy(io.Discard, srt)
	return nil
}
func (f *fakeYT) AddToPlaylist(ctx context.Context, tok *oauth2.Token, playlistID, videoID string) error {
	f.playlists++
	return nil
}

func staticToken(_ context.Context, account string) (oauth2.TokenSource, error) {
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-access-" + account}), nil
}

func seedApproved(t *testing.T, sqlDB *sql.DB, contentID, channelID string) {
	t.Helper()
	warmup := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339Nano)
	mustExec(t, sqlDB, `
INSERT INTO channels (id, platform, handle, language, niche, account_ref, status, warmup_started_at)
VALUES (?, 'youtube', 'h', 'en', 'money', ?, 'active', ?)`, channelID, channelID, warmup)
	mustExec(t, sqlDB, `
INSERT INTO content_items (id, channel_id, kind, language, stage, created_at)
VALUES (?, ?, 'short', 'en', 'approved', ?)`, contentID, channelID, time.Now().UTC().Format(time.RFC3339Nano))
	mustExec(t, sqlDB, `
INSERT INTO approvals (id, content_id, kind, summary, status, nonce, decided_at)
VALUES (?, ?, 'short', 'ok', 'approved', '', ?)`, "ap-"+contentID, contentID, time.Now().UTC().Format(time.RFC3339Nano))
}

func mustExec(t *testing.T, sqlDB *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := sqlDB.Exec(q, args...); err != nil {
		t.Fatalf("exec: %v\n%s", err, q)
	}
}

func writeTemp(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestYouTube_PublishHappyPath(t *testing.T) {
	sqlDB := openPubDB(t)
	seedApproved(t, sqlDB, "c1", "yt-money-en")
	mustExec(t, sqlDB, `
INSERT INTO publications (id, content_id, platform, account, scheduled_at, status, idempotency_key)
VALUES ('pub1', 'c1', 'youtube', 'yt-money-en', ?, 'scheduled', 'c1:youtube:yt-money-en')`,
		time.Now().UTC().Format(time.RFC3339Nano))

	video := writeTemp(t, "v.mp4", "fake-video-bytes")
	thumb := writeTemp(t, "t.jpg", "fake-thumb")
	srt := writeTemp(t, "c.srt", "1\n00:00:00,000 --> 00:00:01,000\nHi\n")

	api := &fakeYT{}
	yt := &YouTube{
		DB: sqlDB, Tokens: staticToken, API: api,
		Now:        func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) },
		QuotaLimit: 10000,
	}
	res, err := yt.Publish(context.Background(), PublishRequest{
		PublicationID:     "pub1",
		ContentID:         "c1",
		Account:           "yt-money-en",
		IdempotencyKey:    "c1:youtube:yt-money-en",
		VideoPath:         video,
		ThumbnailPath:     thumb,
		CaptionsPath:      srt,
		PlaylistID:        "PL123",
		Title:             "Test short",
		Description:       "desc",
		Tags:              []string{"money", "tips"},
		Language:          "en",
		ContainsSynthetic: true,
		PaidPromotion:     true,
		ScheduledAt:       time.Date(2026, 9, 28, 12, 30, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if res.ExternalID != "vid-1" || !strings.Contains(res.URL, "vid-1") {
		t.Fatalf("result=%+v", res)
	}
	if api.uploads != 1 || api.thumbs != 1 || api.captions != 1 || api.playlists != 1 {
		t.Fatalf("api calls upload=%d thumb=%d cap=%d pl=%d", api.uploads, api.thumbs, api.captions, api.playlists)
	}
	if api.lastMeta.MadeForKids {
		t.Fatal("made-for-kids must be false")
	}
	if !api.lastMeta.ContainsSynthetic || !api.lastMeta.PaidPromotion {
		t.Fatalf("disclosure flags meta=%+v", api.lastMeta)
	}
	if api.lastMeta.PublishAt == nil {
		t.Fatal("expected publishAt for future slot")
	}
	if api.lastMeta.PrivacyStatus != "private" {
		t.Fatalf("privacy=%q want private for scheduled", api.lastMeta.PrivacyStatus)
	}

	var st, ext string
	_ = sqlDB.QueryRow(`SELECT status, external_id FROM publications WHERE id='pub1'`).Scan(&st, &ext)
	if st != "published" || ext != "vid-1" {
		t.Fatalf("pub status=%q ext=%q", st, ext)
	}
	var used int
	_ = sqlDB.QueryRow(`SELECT used FROM quotas WHERE provider='youtube'`).Scan(&used)
	want := quotaCostVideosInsert + quotaCostThumbnailsSet + quotaCostCaptionsInsert + quotaCostPlaylistInsert
	if used != want {
		t.Fatalf("quota used=%d want %d", used, want)
	}
}

func TestYouTube_Idempotent(t *testing.T) {
	sqlDB := openPubDB(t)
	seedApproved(t, sqlDB, "c1", "yt-ai-en")
	mustExec(t, sqlDB, `
INSERT INTO publications (id, content_id, platform, account, status, external_id, url, idempotency_key, published_at)
VALUES ('pub1', 'c1', 'youtube', 'yt-ai-en', 'published', 'vid-old', 'https://www.youtube.com/watch?v=vid-old', 'key1', ?)`,
		time.Now().UTC().Format(time.RFC3339Nano))
	api := &fakeYT{}
	yt := &YouTube{DB: sqlDB, Tokens: staticToken, API: api}
	res, err := yt.Publish(context.Background(), PublishRequest{
		PublicationID: "pub1", ContentID: "c1", Account: "yt-ai-en", IdempotencyKey: "key1",
		VideoPath: writeTemp(t, "v.mp4", "x"),
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if res.ExternalID != "vid-old" || api.uploads != 0 {
		t.Fatalf("idempotent fail res=%+v uploads=%d", res, api.uploads)
	}
}

func TestYouTube_RequiresApproval(t *testing.T) {
	sqlDB := openPubDB(t)
	warmup := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339Nano)
	mustExec(t, sqlDB, `
INSERT INTO channels (id, platform, language, niche, status, warmup_started_at)
VALUES ('yt-ai-en', 'youtube', 'en', 'ai', 'active', ?)`, warmup)
	mustExec(t, sqlDB, `
INSERT INTO content_items (id, channel_id, kind, language, stage, created_at)
VALUES ('c1', 'yt-ai-en', 'short', 'en', 'rendered', ?)`, time.Now().UTC().Format(time.RFC3339Nano))
	yt := &YouTube{DB: sqlDB, Tokens: staticToken, API: &fakeYT{}}
	_, err := yt.Publish(context.Background(), PublishRequest{
		ContentID: "c1", Account: "yt-ai-en", IdempotencyKey: "k",
		VideoPath: writeTemp(t, "v.mp4", "x"),
	})
	if err == nil || !strings.Contains(err.Error(), "no approved approval") {
		t.Fatalf("want ErrNotApproved, got %v", err)
	}
}

func TestYouTube_QuotaBlocks(t *testing.T) {
	sqlDB := openPubDB(t)
	seedApproved(t, sqlDB, "c1", "yt-money-en")
	mustExec(t, sqlDB, `
INSERT INTO publications (id, content_id, platform, account, status, idempotency_key)
VALUES ('pub1', 'c1', 'youtube', 'yt-money-en', 'scheduled', 'k1')`)
	ws := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	mustExec(t, sqlDB, `INSERT INTO quotas (provider, window_start, used, "limit") VALUES ('youtube', ?, 9900, 10000)`, ws)
	api := &fakeYT{}
	yt := &YouTube{
		DB: sqlDB, Tokens: staticToken, API: api, QuotaLimit: 10000,
		Now: func() time.Time { return time.Date(2026, 9, 28, 15, 0, 0, 0, time.UTC) },
	}
	_, err := yt.Publish(context.Background(), PublishRequest{
		PublicationID: "pub1", ContentID: "c1", Account: "yt-money-en", IdempotencyKey: "k1",
		VideoPath: writeTemp(t, "v.mp4", "x"),
	})
	if err == nil || !strings.Contains(err.Error(), "quota") {
		t.Fatalf("want quota error, got %v", err)
	}
	if api.uploads != 0 {
		t.Fatal("must not upload when over quota")
	}
}

func TestYouTube_PublicWhenSlotNow(t *testing.T) {
	sqlDB := openPubDB(t)
	seedApproved(t, sqlDB, "c1", "yt-money-hi")
	api := &fakeYT{}
	yt := &YouTube{
		DB: sqlDB, Tokens: staticToken, API: api,
		Now: func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) },
	}
	_, err := yt.Publish(context.Background(), PublishRequest{
		ContentID: "c1", Account: "yt-money-hi", IdempotencyKey: "k-now",
		VideoPath:   writeTemp(t, "v.mp4", "x"),
		Title:       "now",
		ScheduledAt: time.Date(2026, 9, 28, 11, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if api.lastMeta.PrivacyStatus != "public" || api.lastMeta.PublishAt != nil {
		t.Fatalf("meta=%+v want public immediate", api.lastMeta)
	}
}

func TestYouTube_HandleJob(t *testing.T) {
	sqlDB := openPubDB(t)
	seedApproved(t, sqlDB, "c1", "yt-ai-hi")
	video := writeTemp(t, "v.mp4", "x")
	api := &fakeYT{}
	yt := &YouTube{DB: sqlDB, Tokens: staticToken, API: api}
	q := queue.New(sqlDB, queue.WithWorkerName("yt-test"))
	yt.RegisterHandler(q)
	payload, _ := json.Marshal(youtubeJobPayload{
		ContentID: "c1", Account: "yt-ai-hi", IdempotencyKey: "job-key",
		VideoPath: video, Title: "t", Language: "hi", ContainsSynthetic: true,
	})
	raw, err := yt.Handle(context.Background(), queue.Job{Type: JobPublishYouTube, Payload: payload})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	var res PublishResult
	if err := json.Unmarshal(raw, &res); err != nil || res.ExternalID != "vid-1" {
		t.Fatalf("result %s err=%v", raw, err)
	}
}

func TestYouTube_PathConfine(t *testing.T) {
	sqlDB := openPubDB(t)
	seedApproved(t, sqlDB, "c1", "yt-money-en")
	root := t.TempDir()
	outside := writeTemp(t, "outside.mp4", "x")
	yt := &YouTube{DB: sqlDB, Tokens: staticToken, API: &fakeYT{}, DataRoot: root}
	_, err := yt.Publish(context.Background(), PublishRequest{
		ContentID: "c1", Account: "yt-money-en", IdempotencyKey: "esc",
		VideoPath: outside,
	})
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("want path confine error, got %v", err)
	}
}

func TestYouTubeStatusJSON_fields(t *testing.T) {
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	st := youtubeStatusJSON(VideoMeta{
		PrivacyStatus: "private", MadeForKids: false,
		ContainsSynthetic: true, PaidPromotion: true, PublishAt: &at,
	})
	raw, _ := json.Marshal(st)
	if !bytes.Contains(raw, []byte(`"containsSyntheticMedia":true`)) {
		t.Fatalf("missing synthetic field: %s", raw)
	}
	if !bytes.Contains(raw, []byte(`"selfDeclaredMadeForKids":false`)) {
		t.Fatalf("missing madeForKids: %s", raw)
	}
	if !bytes.Contains(raw, []byte(`"containsPaidProductPlacement":true`)) {
		t.Fatalf("missing paid promo: %s", raw)
	}
	if !bytes.Contains(raw, []byte(`"publishAt"`)) {
		t.Fatalf("missing publishAt: %s", raw)
	}
}
