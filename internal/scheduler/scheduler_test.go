package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mayank2/internal/db"
	"mayank2/internal/queue"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	sqlDB, err := db.Open(ctx, filepath.Join(t.TempDir(), "sched.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := db.Migrate(ctx, sqlDB); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	return sqlDB
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

type fakeNotify struct {
	mu   sync.Mutex
	msgs []string
}

func (n *fakeNotify) Notify(ctx context.Context, text string) error {
	_ = ctx
	n.mu.Lock()
	defer n.mu.Unlock()
	n.msgs = append(n.msgs, text)
	return nil
}

// registerFakeAnalyticsPull stands in for internal/analytics.Service's real
// "analytics.pull" registration (M2-116: scheduler.RegisterHandlers no
// longer registers a placeholder for this type itself, since cmd/mayank2's
// run.go is now the single place that wires the real handler — see
// handlers.go's RegisterHandlers doc comment). The scheduler's own cron
// trigger for JobAnalyticsPull still needs *something* registered so
// Enqueue/catch-up succeed in these package-local tests.
func registerFakeAnalyticsPull(q *queue.Queue) {
	q.Register(JobAnalyticsPull, queue.ResourceNet, 5, func(context.Context, queue.Job) (json.RawMessage, error) {
		return nil, nil
	})
}

func TestParseHHMMAndCronSpec(t *testing.T) {
	h, m, err := parseHHMM("22:30")
	if err != nil || h != 22 || m != 30 {
		t.Fatalf("parseHHMM: %d:%d %v", h, m, err)
	}
	spec, err := dailyCronSpec("22:30")
	if err != nil || spec != "30 22 * * *" {
		t.Fatalf("spec=%q %v", spec, err)
	}
	if _, _, err := parseHHMM("24:00"); err == nil {
		t.Fatal("want error")
	}
}

func TestLastScheduledAt(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 28, 23, 0, 0, 0, loc)
	got := lastScheduledAt(now, loc, 22, 30)
	want := time.Date(2026, 9, 28, 22, 30, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	now = time.Date(2026, 9, 28, 21, 0, 0, 0, loc)
	got = lastScheduledAt(now, loc, 22, 30)
	want = time.Date(2026, 9, 27, 22, 30, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestCatchUp_firstBootNoEnqueue(t *testing.T) {
	sqlDB := testDB(t)
	clock := &fakeClock{now: time.Date(2026, 9, 28, 23, 0, 0, 0, time.UTC)}
	q := queue.New(sqlDB, queue.WithClock(clock.Now))
	s, err := New(sqlDB, q, Config{Location: time.UTC, DailySummaryAt: "22:30", DataDir: t.TempDir()}, WithClock(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	s.RegisterHandlers()
	registerFakeAnalyticsPull(q)
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	var n int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("first boot enqueued %d", n)
	}
}

func TestCatchUp_missedOnce(t *testing.T) {
	sqlDB := testDB(t)
	loc := time.UTC
	old := time.Date(2026, 9, 26, 22, 30, 0, 0, loc)
	mustExec(t, sqlDB, `INSERT INTO settings (key, value) VALUES (?, ?)`,
		lastFireKey(JobSummaryDaily), old.UTC().Format(time.RFC3339Nano))
	now := time.Date(2026, 9, 28, 23, 0, 0, 0, loc)
	for _, jt := range []struct {
		name string
		h, m int
	}{
		{JobStorageCleanup, 3, 0},
		{JobScoutTopics, 6, 0},
		{JobAnalyticsPull, 7, 0},
	} {
		slot := previousFire(now, jt.h, jt.m, loc)
		mustExec(t, sqlDB, `INSERT INTO settings (key, value) VALUES (?, ?)`,
			lastFireKey(jt.name), slot.UTC().Format(time.RFC3339Nano))
	}
	clock := &fakeClock{now: now}
	q := queue.New(sqlDB, queue.WithClock(clock.Now))
	s, err := New(sqlDB, q, Config{Location: loc, DailySummaryAt: "22:30", DataDir: t.TempDir()}, WithClock(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	s.RegisterHandlers()
	registerFakeAnalyticsPull(q)
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	var n int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type=?`, JobSummaryDaily).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("catch-up=%d want 1", n)
	}
	var payload string
	_ = sqlDB.QueryRow(`SELECT payload FROM jobs WHERE type=?`, JobSummaryDaily).Scan(&payload)
	if !strings.Contains(payload, "catchup") {
		t.Fatalf("payload=%s", payload)
	}
}

func TestCronPathOnlyEnqueues(t *testing.T) {
	sqlDB := testDB(t)
	loc := time.UTC
	clock := &fakeClock{now: time.Date(2026, 9, 28, 22, 30, 0, 0, loc)}
	q := queue.New(sqlDB, queue.WithClock(clock.Now))
	s, err := New(sqlDB, q, Config{Location: loc, DailySummaryAt: "22:30", DataDir: t.TempDir()}, WithClock(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	s.RegisterHandlers()
	// Watermark before today's slot so cron path enqueues (no Start/catch-up).
	if err := s.setLastFire(context.Background(), JobSummaryDaily,
		time.Date(2026, 9, 27, 22, 30, 0, 0, loc)); err != nil {
		t.Fatal(err)
	}
	s.onCronFire(JobSummaryDaily, "22:30")
	var n int
	_ = sqlDB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type=?`, JobSummaryDaily).Scan(&n)
	if n != 1 {
		t.Fatalf("cron jobs=%d", n)
	}
	var payload string
	_ = sqlDB.QueryRow(`SELECT payload FROM jobs WHERE type=?`, JobSummaryDaily).Scan(&payload)
	if !strings.Contains(payload, "cron") {
		t.Fatalf("payload=%s", payload)
	}
	s.onCronFire(JobSummaryDaily, "22:30")
	_ = sqlDB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type=?`, JobSummaryDaily).Scan(&n)
	if n != 1 {
		t.Fatalf("dedup failed %d", n)
	}
}

func TestBuildDailySummary_sections(t *testing.T) {
	sqlDB := testDB(t)
	clock := &fakeClock{now: time.Date(2026, 9, 28, 17, 0, 0, 0, time.UTC)}
	q := queue.New(sqlDB, queue.WithClock(clock.Now))
	n := &fakeNotify{}
	s, err := New(sqlDB, q, Config{Location: time.UTC, DailySummaryAt: "22:30", DataDir: t.TempDir()},
		WithClock(clock.Now), WithNotifier(n), WithFreeDisk(func(string) (uint64, error) { return 150 << 30, nil }))
	if err != nil {
		t.Fatal(err)
	}
	s.RegisterHandlers()
	mustExec(t, sqlDB, `INSERT INTO channels (id, platform, language, niche, status) VALUES ('yt-ai-en','youtube','en','ai','active')`)
	mustExec(t, sqlDB, `INSERT INTO content_items (id, channel_id, kind, language, created_at) VALUES ('c1','yt-ai-en','short','en',?)`, clock.Now().Format(time.RFC3339Nano))
	mustExec(t, sqlDB, `INSERT INTO publications (id, content_id, platform, account, status, idempotency_key, published_at)
VALUES ('p1','c1','youtube','yt-ai-en','published','k1',?)`, clock.Now().Add(-time.Hour).Format(time.RFC3339Nano))
	mustExec(t, sqlDB, `INSERT INTO approvals (id, content_id, kind, summary, status, nonce) VALUES ('a1','c1','video','x','pending','n1')`)
	mustExec(t, sqlDB, `INSERT INTO jobs (id, type, status, resource, priority, payload, run_at, attempts, max_attempts, created_at, updated_at)
VALUES ('j1','x','dead','light',0,'{}',?,0,3,?,?)`,
		clock.Now().Format(time.RFC3339Nano),
		clock.Now().Format(time.RFC3339Nano), clock.Now().Format(time.RFC3339Nano))
	mustExec(t, sqlDB, `INSERT INTO quotas (provider, window_start, used, "limit") VALUES ('gemini','2026-09-28',5,15)`)
	mustExec(t, sqlDB, `INSERT INTO metrics (publication_id, captured_at, views) VALUES ('p1',?,42)`, clock.Now().Format(time.RFC3339Nano))

	res, err := s.handleSummaryDaily(context.Background(), queue.Job{Type: JobSummaryDaily})
	if err != nil {
		t.Fatal(err)
	}
	if len(n.msgs) != 1 {
		t.Fatalf("notify=%d", len(n.msgs))
	}
	msg := n.msgs[0]
	for _, want := range []string{
		"Published (24h)", "yt-ai-en: 1", "Pending approvals: 1", "Failures (24h): 1",
		"Best performer (24h)", "42 views", "Free-tier quota", "Disk free", "Claude limit: ok",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("missing %q in:\n%s", want, msg)
		}
	}
	var out map[string]any
	if err := json.Unmarshal(res, &out); err != nil {
		t.Fatal(err)
	}
	if out["sent"] != true {
		t.Fatalf("result=%v", out)
	}
}

func TestCleanup_loadsDueRows(t *testing.T) {
	sqlDB := testDB(t)
	dataDir := t.TempDir()
	clock := &fakeClock{now: time.Date(2026, 9, 28, 4, 0, 0, 0, time.UTC)}
	q := queue.New(sqlDB, queue.WithClock(clock.Now))
	s, err := New(sqlDB, q, Config{Location: time.UTC, DailySummaryAt: "22:30", DataDir: dataDir},
		WithClock(clock.Now), WithFreeDisk(func(string) (uint64, error) { return 200 << 30, nil }))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, sqlDB, `INSERT INTO channels (id, platform, language, niche, status) VALUES ('ch','youtube','en','ai','active')`)
	mustExec(t, sqlDB, `INSERT INTO content_items (id, channel_id, kind, language, created_at) VALUES ('c1','ch','short','en',?)`, clock.Now().Format(time.RFC3339Nano))
	due := clock.Now().Add(-time.Hour).Format(time.RFC3339Nano)
	future := clock.Now().Add(24 * time.Hour).Format(time.RFC3339Nano)
	mediaPath := filepath.Join(dataDir, "media", "published", "gone.mp4")
	mustExec(t, sqlDB, `INSERT INTO assets (id, content_id, kind, path, delete_after) VALUES ('a1','c1','render',?,?)`, mediaPath, due)
	mustExec(t, sqlDB, `INSERT INTO assets (id, content_id, kind, path, delete_after) VALUES ('a2','c1','render',?,?)`, mediaPath+".x", future)
	items, err := s.loadDueAssets(context.Background(), clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "a1" {
		t.Fatalf("due=%v", items)
	}
	res, err := s.handleStorageCleanup(context.Background(), queue.Job{Type: JobStorageCleanup})
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(res, &out); err != nil {
		t.Fatal(err)
	}
	if int(out["deleted_local"].(float64)) != 1 {
		t.Fatalf("result=%v", out)
	}
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec: %v", err)
	}
}
