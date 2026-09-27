package content_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mayank2/internal/content"
	"mayank2/internal/db"
)

const testYTKey = "yt-test-key-not-real"

func TestScoreFormula_table(t *testing.T) {
	// AC: score = demand × freshness × fit × (1 − competition)
	tests := []struct {
		demand, fresh, fit, comp, want float64
	}{
		{1, 1, 1, 0, 1},
		{1, 1, 1, 0.5, 0.5},
		{0, 1, 1, 0, 0},
		{0.8, 1.0, 0.7, 0.3, 0.8 * 1.0 * 0.7 * 0.7},
	}
	for _, tt := range tests {
		got := tt.demand * tt.fresh * tt.fit * (1 - tt.comp)
		if abs(got-tt.want) > 1e-12 {
			t.Fatalf("got %v want %v", got, tt.want)
		}
	}

	// Manual path applies DefaultScoutManualBoost to that base.
	ctx := context.Background()
	sqlDB, cleanup := openScoutDB(t)
	defer cleanup()
	seedChannel(t, sqlDB, "yt-money-en", "money_side_hustles")

	s, err := content.NewScout(sqlDB, content.ScoutOptions{
		YouTubeAPIKey: "x",
		Now:           func() time.Time { return time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC) },
		NewID:         seqID("man"),
		ManualBoost:   content.DefaultScoutManualBoost,
	})
	if err != nil {
		t.Fatal(err)
	}
	topic, err := s.AddManual(ctx, "yt-money-en", "Unique manual topic about budgeting", "")
	if err != nil {
		t.Fatalf("AddManual: %v", err)
	}
	base := 0.8 * 1.0 * 1.0 * (1 - 0.2) // AddManual defaults
	want := base * content.DefaultScoutManualBoost
	if abs(topic.Score-want) > 1e-9 {
		t.Fatalf("manual score=%v want %v", topic.Score, want)
	}
	if topic.Source != content.TopicSourceManual {
		t.Fatalf("source=%q", topic.Source)
	}
	if topic.Signals.ManualBoost != content.DefaultScoutManualBoost {
		t.Fatalf("signals.manual_boost=%v", topic.Signals.ManualBoost)
	}
}

func TestKeywordsForNiche(t *testing.T) {
	money := content.KeywordsForNiche("money_side_hustles", content.LanguageEN)
	if len(money) < 4 {
		t.Fatalf("money keywords=%v", money)
	}
	ai := content.KeywordsForNiche("ai_tools_tech", content.LanguageEN)
	if len(ai) < 4 {
		t.Fatalf("ai keywords=%v", ai)
	}
	hi := content.KeywordsForNiche("money_side_hustles", content.LanguageHI)
	if len(hi) <= len(money) {
		t.Fatalf("HI should prepend native keywords: %v", hi)
	}
}

func TestYouTubeRSSTrendsFixtures(t *testing.T) {
	ctx := context.Background()
	sqlDB, cleanup := openScoutDB(t)
	defer cleanup()
	seedChannel(t, sqlDB, "yt-ai-en", "ai_tools_tech")

	var mu sync.Mutex
	var searchN, videoN, trendsN, rssN int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(r.URL.Path, "/search"):
			if r.URL.Query().Get("key") != testYTKey {
				http.Error(w, "bad key", 401)
				return
			}
			searchN++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(readFixture(t, "scout_yt_search.json"))
		case strings.Contains(r.URL.Path, "/videos"):
			videoN++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(readFixture(t, "scout_yt_videos.json"))
		case r.URL.Path == "/trends":
			trendsN++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(readFixture(t, "scout_trends.json"))
		case r.URL.Path == "/feed.xml":
			rssN++
			w.Header().Set("Content-Type", "application/rss+xml")
			_, _ = w.Write(readFixture(t, "scout_feed.xml"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	s, err := content.NewScout(sqlDB, content.ScoutOptions{
		YouTubeAPIKey:  testYTKey,
		YouTubeBaseURL: srv.URL,
		TrendsBaseURL:  srv.URL + "/trends",
		TrendsAPIKey:   "trends-key",
		RSSFeeds:       []string{srv.URL + "/feed.xml"},
		HTTPClient:     srv.Client(),
		Keywords:       []string{"AI productivity workflow", "best AI tools for work"},
		Limit:          8,
		YouTubeQuota:   10_000,
		Now:            func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) },
		NewID:          seqID("yt"),
		RandFloat:      func() float64 { return 0.5 },
	})
	if err != nil {
		t.Fatal(err)
	}

	ch := content.Channel{ID: "yt-ai-en", Niche: "ai_tools_tech", Language: content.LanguageEN}
	warmup := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	topics, err := s.Run(ctx, ch, &warmup)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(topics) == 0 {
		t.Fatal("expected topics")
	}
	mu.Lock()
	sc, vc, tc, rc := searchN, videoN, trendsN, rssN
	mu.Unlock()
	if sc == 0 || vc == 0 {
		t.Fatalf("youtube search=%d videos=%d", sc, vc)
	}
	if tc == 0 {
		t.Fatal("trends not called")
	}
	if rc == 0 {
		t.Fatal("rss not called")
	}
	for _, tp := range topics {
		if tp.Score <= 0 {
			t.Fatalf("topic %q score=%v", tp.Title, tp.Score)
		}
		if tp.Source != content.TopicSourceScout {
			t.Fatalf("source=%q", tp.Source)
		}
		if tp.Signals.Demand < 0 || tp.Signals.Demand > 1 {
			t.Fatalf("demand out of range: %+v", tp.Signals)
		}
	}
}

