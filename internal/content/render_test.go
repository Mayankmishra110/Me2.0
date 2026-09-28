package content

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mayank2/internal/db"
	"mayank2/internal/media"
	"mayank2/internal/queue"
	"mayank2/internal/storage"
)

func renderTestDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	sqlDB, err := db.Open(ctx, filepath.Join(t.TempDir(), "render.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := db.Migrate(ctx, sqlDB); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	mustExec(t, sqlDB, `INSERT INTO channels (id, platform, language, niche, status) VALUES ('ch1','youtube','en','ai','active')`)
	mustExec(t, sqlDB, `INSERT INTO content_items (id, channel_id, kind, language, format, stage, created_at)
VALUES ('c1','ch1','long','en','explained_60s','voice',?)`, time.Now().UTC().Format(time.RFC3339Nano))
	return sqlDB
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec %s: %v", q, err)
	}
}

func testRenderer(t *testing.T, sqlDB *sql.DB, run media.ExecRunner) *Renderer {
	t.Helper()
	dataDir := t.TempDir()
	layout, err := storage.NewLayout(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	remotionRoot := t.TempDir()
	_ = os.MkdirAll(filepath.Join(remotionRoot, "public"), 0o755)
	var idSeq atomic.Int64
	return &Renderer{
		DB:            sqlDB,
		Layout:        layout,
		Tools:         &media.Tools{Run: run, RemotionRoot: remotionRoot, MediaToolsDir: t.TempDir(), FFmpeg: "ffmpeg", UV: "uv", Node: "node"},
		RetentionDays: 7,
		Now:           func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) },
		NewID: func() string {
			n := idSeq.Add(1)
			return fmt.Sprintf("01TESTASSET%016d", n)
		},
	}
}

func TestRegisterHandlers_heavy(t *testing.T) {
	sqlDB := renderTestDB(t)
	q := queue.New(sqlDB)
	r := testRenderer(t, sqlDB, nil)
	r.NewID = func() string { return "01ASSET000000000000000001" }
	r.RegisterHandlers(q)

	for _, typ := range []string{JobVoiceTTS, JobRenderLong, JobRenderShort, JobRenderThumbnail} {
		id, err := q.Enqueue(context.Background(), typ, map[string]string{})
		if err != nil {
			t.Fatalf("enqueue %s: %v", typ, err)
		}
		var res string
		if err := sqlDB.QueryRow(`SELECT resource FROM jobs WHERE id=?`, id).Scan(&res); err != nil {
			t.Fatal(err)
		}
		if res != string(queue.ResourceHeavy) {
			t.Fatalf("%s resource=%s want heavy", typ, res)
		}
	}
}

func TestHandleVoiceTTS(t *testing.T) {
	sqlDB := renderTestDB(t)
	r := testRenderer(t, sqlDB, func(ctx context.Context, name string, args []string, dir string, env []string) ([]byte, []byte, error) {
		_ = ctx
		_ = env
		if name != "uv" {
			t.Fatalf("bin=%s", name)
		}
		outJSON := args[len(args)-1]
		inJSON := args[len(args)-3]
		raw, _ := os.ReadFile(inJSON)
		var in media.TTSRequest
		_ = json.Unmarshal(raw, &in)
		_ = os.WriteFile(in.OutWAV, []byte("RIFFWAV"), 0o644)
		body, _ := json.Marshal(media.TTSResult{
			WAVPath: in.OutWAV, SampleRate: 24000, DurationSec: 0.5, Lang: "en", Voice: "af_heart",
			Words: []media.WordTiming{{Word: "Hi", Start: 0, End: 0.5}},
		})
		return nil, nil, os.WriteFile(outJSON, body, 0o644)
	})

	cid := "c1"
	job := queue.Job{ID: "jobtts1", Type: JobVoiceTTS, ContentID: &cid, Payload: json.RawMessage(`{
		"content_id":"c1","text":"Hi","lang":"en","voice":"af_heart"
	}`)}
	raw, err := r.handleVoiceTTS(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	var res map[string]any
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	asset := res["asset"].(map[string]any)
	if asset["kind"] != "voice" || asset["sha256"] == "" || asset["bytes"].(float64) == 0 {
		t.Fatalf("asset=%v", asset)
	}
	var da string
	var sha string
	var bytes int64
	err = sqlDB.QueryRow(`SELECT sha256, bytes, delete_after FROM assets WHERE content_id=? AND kind='voice'`, "c1").
		Scan(&sha, &bytes, &da)
	if err != nil {
		t.Fatal(err)
	}
	if sha == "" || bytes == 0 || da == "" {
		t.Fatalf("sha=%q bytes=%d da=%q", sha, bytes, da)
	}
	if !strings.HasPrefix(da, "2026-10-05") { // +7 days from fixed now
		t.Fatalf("delete_after=%s want ~2026-10-05", da)
	}

	raw2, err := r.handleVoiceTTS(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw2), "already_rendered") {
		t.Fatalf("want skip, got %s", raw2)
	}
}

