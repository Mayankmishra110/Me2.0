// AssetR2KeyResolver (M2-123) closes the gap M2-122 documented: Instagram,
// Facebook and Pinterest each accept an R2KeyResolver func(ctx, contentID,
// videoPath) (string, error) that, when nil, falls back to using VideoPath
// (or ThumbnailPath, for Pinterest's cover) directly as the R2 object key —
// an assumption that only holds if something uploaded the render to R2
// under exactly that key. internal/content/render.go (Renderer.R2) now does
// that upload and records the real key in assets.r2_key, so this resolver
// just reads it back: a cheap indexed SELECT, not a lazy upload (see
// tickets/M2-123.md's design notes for why upload-at-render-time was chosen
// over upload-at-publish-time).
package publish

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
)

// r2ResolverDB is the subset of *sql.DB AssetR2KeyResolver needs (fakeable
// in tests without a real database).
type r2ResolverDB interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// AssetR2KeyResolver returns an R2KeyResolver (see Instagram.R2KeyResolver,
// Facebook.R2KeyResolver, Pinterest.R2KeyResolver) backed by db. It looks up
// every uploaded asset (assets.r2_key IS NOT NULL) recorded for contentID
// and returns the one whose local path's filename matches videoPath's
// filename — content_id alone is ambiguous (a piece of content can have
// both a long.mp4 render and a short.mp4 render, plus a thumb.png), the same
// disambiguation render.go's own "already rendered" skip check uses
// (filepath.Base comparison).
//
// db may be nil (R2 not configured / no DB wired) — the resolver then
// returns "" with no error, and each publisher's existing nil-safe fallback
// (use videoPath itself) takes over, matching CONTEXT D24's "degrade
// cleanly" rule. A path with no matching r2_key (never uploaded, e.g. R2
// wasn't configured when it rendered) also returns "" with no error, for
// the same reason — the publisher's later presign then fails with a clear
// "R2 not configured" or 404-shaped error instead of this resolver hiding
// the gap.
func AssetR2KeyResolver(db r2ResolverDB) func(ctx context.Context, contentID, videoPath string) (string, error) {
	return func(ctx context.Context, contentID, videoPath string) (string, error) {
		if db == nil || strings.TrimSpace(contentID) == "" {
			return "", nil
		}
		base := filepath.Base(strings.TrimSpace(videoPath))

		rows, err := db.QueryContext(ctx, `
SELECT path, r2_key FROM assets
WHERE content_id = ? AND r2_key IS NOT NULL AND r2_key != ''
ORDER BY id DESC`, contentID)
		if err != nil {
			return "", fmt.Errorf("publish: resolve r2 key for %s: %w", contentID, err)
		}
		defer rows.Close()

		for rows.Next() {
			var p, key string
			if err := rows.Scan(&p, &key); err != nil {
				return "", fmt.Errorf("publish: resolve r2 key for %s: scan: %w", contentID, err)
			}
			if base == "" || filepath.Base(p) == base {
				return key, nil
			}
		}
		if err := rows.Err(); err != nil {
			return "", fmt.Errorf("publish: resolve r2 key for %s: %w", contentID, err)
		}
		return "", nil
	}
}
