package publish

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// seedAssetsForResolver inserts a channel/content_item (FK target) plus the
// given (path, r2_key) asset rows for content_id, mirroring what
// content.Renderer.recordFile (internal/content/render.go, M2-123) writes.
func seedAssetsForResolver(t *testing.T, sqlDB *sql.DB, contentID string, assets [][2]string) {
	t.Helper()
	mustExec(t, sqlDB, `INSERT OR IGNORE INTO channels (id, platform, language, niche, status) VALUES ('ch1','youtube','en','ai','active')`)
	mustExec(t, sqlDB, `INSERT OR IGNORE INTO content_items (id, channel_id, kind, language, format, stage, created_at)
VALUES (?, 'ch1', 'long', 'en', 'explained_60s', 'render', ?)`, contentID, time.Now().UTC().Format(time.RFC3339Nano))
	for i, a := range assets {
		path, r2Key := a[0], a[1]
		var r2KeyArg any
		if r2Key != "" {
			r2KeyArg = r2Key
		}
		mustExec(t, sqlDB, `INSERT INTO assets (id, content_id, kind, path, r2_key, sha256, bytes, delete_after)
VALUES (?, ?, 'render', ?, ?, 'deadbeef', 123, ?)`,
			assetIDForTest(i), contentID, path, r2KeyArg, time.Now().UTC().Format(time.RFC3339Nano))
	}
}

func assetIDForTest(i int) string {
	return "01RESOLVERASSET00000000" + string(rune('A'+i))
}

func TestAssetR2KeyResolver_MatchesByFilename(t *testing.T) {
	sqlDB := openPubDB(t)
	seedAssetsForResolver(t, sqlDB, "c1", [][2]string{
		{`C:\data\media\renders\c1\long.mp4`, "renders/c1/long.mp4"},
		{`C:\data\media\renders\c1\short.mp4`, "renders/c1/short.mp4"},
		{`C:\data\media\renders\c1\thumb.png`, "renders/c1/thumb.png"},
	})

	resolve := AssetR2KeyResolver(sqlDB)

	key, err := resolve(context.Background(), "c1", `C:\data\media\renders\c1\short.mp4`)
	if err != nil {
		t.Fatalf("resolve short: %v", err)
	}
	if key != "renders/c1/short.mp4" {
		t.Fatalf("resolve short: got %q", key)
	}

	key, err = resolve(context.Background(), "c1", `C:\data\media\renders\c1\long.mp4`)
	if err != nil {
		t.Fatalf("resolve long: %v", err)
	}
	if key != "renders/c1/long.mp4" {
		t.Fatalf("resolve long: got %q", key)
	}
}

func TestAssetR2KeyResolver_NoMatchReturnsEmptyNoError(t *testing.T) {
	sqlDB := openPubDB(t)
	seedAssetsForResolver(t, sqlDB, "c1", [][2]string{
		{`C:\data\media\renders\c1\long.mp4`, "renders/c1/long.mp4"},
	})

	resolve := AssetR2KeyResolver(sqlDB)
	key, err := resolve(context.Background(), "c1", `C:\data\media\renders\c1\other.mp4`)
	if err != nil {
		t.Fatalf("resolve other: %v", err)
	}
	if key != "" {
		t.Fatalf("resolve other: got %q, want empty (no fabricated fallback)", key)
	}
}

func TestAssetR2KeyResolver_NeverUploadedReturnsEmptyNoError(t *testing.T) {
	sqlDB := openPubDB(t)
	// r2_key NULL: rendered while R2 was not configured.
	seedAssetsForResolver(t, sqlDB, "c1", [][2]string{
		{`C:\data\media\renders\c1\long.mp4`, ""},
	})

	resolve := AssetR2KeyResolver(sqlDB)
	key, err := resolve(context.Background(), "c1", `C:\data\media\renders\c1\long.mp4`)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if key != "" {
		t.Fatalf("resolve: got %q, want empty (never uploaded)", key)
	}
}

func TestAssetR2KeyResolver_NilDBReturnsEmptyNoError(t *testing.T) {
	resolve := AssetR2KeyResolver(nil)
	key, err := resolve(context.Background(), "c1", "video.mp4")
	if err != nil || key != "" {
		t.Fatalf("nil db: key=%q err=%v", key, err)
	}
}

func TestAssetR2KeyResolver_EmptyContentIDReturnsEmptyNoError(t *testing.T) {
	sqlDB := openPubDB(t)
	resolve := AssetR2KeyResolver(sqlDB)
	key, err := resolve(context.Background(), "", "video.mp4")
	if err != nil || key != "" {
		t.Fatalf("empty content_id: key=%q err=%v", key, err)
	}
}
