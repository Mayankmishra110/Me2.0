package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

func lastFireKey(jobType string) string {
	return "scheduler:last:" + jobType
}

func (s *Scheduler) getLastFire(ctx context.Context, jobType string) (time.Time, bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, lastFireKey(jobType)).Scan(&raw)
	if err == sql.ErrNoRows {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("scheduler: read last fire %s: %w", jobType, err)
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t, err = time.Parse(time.RFC3339, raw)
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("scheduler: parse last fire %s %q: %w", jobType, raw, err)
	}
	return t, true, nil
}

func (s *Scheduler) setLastFire(ctx context.Context, jobType string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		lastFireKey(jobType), at.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("scheduler: write last fire %s: %w", jobType, err)
	}
	return nil
}
