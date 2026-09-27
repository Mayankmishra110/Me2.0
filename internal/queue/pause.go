package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Pause/resume flags live in the `settings` table (docs/ARCHITECTURE.md §4,
// "pause flags"). "pauseAllKey" is a global stop; "pauseResourceKey" pauses
// one resource-class pool. This package owns the resource-class pools, so
// that is the granularity of "per agent" pause it implements; a per-job-type
// or per-channel pause, if ever wanted, layers on top of this in
// httpapi/telegram (see ticket Notes).
const pauseAllKey = "pause:all"

func pauseResourceKey(res Resource) string {
	return fmt.Sprintf("pause:resource:%s", res)
}

// isPaused reports whether workers for res should currently skip claiming.
func (q *Queue) isPaused(ctx context.Context, res Resource) (bool, error) {
	all, err := q.settingBool(ctx, pauseAllKey)
	if err != nil {
		return false, err
	}
	if all {
		return true, nil
	}
	return q.settingBool(ctx, pauseResourceKey(res))
}

func (q *Queue) settingBool(ctx context.Context, key string) (bool, error) {
	var value string
	err := q.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("queue: read setting %s: %w", key, err)
	}
	return value == "1", nil
}

func (q *Queue) setSetting(ctx context.Context, key string, on bool) error {
	value := "0"
	if on {
		value = "1"
	}
	_, err := q.db.ExecContext(ctx, `
INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		key, value,
	)
	if err != nil {
		return fmt.Errorf("queue: set setting %s: %w", key, err)
	}
	return nil
}

// PauseAll stops every worker pool from claiming new work. Jobs already
// running finish normally.
func (q *Queue) PauseAll(ctx context.Context) error { return q.setSetting(ctx, pauseAllKey, true) }

// ResumeAll clears a global pause.
func (q *Queue) ResumeAll(ctx context.Context) error { return q.setSetting(ctx, pauseAllKey, false) }

// PauseResource stops one resource-class pool from claiming new work.
func (q *Queue) PauseResource(ctx context.Context, res Resource) error {
	return q.setSetting(ctx, pauseResourceKey(res), true)
}

// ResumeResource clears a resource-class pause.
func (q *Queue) ResumeResource(ctx context.Context, res Resource) error {
	return q.setSetting(ctx, pauseResourceKey(res), false)
}
