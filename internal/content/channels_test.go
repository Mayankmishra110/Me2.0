package content_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mayank2/internal/content"
	"mayank2/internal/db"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func validChannelYAML(id string) string {
	return `
id: ` + id + `
platform: youtube
handle: TBD
language: en
niche: money_side_hustles
brand: { primary: "#16A34A", secondary: "#0B1220", font_heading: "Montserrat", font_body: "Inter", caption_style: word-highlight }
voice: { engine: kokoro, voice_id: TBD, speed: 1.0 }
formats: [explained_60s, top_n]
windows: { tz: America/New_York, slots: ["12:30", "19:30"] }
disclaimer: "Educational content only."
`
}

// TestLoadChannels_table covers the happy path and every invalid-config case
// this ticket's acceptance criteria call out: missing field (delegated to
// internal/config), duplicate id, bad language, unknown format id, and bad
// caption style.
func TestLoadChannels_table(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		wantErr string
		check   func(t *testing.T, chs []content.Channel)
	}{
		{
			name: "valid multi-channel dir loads sorted by id",
			files: map[string]string{
				"b.yaml": validChannelYAML("yt-b"),
				"a.yaml": validChannelYAML("yt-a"),
			},
			check: func(t *testing.T, chs []content.Channel) {
				if len(chs) != 2 {
					t.Fatalf("len=%d want 2", len(chs))
				}
				if chs[0].ID != "yt-a" || chs[1].ID != "yt-b" {
					t.Fatalf("order=%v want [yt-a yt-b]", []string{chs[0].ID, chs[1].ID})
				}
				ch := chs[0]
				if ch.Language != content.LanguageEN {
					t.Fatalf("language=%v want en", ch.Language)
				}
				if ch.Brand.CaptionStyle != content.CaptionWordHighlight {
					t.Fatalf("caption style=%v", ch.Brand.CaptionStyle)
				}
				if ch.Brand.Primary != "#16A34A" || ch.Brand.FontHeading != "Montserrat" {
					t.Fatalf("brand=%+v", ch.Brand)
				}
				if len(ch.Formats) != 2 || ch.Formats[0] != "explained_60s" {
					t.Fatalf("formats=%v", ch.Formats)
				}
			},
		},
		{
			name: "missing required field surfaces internal/config's error",
			files: map[string]string{
				"a.yaml": strings.Replace(validChannelYAML("yt-a"), "id: yt-a\n", "id: \"\"\n", 1),
			},
			wantErr: "id is required",
		},
		{
			name: "duplicate channel id across files",
			files: map[string]string{
				"a.yaml": validChannelYAML("same"),
				"b.yaml": validChannelYAML("same"),
			},
			wantErr: "duplicate channel id",
		},
		{
			name: "bad language rejected",
			files: map[string]string{
				"a.yaml": strings.Replace(validChannelYAML("yt-a"), "language: en", "language: fr", 1),
			},
			wantErr: "language",
		},
		{
			name: "unknown format id rejected",
			files: map[string]string{
				"a.yaml": strings.Replace(validChannelYAML("yt-a"), "formats: [explained_60s, top_n]", "formats: [explained_60s, made_up_format]", 1),
			},
			wantErr: "unknown format id",
		},
		{
			name: "bad caption style rejected",
			files: map[string]string{
				"a.yaml": strings.Replace(validChannelYAML("yt-a"), "caption_style: word-highlight", "caption_style: bounce", 1),
			},
			wantErr: "caption_style",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range tt.files {
				writeFile(t, filepath.Join(dir, name), body)
			}
			chs, err := content.LoadChannels(dir)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("want error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadChannels: %v", err)
			}
			if tt.check != nil {
				tt.check(t, chs)
			}
		})
	}
}

func TestLoadChannels_realConfig(t *testing.T) {
	root := findRepoRoot(t)
	dir := filepath.Join(root, "config", "channels")
	if _, err := os.Stat(dir); err != nil {
		t.Skip("config/channels not found")
	}
	chs, err := content.LoadChannels(dir)
	if err != nil {
		t.Fatalf("LoadChannels(repo config): %v", err)
	}
	if len(chs) != 4 {
		t.Fatalf("expected 4 real channels, got %d", len(chs))
	}
	seen := map[string]bool{}
	for _, ch := range chs {
		if seen[ch.ID] {
			t.Fatalf("duplicate id %q in repo config", ch.ID)
		}
		seen[ch.ID] = true
	}
	for _, want := range []string{"yt-ai-en", "yt-ai-hi", "yt-money-en", "yt-money-hi"} {
		if !seen[want] {
			t.Fatalf("missing channel %q", want)
		}
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := wd
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "config", "config.example.yaml")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Skip("repo root with config.example.yaml not found")
	return ""
}

