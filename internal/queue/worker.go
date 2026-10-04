package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// backoffSchedule is the retry delay by attempt count (1-indexed): the
// first failure waits 1m, the second 5m, the third 30m, the fourth and any
// later failure 2h. (docs/ARCHITECTURE.md §5)
var backoffSchedule = []time.Duration{
	1 * time.Minute,
	5 * time.Minute,
	30 * time.Minute,
	2 * time.Hour,
}

func backoffFor(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	idx := attempts - 1
	if idx >= len(backoffSchedule) {
		idx = len(backoffSchedule) - 1
	}
	return backoffSchedule[idx]
}

// WorkerPoolConfig sizes the resource-class pools and their timing. Worker
// counts come from config.QueueConfig (heavy_workers/light_workers/net_workers).
type WorkerPoolConfig struct {
	HeavyWorkers int
	LightWorkers int
	NetWorkers   int

	// PollInterval is how often an idle worker checks for claimable work.
	// Default 1s.
	PollInterval time.Duration
	// LeaseTTL is how long a claim is held before it's considered abandoned.
	// Default 5m (SPEC).
	LeaseTTL time.Duration
	// HeartbeatInterval is how often a running job's lease is renewed.
	// Default 60s (SPEC).
	HeartbeatInterval time.Duration
}

func (c WorkerPoolConfig) withDefaults() WorkerPoolConfig {
	if c.PollInterval <= 0 {
		c.PollInterval = time.Second
	}
	if c.LeaseTTL <= 0 {
		c.LeaseTTL = 5 * time.Minute
	}
	if c.HeartbeatInterval <= 0 {
		c.HeartbeatInterval = 60 * time.Second
	}
	return c
}

func (c WorkerPoolConfig) countFor(res Resource) int {
	switch res {
	case ResourceHeavy:
		return c.HeavyWorkers
	case ResourceLight:
		return c.LightWorkers
	case ResourceNet:
		return c.NetWorkers
	default:
		return 0
	}
}

// StartWorkers launches the resource-class worker pools and returns a
// WaitGroup callers can Wait() on after cancelling ctx for a clean shutdown.
// Workers that find no claimable job, or that are paused, sleep for
// PollInterval and try again.
func (q *Queue) StartWorkers(ctx context.Context, cfg WorkerPoolConfig) *sync.WaitGroup {
	cfg = cfg.withDefaults()
	wg := &sync.WaitGroup{}
	for _, res := range []Resource{ResourceHeavy, ResourceLight, ResourceNet} {
		n := cfg.countFor(res)
		for i := 0; i < n; i++ {
			workerID := fmt.Sprintf("%s-%s-%d", q.workerName, res, i)
			wg.Add(1)
			go func(res Resource, workerID string) {
				defer wg.Done()
				q.workerLoop(ctx, res, workerID, cfg)
			}(res, workerID)
		}
	}
	return wg
}

func (q *Queue) workerLoop(ctx context.Context, res Resource, workerID string, cfg WorkerPoolConfig) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		paused, err := q.isPaused(ctx, res)
		if err != nil {
			q.log.ErrorContext(ctx, "queue: pause check failed", "resource", res, "error", err)
			if !sleepCtx(ctx, cfg.PollInterval) {
				return
			}
			continue
		}
		if paused {
			if !sleepCtx(ctx, cfg.PollInterval) {
				return
			}
			continue
		}

		job, ok, err := q.claim(ctx, res, workerID, cfg.LeaseTTL)
		if err != nil {
			q.log.ErrorContext(ctx, "queue: claim failed", "resource", res, "error", err)
			if !sleepCtx(ctx, cfg.PollInterval) {
				return
			}
			continue
		}
		if !ok {
			if !sleepCtx(ctx, cfg.PollInterval) {
				return
			}
			continue
		}

		q.runJob(ctx, job, workerID, cfg)
	}
}

