package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBackoffFor_schedule(t *testing.T) {
	cases := []struct {
		attempts int
		want     time.Duration
	}{
		{0, time.Minute}, // clamped to attempt 1
		{1, time.Minute},
		{2, 5 * time.Minute},
		{3, 30 * time.Minute},
		{4, 2 * time.Hour},
		{5, 2 * time.Hour},
		{100, 2 * time.Hour},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("attempts=%d", tc.attempts), func(t *testing.T) {
			got := backoffFor(tc.attempts)
			if got != tc.want {
				t.Errorf("backoffFor(%d) = %v, want %v", tc.attempts, got, tc.want)
			}
		})
	}
}

// TestClaim_concurrentNeverDoubleRuns proves the atomic claim never hands
// the same queued job to two workers, even under concurrent goroutines.
// Run with -race.
func TestClaim_concurrentNeverDoubleRuns(t *testing.T) {
	q, _ := testQueue(t)
	q.Register("t.claim", ResourceLight, 3, noopHandler)

	id, err := q.Enqueue(context.Background(), "t.claim", map[string]string{})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	const workers = 20
	var wg sync.WaitGroup
	var claimed int32
	var claimedBy string
	var mu sync.Mutex
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			job, ok, err := q.claim(context.Background(), ResourceLight, fmt.Sprintf("w%d", i), 5*time.Minute)
			if err != nil {
				t.Errorf("claim: %v", err)
				return
			}
			if ok {
				atomic.AddInt32(&claimed, 1)
				mu.Lock()
				claimedBy = job.ID
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	if claimed != 1 {
		t.Fatalf("claimed = %d, want exactly 1", claimed)
	}
	if claimedBy != id {
		t.Fatalf("claimed job %q, want %q", claimedBy, id)
	}

	row := readJob(t, q.db, id)
	if row.status != "running" {
		t.Fatalf("status = %q, want running", row.status)
	}
}

func TestClaim_respectsResourceAndRunAt(t *testing.T) {
	q, clock := testQueue(t)
	q.Register("t.heavy", ResourceHeavy, 3, noopHandler)
	q.Register("t.light", ResourceLight, 3, noopHandler)

	// Wrong resource: a light job must not be claimable by the heavy pool.
	_, err := q.Enqueue(context.Background(), "t.light", map[string]string{})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	_, ok, err := q.claim(context.Background(), ResourceHeavy, "w", 5*time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if ok {
		t.Fatalf("claimed a light job from the heavy pool")
	}

	// Future run_at: not claimable yet.
	future := clock.Now().Add(time.Hour)
	_, err = q.Enqueue(context.Background(), "t.heavy", map[string]string{}, RunAt(future))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	_, ok, err = q.claim(context.Background(), ResourceHeavy, "w", 5*time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if ok {
		t.Fatalf("claimed a job scheduled in the future")
	}

	// Advance the clock past run_at: now claimable.
	clock.Set(future.Add(time.Second))
	_, ok, err = q.claim(context.Background(), ResourceHeavy, "w", 5*time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !ok {
		t.Fatalf("expected job to be claimable after run_at passed")
	}
}

func TestFinalizeError_retriesWithBackoffThenDeadLetters(t *testing.T) {
	q, clock := testQueue(t)
	q.Register("t.retry", ResourceLight, 3, noopHandler)
	id, err := q.Enqueue(context.Background(), "t.retry", map[string]string{})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	job, ok, err := q.claim(context.Background(), ResourceLight, "w0", 5*time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}

	// Attempt 1 fails -> queued, run_at = now + 1m.
	q.finalizeError(context.Background(), job, "w0", 3, errors.New("boom 1"))
	row := readJob(t, q.db, id)
	if row.status != "queued" {
		t.Fatalf("after attempt 1: status = %q, want queued", row.status)
	}
	if row.attempts != 1 {
		t.Fatalf("after attempt 1: attempts = %d, want 1", row.attempts)
	}
	wantRunAt := formatTime(clock.Now().Add(time.Minute))
	if row.runAt != wantRunAt {
		t.Fatalf("after attempt 1: run_at = %q, want %q", row.runAt, wantRunAt)
	}
	if row.leaseUntil.Valid {
		t.Fatalf("after attempt 1: lease_until should be cleared, got %v", row.leaseUntil)
	}

	// The backoff run_at is in the future; advance the fake clock (no real
	// sleep) past it before the job becomes claimable again.
	clock.Advance(time.Minute + time.Second)

	// Re-claim (as if reclaimed for attempt 2), fail again with maxAttempts=3.
	job2, ok, err := q.claim(context.Background(), ResourceLight, "w1", 5*time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim #2: ok=%v err=%v", ok, err)
	}
	if job2.Attempts != 1 {
		t.Fatalf("job2.Attempts = %d, want 1", job2.Attempts)
	}
	q.finalizeError(context.Background(), job2, "w1", 3, errors.New("boom 2"))
	row = readJob(t, q.db, id)
	if row.status != "queued" || row.attempts != 2 {
		t.Fatalf("after attempt 2: status=%q attempts=%d, want queued/2", row.status, row.attempts)
	}

	// Attempt 2's backoff is 5m; advance past it too.
	clock.Advance(5*time.Minute + time.Second)

	// Attempt 3 (== maxAttempts) fails -> dead + alert event.
	job3, ok, err := q.claim(context.Background(), ResourceLight, "w2", 5*time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim #3: ok=%v err=%v", ok, err)
	}
	q.finalizeError(context.Background(), job3, "w2", 3, errors.New("boom 3"))
	row = readJob(t, q.db, id)
	if row.status != "dead" {
		t.Fatalf("after attempt 3: status = %q, want dead", row.status)
	}
	if row.attempts != 3 {
		t.Fatalf("after attempt 3: attempts = %d, want 3", row.attempts)
	}
	if n := countEvents(t, q.db, "alert", id); n != 1 {
		t.Fatalf("alert events for job = %d, want 1", n)
	}
}

func TestFinalizePermanent_failsWithoutAlertOrRetry(t *testing.T) {
	q, _ := testQueue(t)
	q.Register("t.permanent", ResourceLight, 5, noopHandler)
	id, err := q.Enqueue(context.Background(), "t.permanent", map[string]string{})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	job, ok, err := q.claim(context.Background(), ResourceLight, "w0", 5*time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}

	q.finalizePermanent(context.Background(), job, Permanent(errors.New("bad payload, never retry")))
	row := readJob(t, q.db, id)
	if row.status != "failed" {
		t.Fatalf("status = %q, want failed", row.status)
	}
	if row.attempts != 1 {
		t.Fatalf("attempts = %d, want 1", row.attempts)
	}
	if n := countEvents(t, q.db, "alert", id); n != 0 {
		t.Fatalf("alert events for permanent failure = %d, want 0", n)
	}
}

func TestFinalizeSuccess_storesResultAndClearsLease(t *testing.T) {
	q, _ := testQueue(t)
	q.Register("t.success", ResourceLight, 3, noopHandler)
	id, err := q.Enqueue(context.Background(), "t.success", map[string]string{})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, ok, err := q.claim(context.Background(), ResourceLight, "w0", 5*time.Minute); err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	q.finalizeSuccess(context.Background(), id, json.RawMessage(`{"ok":true}`))
	row := readJob(t, q.db, id)
	if row.status != "succeeded" {
		t.Fatalf("status = %q, want succeeded", row.status)
	}
	if row.leaseUntil.Valid {
		t.Fatalf("lease_until should be cleared on success")
	}
	if !row.result.Valid || row.result.String != `{"ok":true}` {
		t.Fatalf("result = %v, want {\"ok\":true}", row.result)
	}
}

func TestInvoke_recoversHandlerPanic(t *testing.T) {
	q, _ := testQueue(t)
	panicky := func(ctx context.Context, job Job) (json.RawMessage, error) {
		panic("handler exploded")
	}
	_, err := q.invoke(context.Background(), panicky, Job{ID: "j1", Type: "t.panic"})
	if err == nil {
		t.Fatalf("expected recovered error, got nil")
	}
	if IsPermanent(err) {
		t.Fatalf("panic should be retryable, not permanent")
	}
}

// TestRunJob_panicIsRetryable exercises the full runJob path with a handler
// that panics, proving the job is rescheduled (not crashing the pool, not
// dead-lettered on attempt 1 of maxAttempts=3).
func TestRunJob_panicIsRetryable(t *testing.T) {
	q, _ := testQueue(t)
	q.Register("t.runpanic", ResourceLight, 3, func(ctx context.Context, job Job) (json.RawMessage, error) {
		panic("boom")
	})
	id, err := q.Enqueue(context.Background(), "t.runpanic", map[string]string{})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	job, ok, err := q.claim(context.Background(), ResourceLight, "w0", 5*time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	cfg := WorkerPoolConfig{HeartbeatInterval: time.Hour, LeaseTTL: 5 * time.Minute}.withDefaults()
	q.runJob(context.Background(), job, "w0", cfg)

	row := readJob(t, q.db, id)
	if row.status != "queued" {
		t.Fatalf("status = %q, want queued (retryable)", row.status)
	}
	if row.attempts != 1 {
		t.Fatalf("attempts = %d, want 1", row.attempts)
	}
	if !row.lastError.Valid || row.lastError.String == "" {
		t.Fatalf("expected last_error to mention the panic")
	}
}

// TestCrashRecovery_expiredLeaseIsReclaimedAndRun proves the acceptance
// criterion end to end: a job claimed by a worker that then "dies" (never
// heartbeats, never finishes) is requeued once its lease expires, and a
// real worker picks it up and runs it to completion.
func TestCrashRecovery_expiredLeaseIsReclaimedAndRun(t *testing.T) {
	q, clock := testQueue(t)
	var ran int32
	done := make(chan struct{}, 1)
	q.Register("t.crash", ResourceLight, 3, func(ctx context.Context, job Job) (json.RawMessage, error) {
		atomic.AddInt32(&ran, 1)
		done <- struct{}{}
		return json.RawMessage(`{"ok":true}`), nil
	})

	id, err := q.Enqueue(context.Background(), "t.crash", map[string]string{})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Simulate a worker that claimed the job and then died: claim it
	// directly (bypassing runJob, so no heartbeat and no finalize ever
	// happens), with a short lease.
	leaseTTL := 5 * time.Minute
	_, ok, err := q.claim(context.Background(), ResourceLight, "dead-worker", leaseTTL)
	if err != nil || !ok {
		t.Fatalf("initial claim: ok=%v err=%v", ok, err)
	}
	row := readJob(t, q.db, id)
	if row.status != "running" {
		t.Fatalf("status after claim = %q, want running", row.status)
	}

	// Before the lease expires, recovery must not touch it.
	n, err := q.RecoverExpiredLeases(context.Background())
	if err != nil {
		t.Fatalf("RecoverExpiredLeases (too early): %v", err)
	}
	if n != 0 {
		t.Fatalf("recovered %d jobs before lease expiry, want 0", n)
	}

	// Advance the fake clock past the lease (no real sleep).
	clock.Advance(leaseTTL + time.Second)

	n, err = q.RecoverExpiredLeases(context.Background())
	if err != nil {
		t.Fatalf("RecoverExpiredLeases: %v", err)
	}
	if n != 1 {
		t.Fatalf("recovered %d jobs, want 1", n)
	}
	row = readJob(t, q.db, id)
	if row.status != "queued" {
		t.Fatalf("status after recovery = %q, want queued", row.status)
	}
	if row.attempts != 0 {
		t.Fatalf("attempts after recovery = %d, want 0 (abandoned claim, not a handler failure)", row.attempts)
	}
	if row.leaseUntil.Valid || row.worker.Valid {
		t.Fatalf("lease_until/worker should be cleared, got %v/%v", row.leaseUntil, row.worker)
	}

	// Now a real worker pool should pick it up and run it to completion.
	ctx, cancel := context.WithCancel(context.Background())
	wg := q.StartWorkers(ctx, WorkerPoolConfig{LightWorkers: 1, PollInterval: 5 * time.Millisecond})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("job was not reclaimed and run within 2s")
	}
	cancel()
	wg.Wait()

	if atomic.LoadInt32(&ran) != 1 {
		t.Fatalf("handler ran %d times, want 1", ran)
	}
	row = readJob(t, q.db, id)
	if row.status != "succeeded" {
		t.Fatalf("final status = %q, want succeeded", row.status)
	}
}

func TestPause_globalAndPerResource(t *testing.T) {
	q, _ := testQueue(t)
	ctx := context.Background()

	if paused, err := q.isPaused(ctx, ResourceLight); err != nil || paused {
		t.Fatalf("isPaused before any pause = %v/%v, want false/nil", paused, err)
	}

	if err := q.PauseResource(ctx, ResourceLight); err != nil {
		t.Fatalf("PauseResource: %v", err)
	}
	if paused, err := q.isPaused(ctx, ResourceLight); err != nil || !paused {
		t.Fatalf("isPaused(light) after PauseResource = %v/%v, want true/nil", paused, err)
	}
	if paused, err := q.isPaused(ctx, ResourceHeavy); err != nil || paused {
		t.Fatalf("isPaused(heavy) should be unaffected by light pause: %v/%v", paused, err)
	}
	if err := q.ResumeResource(ctx, ResourceLight); err != nil {
		t.Fatalf("ResumeResource: %v", err)
	}
	if paused, _ := q.isPaused(ctx, ResourceLight); paused {
		t.Fatalf("isPaused(light) after ResumeResource = true, want false")
	}

	if err := q.PauseAll(ctx); err != nil {
		t.Fatalf("PauseAll: %v", err)
	}
	for _, res := range []Resource{ResourceHeavy, ResourceLight, ResourceNet} {
		if paused, err := q.isPaused(ctx, res); err != nil || !paused {
			t.Fatalf("isPaused(%s) after PauseAll = %v/%v, want true/nil", res, paused, err)
		}
	}
	if err := q.ResumeAll(ctx); err != nil {
		t.Fatalf("ResumeAll: %v", err)
	}
	for _, res := range []Resource{ResourceHeavy, ResourceLight, ResourceNet} {
		if paused, _ := q.isPaused(ctx, res); paused {
			t.Fatalf("isPaused(%s) after ResumeAll = true, want false", res)
		}
	}
}

// TestStartWorkers_skipsClaimingWhilePaused proves a paused pool leaves a
// claimable job untouched, and resumes claiming it once unpaused.
func TestStartWorkers_skipsClaimingWhilePaused(t *testing.T) {
	q, _ := testQueue(t)
	ctx := context.Background()
	done := make(chan struct{}, 1)
	q.Register("t.paused", ResourceLight, 3, func(ctx context.Context, job Job) (json.RawMessage, error) {
		done <- struct{}{}
		return nil, nil
	})
	id, err := q.Enqueue(ctx, "t.paused", map[string]string{})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := q.PauseResource(ctx, ResourceLight); err != nil {
		t.Fatalf("PauseResource: %v", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	wg := q.StartWorkers(runCtx, WorkerPoolConfig{LightWorkers: 1, PollInterval: 5 * time.Millisecond})

	select {
	case <-done:
		cancel()
		wg.Wait()
		t.Fatalf("job ran while resource pool was paused")
	case <-time.After(100 * time.Millisecond):
		// expected: nothing happened while paused
	}
	row := readJob(t, q.db, id)
	if row.status != "queued" {
		t.Fatalf("status while paused = %q, want queued", row.status)
	}

	if err := q.ResumeResource(ctx, ResourceLight); err != nil {
		t.Fatalf("ResumeResource: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("job did not run within 2s of resuming")
	}
	cancel()
	wg.Wait()
}
