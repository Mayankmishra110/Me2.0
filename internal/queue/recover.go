package queue

import (
	"context"
	"fmt"
)

// RecoverExpiredLeases requeues jobs stuck in status='running' whose
// lease_until has passed: a worker claimed them and then died (process
// crash, kill -9, power loss) before finishing or renewing the lease via
// heartbeat. It resets them to 'queued' without touching attempts, since
// the handler never got a chance to run to completion — that's not a
// handler failure, it's an abandoned claim.
//
// Call this once at startup (before StartWorkers) to recover from a prior
// crash. It is also safe to call periodically as a background safety net
// for a worker goroutine that hangs without the whole process dying.
func (q *Queue) RecoverExpiredLeases(ctx context.Context) (int, error) {
	now := formatTime(q.now())
	res, err := q.db.ExecContext(ctx, `
UPDATE jobs
SET status='queued', lease_until=NULL, worker=NULL, updated_at=?
WHERE status='running' AND lease_until IS NOT NULL AND lease_until < ?`,
		now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("queue: recover expired leases: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("queue: recover expired leases: rows affected: %w", err)
	}
	return int(n), nil
}