// sleepCtx sleeps for d or until ctx is done, returning false if ctx ended
// the wait.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// claim atomically takes the highest-priority, oldest claimable job for
// res, if any. The UPDATE...RETURNING is atomic against the single SQLite
// writer connection (internal/db.Open sets SetMaxOpenConns(1)), so
// concurrent callers never claim the same row twice.
func (q *Queue) claim(ctx context.Context, res Resource, workerID string, leaseTTL time.Duration) (Job, bool, error) {
	now := q.now()
	leaseUntil := now.Add(leaseTTL)

	row := q.db.QueryRowContext(ctx, `
UPDATE jobs SET status='running', lease_until=?, worker=?, updated_at=?
WHERE id = (
    SELECT id FROM jobs
    WHERE status='queued' AND resource=? AND run_at<=?
    ORDER BY priority DESC, run_at ASC
    LIMIT 1
)
RETURNING id, type, status, resource, priority, payload, result, run_at, attempts, max_attempts,
          lease_until, worker, last_error, parent_id, content_id, created_at, updated_at`,
		formatTime(leaseUntil), workerID, formatTime(now),
		string(res), formatTime(now),
	)

	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, fmt.Errorf("queue: claim %s: %w", res, err)
	}
	return job, true, nil
}

func scanJob(row *sql.Row) (Job, error) {
	var (
		j                                   Job
		resource, payload                   string
		result, leaseUntil, worker, lastErr sql.NullString
		parentID, contentID                 sql.NullString
		runAt, createdAt, updatedAt         string
	)
	// payload/result are scanned into plain strings, not json.RawMessage
	// directly: database/sql's reflect fallback only special-cases the
	// exact type *[]byte, and a driver that hands back a Go string for a
	// TEXT column (as modernc.org/sqlite does) won't convert into a named
	// []byte type like json.RawMessage through that path.
	if err := row.Scan(&j.ID, &j.Type, &j.Status, &resource, &j.Priority, &payload, &result,
		&runAt, &j.Attempts, &j.MaxAttempts, &leaseUntil, &worker, &lastErr,
		&parentID, &contentID, &createdAt, &updatedAt); err != nil {
		return Job{}, err
	}
	j.Payload = json.RawMessage(payload)
	j.Resource = Resource(resource)
	if result.Valid {
		j.Result = json.RawMessage(result.String)
	}
	if leaseUntil.Valid {
		t, err := parseTime(leaseUntil.String)
		if err != nil {
			return Job{}, fmt.Errorf("parse lease_until: %w", err)
		}
		j.LeaseUntil = &t
	}
	j.Worker = worker.String
	j.LastError = lastErr.String
	if parentID.Valid {
		v := parentID.String
		j.ParentID = &v
	}
	if contentID.Valid {
		v := contentID.String
		j.ContentID = &v
	}
	var err error
	if j.RunAt, err = parseTime(runAt); err != nil {
		return Job{}, fmt.Errorf("parse run_at: %w", err)
	}
	if j.CreatedAt, err = parseTime(createdAt); err != nil {
		return Job{}, fmt.Errorf("parse created_at: %w", err)
	}
	if j.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return Job{}, fmt.Errorf("parse updated_at: %w", err)
	}
	return j, nil
}

// runJob executes a claimed job's handler with a live heartbeat, then
// finalizes the row (succeeded/queued-for-retry/failed/dead).
func (q *Queue) runJob(ctx context.Context, job Job, workerID string, cfg WorkerPoolConfig) {
	reg, ok := q.lookup(job.Type)
	if !ok {
		// Registered at Enqueue time but no longer registered now (binary
		// changed under it, or a bug); fail loudly but don't crash the pool.
		finalizeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		q.finalizeError(finalizeCtx, job, workerID, job.MaxAttempts,
			fmt.Errorf("queue: no handler registered for job type %q", job.Type))
		return
	}

	hbCtx, stopHB := context.WithCancel(ctx)
	var hbWG sync.WaitGroup
	hbWG.Add(1)
	go func() {
		defer hbWG.Done()
		q.heartbeat(hbCtx, job.ID, workerID, cfg.HeartbeatInterval, cfg.LeaseTTL)
	}()

	result, err := q.invoke(ctx, reg.handler, job)

	stopHB()
	hbWG.Wait()

	// A job that has already run (successfully or not) must be finalized
	// even if the worker pool's ctx was just cancelled for shutdown -
	// otherwise a job that finished a split second before shutdown would
	// be left "running" and only recovered after its 5-minute lease
	// expires. Finalization is a couple of small local SQLite writes, so a
	// short bounded timeout is enough; it never waits on the network.
	finalizeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err == nil {
		q.finalizeSuccess(finalizeCtx, job.ID, result)
		return
	}
	if IsPermanent(err) {
		q.finalizePermanent(finalizeCtx, job, err)
		return
	}
	q.finalizeError(finalizeCtx, job, workerID, job.MaxAttempts, err)
}

