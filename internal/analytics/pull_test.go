package analytics

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"mayank2/internal/db"
	"mayank2/internal/queue"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	sqlDB, err := db.Open(ctx, filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := db.Migrate(ctx, sqlDB); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return sqlDB
}

type fakeAPI struct {
	lastVideo  string
	lastWindow string
	m          VideoMetrics
	err        error
}

func (f *fakeAPI) PullVideo(ctx context.Context, tok *oauth2.Token, videoID, window string) (VideoMetrics, error) {
	f.lastVideo = videoID
	f.lastWindow = window
	if f.err != nil {
		return VideoMetrics{}, f.err
	}
	if tok == nil || tok.AccessToken == "" {
		return VideoMetrics{}, context.Canceled
	}
	return f.m, nil
}

func staticTok(_ context.Context, account string) (oauth2.TokenSource, error) {
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "tok-" + account}), nil
}

func seedPublished(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	must(t, sqlDB, `INSERT INTO channels (id, platform, language, niche, status) VALUES ('yt-money-en','youtube','en','money','active')`)
	must(t, sqlDB, `INSERT INTO topics (id, channel_id, title, source, score, signals, status, created_at)
VALUES ('top1','yt-money-en','Index Funds Explained','scout',0.5,'{}','used',?)`, time.Now().UTC().Format(time.RFC3339Nano))
	script, _ := json.Marshal(map[string]any{"hook_style": "myth_vs_fact", "hook": "x"})
	must(t, sqlDB, `INSERT INTO content_items (id, topic_id, channel_id, kind, format, language, stage, script, created_at)
VALUES ('c1','top1','yt-money-en','short','myth_vs_fact','en','published',?,?)`,
		string(script), time.Now().UTC().Format(time.RFC3339Nano))
	must(t, sqlDB, `INSERT INTO publications (id, content_id, platform, account, scheduled_at, status, external_id, url, idempotency_key, published_at)
VALUES ('pub1','c1','youtube','yt-money-en',?,'published','vidABC','https://youtu.be/vidABC','k1',?)`,
		time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		time.Now().UTC().Format(time.RFC3339Nano))
}

func must(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec: %v\n%s", err, q)
	}
}

func TestPull_writesMetricsAndScores(t *testing.T) {
	sqlDB := openDB(t)
	seedPublished(t, sqlDB)
	api := &fakeAPI{m: VideoMetrics{
		Views: 2000, WatchSeconds: 5000, AvgViewPct: 45, Likes: 10,
		Comments: 2, Shares: 20, SubsGained: 5, CTR: 0.04,
	}}
	svc := &Service{DB: sqlDB, Tokens: staticTok, API: api, Now: func() time.Time {
		return time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	}}
	if err := svc.Pull(context.Background(), PullRequest{PublicationID: "pub1", Window: Window24h}); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if api.lastVideo != "vidABC" || api.lastWindow != Window24h {
		t.Fatalf("api last=%s/%s", api.lastVideo, api.lastWindow)
	}
	var views int64
	if err := sqlDB.QueryRow(`SELECT views FROM metrics WHERE publication_id='pub1'`).Scan(&views); err != nil || views != 2000 {
		t.Fatalf("metrics views=%d err=%v", views, err)
	}
	for _, scope := range []string{ScopeTopic, ScopeFormat, ScopeHook, ScopeTimeSlot} {
		var n int
		_ = sqlDB.QueryRow(`SELECT COUNT(*) FROM scores WHERE scope=? AND channel_id='yt-money-en'`, scope).Scan(&n)
		if n != 1 {
			t.Fatalf("scope %s count=%d", scope, n)
		}
	}
	var hookKey string
	_ = sqlDB.QueryRow(`SELECT key FROM scores WHERE scope='hook'`).Scan(&hookKey)
	if hookKey != "myth_vs_fact" {
		t.Fatalf("hook key=%q", hookKey)
	}
	var slot string
	_ = sqlDB.QueryRow(`SELECT key FROM scores WHERE scope='time_slot'`).Scan(&slot)
	if slot != "12-15" {
		t.Fatalf("slot=%q want 12-15", slot)
	}
}