// TestCapsFor_table covers COMPLIANCE §4's ramp-up cadence across weeks.
func TestCapsFor_table(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rec := content.Record{ID: "yt-a", WarmupStartedAt: &start}

	tests := []struct {
		name string
		date time.Time
		want content.Caps
	}{
		{"day 0 (week 1)", start, content.Caps{ShortsPerDay: 1, LongPerWeek: 1}},
		{"day 6 (still week 1)", start.AddDate(0, 0, 6), content.Caps{ShortsPerDay: 1, LongPerWeek: 1}},
		{"day 7 (week 2)", start.AddDate(0, 0, 7), content.Caps{ShortsPerDay: 1, LongPerWeek: 2}},
		{"day 14 (week 3)", start.AddDate(0, 0, 14), content.Caps{ShortsPerDay: 2, LongPerWeek: 2}},
		{"day 21 (week 4)", start.AddDate(0, 0, 21), content.Caps{ShortsPerDay: 2, LongPerWeek: 4}},
		{"day 90 (week 4+ stays capped)", start.AddDate(0, 0, 90), content.Caps{ShortsPerDay: 2, LongPerWeek: 4}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := content.CapsFor(rec, tt.date)
			if got != tt.want {
				t.Fatalf("CapsFor=%+v want %+v", got, tt.want)
			}
		})
	}

	t.Run("no warmup_started_at means zero caps", func(t *testing.T) {
		got := content.CapsFor(content.Record{ID: "yt-b"}, start)
		if got != (content.Caps{}) {
			t.Fatalf("CapsFor=%+v want zero value", got)
		}
	})
}

// TestSyncChannels_roundTrip proves the DB round-trip: first sync inserts
// with status=warming and sets warmup_started_at; a second sync (simulating
// a config reload) refreshes niche but leaves status/warmup_started_at
// alone.
func TestSyncChannels_roundTrip(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mayank2.db")

	sqlDB, err := db.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlDB.Close()
	if _, err := db.Migrate(ctx, sqlDB); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ch := content.Channel{
		ID:       "yt-a",
		Platform: "youtube",
		Handle:   "TBD",
		Language: content.LanguageEN,
		Niche:    "money_side_hustles",
		Brand: content.BrandKit{
			Primary: "#16A34A", Secondary: "#0B1220",
			FontHeading: "Montserrat", FontBody: "Inter",
			CaptionStyle: content.CaptionWordHighlight,
		},
		Voice:   content.Voice{Engine: "kokoro", Speed: 1},
		Formats: []string{"explained_60s"},
		Windows: content.Windows{TZ: "America/New_York", Slots: []string{"12:30"}},
	}

	firstNow := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	recs, err := content.SyncChannels(ctx, sqlDB, []content.Channel{ch}, firstNow)
	if err != nil {
		t.Fatalf("SyncChannels #1: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("recs=%v", recs)
	}
	first := recs[0]
	if first.Status != "warming" {
		t.Fatalf("status=%q want warming", first.Status)
	}
	if first.WarmupStartedAt == nil || !first.WarmupStartedAt.Equal(firstNow) {
		t.Fatalf("warmup_started_at=%v want %v", first.WarmupStartedAt, firstNow)
	}
	if first.Niche != "money_side_hustles" {
		t.Fatalf("niche=%q", first.Niche)
	}

	// Simulate the scheduler activating the channel and time passing, then a
	// config reload with an updated niche.
	if _, err := sqlDB.ExecContext(ctx, `UPDATE channels SET status = 'active' WHERE id = ?`, ch.ID); err != nil {
		t.Fatalf("simulate activation: %v", err)
	}
	ch.Niche = "money_side_hustles_v2"
	secondNow := firstNow.AddDate(0, 0, 30)
	recs2, err := content.SyncChannels(ctx, sqlDB, []content.Channel{ch}, secondNow)
	if err != nil {
		t.Fatalf("SyncChannels #2: %v", err)
	}
	second := recs2[0]
	if second.Status != "active" {
		t.Fatalf("status=%q want active (should be left alone by sync)", second.Status)
	}
	if second.WarmupStartedAt == nil || !second.WarmupStartedAt.Equal(firstNow) {
		t.Fatalf("warmup_started_at=%v want unchanged %v", second.WarmupStartedAt, firstNow)
	}
	if second.Niche != "money_side_hustles_v2" {
		t.Fatalf("niche=%q want refreshed value", second.Niche)
	}

	got, err := content.GetChannel(ctx, sqlDB, ch.ID)
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	if got == nil || got.ID != ch.ID {
		t.Fatalf("GetChannel=%+v", got)
	}

	missing, err := content.GetChannel(ctx, sqlDB, "does-not-exist")
	if err != nil {
		t.Fatalf("GetChannel missing: %v", err)
	}
	if missing != nil {
		t.Fatalf("GetChannel missing=%+v want nil", missing)
	}
}