// invoke runs the handler, converting a panic into a retryable error so one
// bad handler can't take the daemon down.
func (q *Queue) invoke(ctx context.Context, h Handler, job Job) (result json.RawMessage, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("queue: handler panic for job %s (%s): %v", job.ID, job.Type, r)
		}
	}()
	return h(ctx, job)
}

// heartbeat periodically extends lease_until for a running job until ctx is
// cancelled (the job finished or the process is shutting down).
func (q *Queue) heartbeat(ctx context.Context, jobID, workerID string, interval, leaseTTL time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			newLease := formatTime(q.now().Add(leaseTTL))
			if _, err := q.db.ExecContext(context.Background(), `
UPDATE jobs SET lease_until=?, updated_at=? WHERE id=? AND worker=? AND status='running'`,
				newLease, formatTime(q.now()), jobID, workerID,
			); err != nil {
				q.log.Error("queue: heartbeat failed", "job", jobID, "error", err)
			}
		}
	}
}

func (q *Queue) finalizeSuccess(ctx context.Context, jobID string, result json.RawMessage) {
	if result == nil {
		result = json.RawMessage("null")
	}
	if _, err := q.db.ExecContext(ctx, `
UPDATE jobs SET status='succeeded', result=?, lease_until=NULL, last_error=NULL, updated_at=?
WHERE id=?`,
		string(result), formatTime(q.now()), jobID,
	); err != nil {
		q.log.Error("queue: finalizeSuccess failed", "job", jobID, "error", err)
	}
}

// finalizePermanent marks a job failed (terminal, no retry) for a
// queue.Permanent(err) result. No dead-letter alert: this is an expected,
// handled non-retryable failure, not an exhausted-retries situation.
func (q *Queue) finalizePermanent(ctx context.Context, job Job, err error) {
	attempts := job.Attempts + 1
	if _, execErr := q.db.ExecContext(ctx, `
UPDATE jobs SET status='failed', attempts=?, lease_until=NULL, worker=NULL, last_error=?, updated_at=?
WHERE id=?`,
		attempts, err.Error(), formatTime(q.now()), job.ID,
	); execErr != nil {
		q.log.Error("queue: finalizePermanent failed", "job", job.ID, "error", execErr)
	}
}

// finalizeError handles an ordinary (retryable) handler error: reschedule
// with backoff, or go dead-letter + alert event once maxAttempts is
// exhausted.
func (q *Queue) finalizeError(ctx context.Context, job Job, workerID string, maxAttempts int, err error) {
	attempts := job.Attempts + 1
	now := q.now()

	if attempts >= maxAttempts {
		if _, execErr := q.db.ExecContext(ctx, `
UPDATE jobs SET status='dead', attempts=?, lease_until=NULL, worker=NULL, last_error=?, updated_at=?
WHERE id=?`,
			attempts, err.Error(), formatTime(now), job.ID,
		); execErr != nil {
			q.log.Error("queue: finalizeError (dead) failed", "job", job.ID, "error", execErr)
			return
		}
		q.emitDeadLetterAlert(ctx, job, attempts, err)
		return
	}

	nextRun := now.Add(backoffFor(attempts))
	if _, execErr := q.db.ExecContext(ctx, `
UPDATE jobs SET status='queued', attempts=?, run_at=?, lease_until=NULL, worker=NULL, last_error=?, updated_at=?
WHERE id=?`,
		attempts, formatTime(nextRun), err.Error(), formatTime(now), job.ID,
	); execErr != nil {
		q.log.Error("queue: finalizeError (retry) failed", "job", job.ID, "error", execErr)
	}
}

func (q *Queue) emitDeadLetterAlert(ctx context.Context, job Job, attempts int, cause error) {
	data, _ := json.Marshal(map[string]any{
		"job_id":   job.ID,
		"job_type": job.Type,
		"attempts": attempts,
		"error":    cause.Error(),
	})
	id := q.ids.New()
	if _, err := q.db.ExecContext(ctx, `
INSERT INTO events (id, at, actor, kind, ref, message, data) VALUES (?, ?, 'system', 'alert', ?, ?, ?)`,
		id, formatTime(q.now()), job.ID,
		fmt.Sprintf("job %s (%s) exhausted %d attempts and is dead-lettered", job.ID, job.Type, attempts),
		string(data),
	); err != nil {
		q.log.Error("queue: emitDeadLetterAlert failed", "job", job.ID, "error", err)
	}
}