func TestPull_requiresPublished(t *testing.T) {
	sqlDB := openDB(t)
	must(t, sqlDB, `INSERT INTO channels (id, platform, language, niche, status) VALUES ('yt-ai-en','youtube','en','ai','active')`)
	must(t, sqlDB, `INSERT INTO content_items (id, channel_id, kind, language, stage, created_at)
VALUES ('c2','yt-ai-en','short','en','approved',?)`, time.Now().UTC().Format(time.RFC3339Nano))
	must(t, sqlDB, `INSERT INTO publications (id, content_id, platform, account, status, idempotency_key)
VALUES ('pub2','c2','youtube','yt-ai-en','scheduled','k2')`)
	svc := &Service{DB: sqlDB, Tokens: staticTok, API: &fakeAPI{}}
	err := svc.Pull(context.Background(), PullRequest{PublicationID: "pub2", Window: "24h"})
	if err == nil || err.Error() == "" {
		t.Fatal("expected error")
	}
}

func TestQueryChannelMetrics(t *testing.T) {
	sqlDB := openDB(t)
	seedPublished(t, sqlDB)
	svc := &Service{DB: sqlDB, Tokens: staticTok, API: &fakeAPI{m: VideoMetrics{Views: 100, AvgViewPct: 30, CTR: 0.02}}}
	_ = svc.Pull(context.Background(), PullRequest{PublicationID: "pub1", Window: "7d"})
	rep, err := QueryChannelMetrics(context.Background(), sqlDB, "yt-money-en", "7d")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rep.Points) != 1 || rep.Points[0].Views != 100 {
		t.Fatalf("points=%+v", rep.Points)
	}
	if len(rep.Scores) < 4 {
		t.Fatalf("scores=%v", rep.Scores)
	}
}

func TestEnqueuePulls(t *testing.T) {
	sqlDB := openDB(t)
	q := queue.New(sqlDB, queue.WithWorkerName("an-test"))
	svc := &Service{DB: sqlDB, Tokens: staticTok, API: &fakeAPI{}}
	svc.RegisterHandler(q)
	pubAt := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	if err := EnqueuePulls(context.Background(), q, "pub1", "c1", pubAt); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	var n int
	_ = sqlDB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type='analytics.pull'`).Scan(&n)
	if n != 2 {
		t.Fatalf("jobs=%d want 2", n)
	}
}

func TestScoreFromMetrics(t *testing.T) {
	s := ScoreFromMetrics(VideoMetrics{Views: 10000, AvgViewPct: 100, CTR: 0.10, SubsGained: 50, Shares: 100})
	if s < 0.99 {
		t.Fatalf("score=%v want ~1", s)
	}
	if ScoreFromMetrics(VideoMetrics{}) != 0 {
		t.Fatal("zero metrics should score 0")
	}
}

func TestParseAnalyticsReport(t *testing.T) {
	raw := []byte(`{
	  "columnHeaders":[
	    {"name":"video"},{"name":"views"},{"name":"estimatedMinutesWatched"},
	    {"name":"averageViewPercentage"},{"name":"likes"},{"name":"comments"},
	    {"name":"shares"},{"name":"subscribersGained"},{"name":"annotationClickThroughRate"}
	  ],
	  "rows":[["vid",150,10.5,42.0,3,1,2,4,0.03]]
	}`)
	m, err := parseAnalyticsReport(raw)
	if err != nil {
		t.Fatal(err)
	}
	if m.Views != 150 || m.WatchSeconds != 10.5*60 || m.AvgViewPct != 42 || m.CTR != 0.03 {
		t.Fatalf("%+v", m)
	}
}

func TestHandleJob(t *testing.T) {
	sqlDB := openDB(t)
	seedPublished(t, sqlDB)
	svc := &Service{DB: sqlDB, Tokens: staticTok, API: &fakeAPI{m: VideoMetrics{Views: 9}}}
	payload, _ := json.Marshal(pullPayload{PublicationID: "pub1", Window: "24h"})
	if _, err := svc.Handle(context.Background(), queue.Job{Type: JobAnalyticsPull, Payload: payload}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
}