func TestDedupeRecentTopics(t *testing.T) {
	ctx := context.Background()
	sqlDB, cleanup := openScoutDB(t)
	defer cleanup()
	seedChannel(t, sqlDB, "yt-money-en", "money_side_hustles")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write(readFixture(t, "scout_feed.xml"))
	}))
	t.Cleanup(srv.Close)

	opts := content.ScoutOptions{
		RSSFeeds:   []string{srv.URL},
		HTTPClient: srv.Client(),
		Limit:      10,
		Now:        func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) },
		NewID:      seqID("dd"),
		RandFloat:  func() float64 { return 0 },
		Keywords:   []string{"tax tips", "credit card basics"},
	}
	s, err := content.NewScout(sqlDB, opts)
	if err != nil {
		t.Fatal(err)
	}
	ch := content.Channel{ID: "yt-money-en", Niche: "money_side_hustles", Language: content.LanguageEN}
	warmup := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	first, err := s.Run(ctx, ch, &warmup)
	if err != nil {
		t.Fatalf("Run #1: %v", err)
	}
	if len(first) == 0 {
		t.Fatal("want topics on first run")
	}
	second, err := s.Run(ctx, ch, &warmup)
	if err != nil {
		t.Fatalf("Run #2: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("dedupe failed: second run wrote %d", len(second))
	}
}

func TestQuotaAwareStop(t *testing.T) {
	ctx := context.Background()
	sqlDB, cleanup := openScoutDB(t)
	defer cleanup()
	seedChannel(t, sqlDB, "yt-money-en", "money_side_hustles")

	var searches atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/search") {
			searches.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(readFixture(t, "scout_yt_search.json"))
			return
		}
		if strings.Contains(r.URL.Path, "/videos") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(readFixture(t, "scout_yt_videos.json"))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	s, err := content.NewScout(sqlDB, content.ScoutOptions{
		YouTubeAPIKey:  testYTKey,
		YouTubeBaseURL: srv.URL,
		HTTPClient:     srv.Client(),
		YouTubeQuota:   150, // one search(100)+videos(1); second search blocked
		Keywords:       []string{"side hustle", "budgeting system", "index funds"},
		Limit:          10,
		Now:            func() time.Time { return time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC) },
		NewID:          seqID("q"),
		RandFloat:      func() float64 { return 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	ch := content.Channel{ID: "yt-money-en", Niche: "money_side_hustles", Language: content.LanguageEN}
	warmup := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.Run(ctx, ch, &warmup); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if searches.Load() != 1 {
		t.Fatalf("searches=%d want 1", searches.Load())
	}
	if s.QuotaUsed() > 150 {
		t.Fatalf("quota used=%d", s.QuotaUsed())
	}
}

func TestExplorationFraction(t *testing.T) {
	ctx := context.Background()
	sqlDB, cleanup := openScoutDB(t)
	defer cleanup()
	seedChannel(t, sqlDB, "yt-money-en", "money_side_hustles")

	var body strings.Builder
	body.WriteString(`<?xml version="1.0"?><rss version="2.0"><channel><title>x</title>`)
	for i := 0; i < 20; i++ {
		body.WriteString(`<item><title>Finance budgeting hustle topic `)
		body.WriteString(itoa(int64(i)))
		body.WriteString(`</title><link>https://ex/`)
		body.WriteString(itoa(int64(i)))
		body.WriteString(`</link><pubDate>Mon, 21 Sep 2026 10:00:00 +0000</pubDate></item>`)
	}
	body.WriteString(`</channel></rss>`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(body.String()))
	}))
	t.Cleanup(srv.Close)

	s, err := content.NewScout(sqlDB, content.ScoutOptions{
		RSSFeeds:        []string{srv.URL},
		HTTPClient:      srv.Client(),
		Limit:           10,
		ExplorationRate: content.DefaultScoutExplorationRate,
		Keywords:        []string{"budgeting", "hustle", "finance"},
		Now:             func() time.Time { return time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC) },
		NewID:           seqID("ex"),
		RandFloat:       func() float64 { return 0.99 },
	})
	if err != nil {
		t.Fatal(err)
	}
	ch := content.Channel{ID: "yt-money-en", Niche: "money_side_hustles", Language: content.LanguageEN}
	warmup := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	topics, err := s.Run(ctx, ch, &warmup)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(topics) != 10 {
		t.Fatalf("wrote %d want 10", len(topics))
	}
	explored := 0
	for _, tp := range topics {
		if tp.Signals.Exploration {
			explored++
		}
	}
	// ~20% of 10 → exploit 8, explore 2
	if explored < 1 || explored > 3 {
		t.Fatalf("explored=%d/10 want ~20%%", explored)
	}
}

