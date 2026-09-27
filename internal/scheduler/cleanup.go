package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"mayank2/internal/queue"
	"mayank2/internal/storage"
)

func (s *Scheduler) handleStorageCleanup(ctx context.Context, job queue.Job) (json.RawMessage, error) {
	_ = job
	now := s.now().UTC()
	layout, err := storage.NewLayout(s.cfg.DataDir)
	if err != nil {
		return nil, queue.Permanent(fmt.Errorf("scheduler: storage.cleanup layout: %w", err))
	}
	root := layout.Root()

	items, err := s.loadDueAssets(ctx, now)
	if err != nil {
		return nil, fmt.Errorf("scheduler: storage.cleanup load assets: %w", err)
	}

	var r2 *storage.R2Client
	r2Client, r2Err := storage.NewR2FromEnv(nil, storage.R2Options{})
	if r2Err != nil {
		var nc *storage.NotConfiguredError
		if !errors.As(r2Err, &nc) && !errors.Is(r2Err, storage.ErrR2NotConfigured) {
			s.log.Warn("scheduler: storage.cleanup r2", "err", r2Err)
		}
	} else {
		r2 = r2Client
	}

	res := storage.Cleanup(ctx, root, items, r2, now)

	low, free, diskErr := false, uint64(0), error(nil)
	if s.freeDisk != nil {
		free, diskErr = s.freeDisk(root)
		if diskErr == nil {
			low = free < storage.LowDiskThresholdBytes
		}
	} else {
		low, free, diskErr = storage.LowDisk(root)
	}
	if diskErr != nil {
		s.log.Warn("scheduler: storage.cleanup disk check", "err", diskErr)
	} else if low {
		msg := fmt.Sprintf("Disk free %.1f GiB (< 100 GiB)", float64(free)/float64(1<<30))
		s.log.Warn("scheduler: low disk", "free_bytes", free)
		if s.notify != nil {
			if err := s.notify.Notify(ctx, msg); err != nil {
				s.log.Error("scheduler: low-disk alert", "err", err)
			}
		}
	}

	return json.Marshal(map[string]any{
		"deleted_local": len(res.DeletedLocal),
		"deleted_r2":    len(res.DeletedR2),
		"skipped":       len(res.Skipped),
		"errors":        len(res.Errors),
		"low_disk":      low,
		"free_bytes":    free,
	})
}

func (s *Scheduler) loadDueAssets(ctx context.Context, now time.Time) ([]storage.CleanupItem, error) {
	cutoff := now.UTC().Format(time.RFC3339Nano)
	rows, err := s.db.QueryContext(ctx, `
SELECT id, path, COALESCE(r2_key, ''), delete_after
FROM assets
WHERE delete_after IS NOT NULL AND delete_after <= ?`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []storage.CleanupItem
	for rows.Next() {
		var (
			id, path, r2Key, deleteAfter string
		)
		if err := rows.Scan(&id, &path, &r2Key, &deleteAfter); err != nil {
			return nil, err
		}
		da, err := time.Parse(time.RFC3339Nano, deleteAfter)
		if err != nil {
			da, err = time.Parse(time.RFC3339, deleteAfter)
		}
		if err != nil {
			return nil, fmt.Errorf("scheduler: asset %s delete_after %q: %w", id, deleteAfter, err)
		}
		items = append(items, storage.CleanupItem{
			ID:          id,
			Path:        path,
			R2Key:       r2Key,
			DeleteAfter: da,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	_ = sql.ErrNoRows
	return items, nil
}