func TestHandleRenderLong_short_thumbnail(t *testing.T) {
	sqlDB := renderTestDB(t)
	r := testRenderer(t, sqlDB, func(ctx context.Context, name string, args []string, dir string, env []string) ([]byte, []byte, error) {
		_ = ctx
		_ = dir
		_ = env
		joined := strings.Join(args, " ")
		var out string
		switch {
		case name == "node" && strings.Contains(joined, " still "):
			out = args[4]
		case name == "node" && strings.Contains(joined, " render "):
			out = args[4]
		case name == "ffmpeg":
			out = args[len(args)-1]
		default:
			t.Fatalf("unexpected %s %v", name, args)
		}
		return nil, nil, os.WriteFile(out, []byte("bytes"), 0o644)
	})

	rawDir := filepath.Join(r.Layout.Root(), "raw")
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
		t.Fatal(err)
	}
	clip := filepath.Join(rawDir, "beat.png")
	voice := filepath.Join(rawDir, "voice.wav")
	if err := os.WriteFile(clip, []byte("PNG"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(voice, []byte("WAV"), 0o644); err != nil {
		t.Fatal(err)
	}

	payload := map[string]any{
		"content_id": "c1",
		"format":     "explained_60s",
		"brand_kit": BrandKit{
			Primary: "#22D3EE", Secondary: "#0F172A",
			FontHeading: "Montserrat", FontBody: "Inter", CaptionStyle: CaptionWordHighlight,
		},
		"beats": []map[string]any{
			{"text": "Hook", "clip_path": clip, "duration_in_seconds": 2},
		},
		"voice_path": voice,
		"words":      []media.WordTiming{{Word: "Hook", Start: 0, End: 0.5}},
		"thumb_text": "Hook Now",
	}
	body, _ := json.Marshal(payload)
	cid := "c1"
	job := queue.Job{ID: "joblong1", Type: JobRenderLong, ContentID: &cid, Payload: body}

	raw, err := r.handleRenderLong(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "explained-60s-landscape") {
		t.Fatalf("result=%s", raw)
	}

	// assets: render + subs (+ thumb)
	var kinds []string
	rows, err := sqlDB.Query(`SELECT kind FROM assets WHERE content_id='c1' ORDER BY kind`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		_ = rows.Scan(&k)
		kinds = append(kinds, k)
	}
	joined := strings.Join(kinds, ",")
	if !strings.Contains(joined, "render") || !strings.Contains(joined, "subs") {
		t.Fatalf("kinds=%v", kinds)
	}
	var daCount int
	_ = sqlDB.QueryRow(`SELECT COUNT(*) FROM assets WHERE content_id='c1' AND delete_after IS NOT NULL AND sha256 IS NOT NULL AND bytes > 0`).Scan(&daCount)
	if daCount < 2 {
		t.Fatalf("assets with digest+delete_after=%d", daCount)
	}

	// short
	mustExec(t, sqlDB, `DELETE FROM assets WHERE content_id='c1'`)
	job.ID = "jobshort1"
	job.Type = JobRenderShort
	raw, err = r.handleRenderShort(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "explained-60s-portrait") {
		t.Fatalf("short result=%s", raw)
	}

	// thumbnail
	mustExec(t, sqlDB, `DELETE FROM assets WHERE content_id='c1'`)
	job.ID = "jobthumb1"
	job.Type = JobRenderThumbnail
	raw, err = r.handleRenderThumbnail(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"kind":"thumb"`) {
		t.Fatalf("thumb result=%s", raw)
	}
}

func TestRetention_zeroUsesDefault(t *testing.T) {
	r := &Renderer{} // RetentionDays unset / zero
	if got := r.retention(); got != defaultRetentionDays {
		t.Fatalf("zero RetentionDays → %d, want %d", got, defaultRetentionDays)
	}
	r.RetentionDays = 3
	if got := r.retention(); got != 3 {
		t.Fatalf("RetentionDays=3 → %d", got)
	}
	r.Now = func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) }
	r.RetentionDays = 0
	da := r.deleteAfter()
	if !strings.HasPrefix(da, "2026-10-05") {
		t.Fatalf("delete_after with zero retention=%s want ~2026-10-05 (+7d)", da)
	}
}

func TestHandleRender_pathOutsideMediaRoot(t *testing.T) {
	sqlDB := renderTestDB(t)
	r := testRenderer(t, sqlDB, nil)

	outside := filepath.Join(t.TempDir(), "secret.env")
	if err := os.WriteFile(outside, []byte("SECRET=1"), 0o644); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(r.Layout.Root(), "raw", "ok.wav")
	if err := os.MkdirAll(filepath.Dir(inside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inside, []byte("WAV"), 0o644); err != nil {
		t.Fatal(err)
	}

	payload := map[string]any{
		"content_id": "c1",
		"format":     "explained_60s",
		"brand_kit":  BrandKit{Primary: "#22D3EE", Secondary: "#0F172A", FontHeading: "M", FontBody: "I", CaptionStyle: CaptionWordHighlight},
		"beats": []map[string]any{
			{"text": "Hook", "clip_path": outside, "duration_in_seconds": 2},
		},
		"voice_path": inside,
		"words":      []media.WordTiming{{Word: "Hook", Start: 0, End: 0.5}},
	}
	body, _ := json.Marshal(payload)
	cid := "c1"
	_, err := r.handleRenderLong(context.Background(), queue.Job{ID: "jobescape1", Type: JobRenderLong, ContentID: &cid, Payload: body})
	if err == nil || !queue.IsPermanent(err) {
		t.Fatalf("want permanent err for outside clip_path, got %v", err)
	}
	if !strings.Contains(err.Error(), "clip_path") && !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("want escape/clip_path in error, got %v", err)
	}

	// voice_path outside also fails closed
	payload["beats"] = []map[string]any{
		{"text": "Hook", "clip_path": filepath.Join(r.Layout.Root(), "raw", "beat.png"), "duration_in_seconds": 2},
	}
	_ = os.WriteFile(filepath.Join(r.Layout.Root(), "raw", "beat.png"), []byte("PNG"), 0o644)
	payload["voice_path"] = outside
	body, _ = json.Marshal(payload)
	_, err = r.handleRenderLong(context.Background(), queue.Job{ID: "jobescape2", Type: JobRenderLong, ContentID: &cid, Payload: body})
	if err == nil || !queue.IsPermanent(err) {
		t.Fatalf("want permanent err for outside voice_path, got %v", err)
	}

	// thumbnail focal outside
	thumbPayload := map[string]any{
		"content_id":      "c1",
		"thumb_text":      "Hook",
		"focal_clip_path": outside,
	}
	body, _ = json.Marshal(thumbPayload)
	_, err = r.handleRenderThumbnail(context.Background(), queue.Job{ID: "jobescape3", Type: JobRenderThumbnail, ContentID: &cid, Payload: body})
	if err == nil || !queue.IsPermanent(err) {
		t.Fatalf("want permanent err for outside focal_clip_path, got %v", err)
	}
}

func TestHandleRender_badPayload(t *testing.T) {
	sqlDB := renderTestDB(t)
	r := testRenderer(t, sqlDB, nil)
	r.NewID = func() string { return "01BAD00000000000000000001" }
	_, err := r.handleRenderLong(context.Background(), queue.Job{ID: "j", Payload: json.RawMessage(`{}`)})
	if err == nil || !queue.IsPermanent(err) {
		t.Fatalf("want permanent err, got %v", err)
	}
}