func TestAnalyticsAfterWeek4(t *testing.T) {
	ctx := context.Background()
	sqlDB, cleanup := openScoutDB(t)
	defer cleanup()
	seedChannel(t, sqlDB, "yt-money-en", "money_side_hustles")

	_, err := sqlDB.ExecContext(ctx, `
INSERT INTO scores (scope, channel_id, key, score, samples, updated_at)
VALUES ('topic', 'yt-money-en', 'new tax tips for freelancers in 2026', 0.95, 3, ?)
`, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write(readFixture(t, "scout_feed.xml"))
	}))
	t.Cleanup(srv.Close)

	s, err := content.NewScout(sqlDB, content.ScoutOptions{
		RSSFeeds:   []string{srv.URL},
		HTTPClient: srv.Client(),
		Limit:      5,
		Keywords:   []string{"tax tips", "credit"},
		Now:        func() time.Time { return time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC) },
		NewID:      seqID("an"),
		RandFloat:  func() float64 { return 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	ch := content.Channel{ID: "yt-money-en", Niche: "money_side_hustles", Language: content.LanguageEN}
	// 40+ days before now → analytics on
	warmup := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	topics, err := s.Run(ctx, ch, &warmup)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	found := false
	for _, tp := range topics {
		if strings.Contains(strings.ToLower(tp.Title), "tax tips") && tp.Signals.Analytics > 0 {
			found = true
			break
		}
	}
	if !found {
		// dump for debug
		raw, _ := json.Marshal(topics)
		t.Fatalf("expected analytics blend on tax tips topic: %s", raw)
	}
}

func TestNotConfigured(t *testing.T) {
	sqlDB, cleanup := openScoutDB(t)
	defer cleanup()
	_, err := content.NewScout(sqlDB, content.ScoutOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, content.ErrScoutNotConfigured) {
		t.Fatalf("err=%v", err)
	}
}

func TestNewScoutFromEnv(t *testing.T) {
	sqlDB, cleanup := openScoutDB(t)
	defer cleanup()
	s, err := content.NewScoutFromEnv(sqlDB, content.ScoutOptions{RSSFeeds: []string{"http://example/feed"}},
		func(k string) (string, bool) {
			if k == content.YouTubeAPIKeyEnv {
				return "env-key", true
			}
			return "", false
		})
	if err != nil {
		t.Fatal(err)
	}
	if s == nil {
		t.Fatal("nil scout")
	}
}

func TestTrendsSkippedWithoutBaseURL(t *testing.T) {
	ctx := context.Background()
	sqlDB, cleanup := openScoutDB(t)
	defer cleanup()
	seedChannel(t, sqlDB, "yt-money-en", "money_side_hustles")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "trends") {
			t.Error("trends must not be called when TrendsBaseURL empty")
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write(readFixture(t, "scout_feed.xml"))
	}))
	t.Cleanup(srv.Close)

	s, err := content.NewScout(sqlDB, content.ScoutOptions{
		RSSFeeds:     []string{srv.URL},
		TrendsAPIKey: "has-key-but-no-base",
		HTTPClient:   srv.Client(),
		Keywords:     []string{"budget"},
		Limit:        3,
		Now:          func() time.Time { return time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC) },
		NewID:        seqID("tr"),
		RandFloat:    func() float64 { return 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	ch := content.Channel{ID: "yt-money-en", Niche: "money_side_hustles", Language: content.LanguageEN}
	warmup := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.Run(ctx, ch, &warmup); err != nil {
		t.Fatal(err)
	}
}

// --- helpers ----------------------------------------------------------------

func openScoutDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mayank2.db")
	sqlDB, err := db.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := db.Migrate(ctx, sqlDB); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("Migrate: %v", err)
	}
	return sqlDB, func() { _ = sqlDB.Close() }
}

func seedChannel(t *testing.T, sqlDB *sql.DB, id, niche string) {
	t.Helper()
	_, err := sqlDB.ExecContext(context.Background(), `
INSERT INTO channels (id, platform, handle, language, niche, account_ref, status, warmup_started_at)
VALUES (?, 'youtube', 'TBD', 'en', ?, '', 'warming', ?)
`, id, niche, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return b
}

func seqID(prefix string) func() string {
	var n atomic.Int64
	return func() string {
		v := n.Add(1)
		s := itoa(v)
		pad := 26 - len(prefix) - len(s)
		if pad < 0 {
			pad = 0
		}
		return prefix + strings.Repeat("0", pad) + s
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
