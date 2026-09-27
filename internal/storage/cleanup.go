package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// DeleteAfter computes the retention cutoff for a piece of media: publishedAt
// plus content.retention_days (config.Config.Content.RetentionDays, default 7
// — CONTEXT D20). Negative retentionDays is treated as 0 (delete immediately)
// so a bad config value never keeps content forever.
func DeleteAfter(publishedAt time.Time, retentionDays int) time.Time {
	if retentionDays < 0 {
		retentionDays = 0
	}
	return publishedAt.AddDate(0, 0, retentionDays)
}

// CleanupItem is one asset eligible for retention cleanup. It mirrors the
// `assets` table (ARCHITECTURE §4: path, r2_key, delete_after) — this package
// has no DB dependency, so the `storage.cleanup` job handler loads the due
// rows and passes them in.
type CleanupItem struct {
	ID          string // assets.id, used only to key results/errors
	Path        string // absolute local path; must resolve under the cleanup root
	R2Key       string // empty if the asset was never uploaded to R2
	DeleteAfter time.Time
}

// CleanupResult reports what Cleanup did. A partial failure on one item never
// stops the run; check Errors for anything that needs attention.
type CleanupResult struct {
	DeletedLocal []string         // item IDs whose local file was removed (or already absent)
	DeletedR2    []string         // item IDs whose R2 object was removed
	Skipped      []string         // item IDs not yet due (now < DeleteAfter)
	Errors       map[string]error // item ID -> error (unsafe path, delete failure, ...)
}

func newCleanupResult() CleanupResult {
	return CleanupResult{Errors: map[string]error{}}
}

// Cleanup deletes local files (and, when r2 is configured and the item has an
// R2Key, the matching R2 object) for every item whose DeleteAfter is at or
// before now. r2 may be nil — R2 not being configured is not an error, it
// just means only the local half runs (D24: local-only storage must always
// work).
//
// Every local delete is re-validated against root before it happens: an item
// whose Path does not resolve under root is never touched and is reported as
// an error, so a bad or tampered assets row can't make cleanup delete files
// outside data/media.
func Cleanup(ctx context.Context, root string, items []CleanupItem, r2 *R2Client, now time.Time) CleanupResult {
	res := newCleanupResult()
	absRoot, err := filepath.Abs(root)
	if err != nil {
		for _, it := range items {
			res.Errors[it.ID] = fmt.Errorf("storage cleanup: resolve root %q: %w", root, err)
		}
		return res
	}
	absRoot = filepath.Clean(absRoot)

	for _, it := range items {
		if err := ctx.Err(); err != nil {
			res.Errors[it.ID] = fmt.Errorf("storage cleanup: %w", err)
			continue
		}
		if now.Before(it.DeleteAfter) {
			res.Skipped = append(res.Skipped, it.ID)
			continue
		}
		if err := deleteLocal(absRoot, it.Path); err != nil {
			res.Errors[it.ID] = err
			continue
		}
		res.DeletedLocal = append(res.DeletedLocal, it.ID)

		if it.R2Key == "" {
			continue
		}
		if r2 == nil {
			continue // R2 not configured (D24): local cleanup still ran, nothing more to do
		}
		if err := r2.Delete(ctx, it.R2Key); err != nil {
			res.Errors[it.ID] = fmt.Errorf("storage cleanup: delete r2 object for %s: %w", it.ID, err)
			continue
		}
		res.DeletedR2 = append(res.DeletedR2, it.ID)
	}
	return res
}

// deleteLocal removes path after confirming it resolves under root. A path
// that is already gone is not an error (cleanup is idempotent — a retried or
// re-run job must not fail just because a previous run already deleted it).
func deleteLocal(root, path string) error {
	if path == "" {
		return errors.New("storage cleanup: empty path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("storage cleanup: resolve %q: %w", path, err)
	}
	abs = filepath.Clean(abs)
	if !withinRoot(root, abs) {
		return fmt.Errorf("storage cleanup: %w: %q is outside %q, refusing to delete", ErrPathEscapesRoot, path, root)
	}
	if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("storage cleanup: delete %q: %w", abs, err)
	}
	return nil
}
