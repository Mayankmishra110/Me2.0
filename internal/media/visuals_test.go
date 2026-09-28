// Tests for the M2-117 visuals.fetch queue adapter (visuals.go), reusing
// stock_test.go's fake Pexels/Pixabay TLS server so no real network calls
// happen here.
package media

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	dbpkg "mayank2/internal/db"
	"mayank2/internal/queue"
	"mayank2/internal/storage"
)

func visualsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	sqlDB, err := dbpkg.Open(ctx, filepath.Join(t.TempDir(), "visuals.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := dbpkg.Migrate(ctx, sqlDB); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	mustExecVisuals(t, sqlDB, `INSERT INTO channels (id, platform, language, niche, status) VALUES ('ch1','youtube','en','ai','active')`)
	mustExecVisuals(t, sqlDB, `INSERT INTO content_items (id, channel_id, kind, language, format, stage, created_at)
VALUES ('c1','ch1','short','en','','voice',?)`, time.Now().UTC().Format(time.RFC3339Nano))
	return sqlDB
}

func mustExecVisuals(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec %s: %v", q, err)
	}
}

func TestVisualsFetcher_handle_recordsAssetsPerBeat(t *testing.T) {
	sqlDB := visualsTestDB(t)
	fapi := newFakeStockAPI(t)
	stock, _, _ := newTestStock(t, fapi, []StockProvider{fapi.pexels()}, nil)

	layout, err := storage.NewLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}

	v := &VisualsFetcher{
		DB:     sqlDB,
		Stock:  stock,
		Layout: layout,
	}

	payload, _ := json.Marshal(VisualsFetchPayload{
		ContentID: "c1",
		ChannelID: "ch1",
		Beats: []VisualBeatRequest{
			{Text: "an intro beat", VisualCue: "city skyline sunset"},
		},
	})
	job := queue.Job{ID: "job1", Type: JobVisualsFetch, Payload: payload}

	out, err := v.handle(context.Background(), job)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	var res VisualsFetchResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(res.Beats) != 1 || res.Beats[0].ClipPath == "" {
		t.Fatalf("expected one beat result with a clip path, got %+v", res)
	}

	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM assets WHERE content_id='c1' AND kind='clip'`).Scan(&count); err != nil {
		t.Fatalf("count assets: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 clip asset recorded, got %d", count)
	}
}

func TestVisualsFetcher_handle_requiresContentIDChannelIDBeats(t *testing.T) {
	v := &VisualsFetcher{}
	tests := []struct {
		name    string
		payload string
	}{
		{"missing content_id", `{"channel_id":"ch1","beats":[{"text":"x"}]}`},
		{"missing channel_id", `{"content_id":"c1","beats":[{"text":"x"}]}`},
		{"missing beats", `{"content_id":"c1","channel_id":"ch1"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := queue.Job{ID: "job1", Type: JobVisualsFetch, Payload: json.RawMessage(tt.payload)}
			_, err := v.handle(context.Background(), job)
			if err == nil || !queue.IsPermanent(err) {
				t.Fatalf("expected a permanent error, got %v", err)
			}
		})
	}
}

func TestDeriveKeywords(t *testing.T) {
	if got := deriveKeywords("city skyline at sunset", ""); len(got) == 0 {
		t.Fatalf("expected keywords from visual cue, got %v", got)
	}
	if got := deriveKeywords("", "a fallback beat text"); len(got) == 0 {
		t.Fatalf("expected keywords from text fallback, got %v", got)
	}
	if got := deriveKeywords("", ""); got != nil {
		t.Fatalf("expected no keywords for empty input, got %v", got)
	}
}
