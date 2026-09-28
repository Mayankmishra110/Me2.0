package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"mayank2/internal/config"
	"mayank2/internal/queue"
)

// testConfig builds a minimal-but-complete config.Config for runDaemon,
// without touching config.yaml/.env on disk: a loopback dashboard listen
// address (no Tailscale dependency), Builder disabled, and no LLM
// providers configured (nothing here should ever need network access).
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	dataDir := t.TempDir()
	return &config.Config{
		DataDir:   dataDir,
		ConfigDir: filepath.Join(dataDir, "config"),
		Queue: config.QueueConfig{
			HeavyWorkers: 1,
			LightWorkers: 2,
			NetWorkers:   1,
		},
		Telegram: config.TelegramConfig{
			DailySummaryAt: "22:30",
		},
		Dashboard: config.DashboardConfig{
			Listen: []string{"127.0.0.1:0"},
		},
		Content: config.ContentConfig{
			RetentionDays: 7,
		},
		Builder: config.BuilderConfig{
			Enabled: false,
		},
	}
}

// TestRunDaemon_integration is the ticket's required practical integration
// test: a real (temp, file-backed) SQLite DB, the whole cmdRun wiring
// (queue + every real handler + scheduler + http + telegram-disabled) built
// exactly as `mayank2 run` builds it, a throwaway job type registered and
// enqueued through the extraRegister hook, and proof it is actually picked
// up and completed by a worker — then a clean, non-leaking shutdown on
// context cancellation.
func TestRunDaemon_integration(t *testing.T) {
	t.Setenv("DASHBOARD_TOKEN", "test-dashboard-token")
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_USER_ID", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")

	cfg := testConfig(t)
	dbPath := filepath.Join(t.TempDir(), "run-integration.db")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	baseline := goroutineCountStable(t)

	ctx, cancel := context.WithCancel(context.Background())

	jobDone := make(chan struct{})
	extra := func(q *queue.Queue) {
		q.Register("test.noop", queue.ResourceLight, 3, func(_ context.Context, job queue.Job) (json.RawMessage, error) {
			close(jobDone)
			return json.RawMessage(`{"ok":true}`), nil
		})
		if _, err := q.Enqueue(context.Background(), "test.noop", map[string]string{"probe": "M2-116"}); err != nil {
			t.Errorf("enqueue test.noop: %v", err)
		}
	}

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- runDaemon(ctx, cfg, dbPath, log, extra)
	}()

	select {
	case <-jobDone:
		// picked up and completed by a worker.
	case err := <-runErrCh:
		t.Fatalf("runDaemon exited early before the test job ran: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("test.noop job was never picked up / completed")
	}

	// Confirm the job row itself is recorded succeeded (not just that our
	// handler ran) — open a second connection to the same file.
	verifyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open verify db: %v", err)
	}
	t.Cleanup(func() { _ = verifyDB.Close() })
	waitForJobStatus(t, verifyDB, "test.noop", "succeeded")

	// Now request shutdown and confirm the daemon drains cleanly.
	cancel()

	select {
	case err := <-runErrCh:
		if err != nil {
			t.Fatalf("runDaemon returned an error on shutdown: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("runDaemon did not shut down within 15s of context cancellation")
	}

	// Bounded-wait goroutine-leak check (no goleak dependency in this repo):
	// give background goroutines a moment to actually unwind, then require
	// we're back near the pre-run baseline instead of climbing with every
	// call to runDaemon.
	assertNoGoroutineLeak(t, baseline)
}

// goroutineCountStable returns a settled NumGoroutine() reading (a couple of
// GCs to let anything already-finishing actually finish) to use as a leak
// baseline.
func goroutineCountStable(t *testing.T) int {
	t.Helper()
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	return runtime.NumGoroutine()
}

// assertNoGoroutineLeak polls NumGoroutine() for up to a few seconds and
// fails only if it never comes back down near baseline — avoids flaking on
// goroutines that are mid-teardown rather than actually leaked.
func assertNoGoroutineLeak(t *testing.T, baseline int) {
	t.Helper()
	const slack = 3 // scheduler/http/db driver internals can lag teardown by a goroutine or two
	deadline := time.Now().Add(5 * time.Second)
	var last int
	for time.Now().Before(deadline) {
		runtime.GC()
		last = runtime.NumGoroutine()
		if last <= baseline+slack {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("possible goroutine leak: baseline=%d, still at %d after shutdown", baseline, last)
}

func waitForJobStatus(t *testing.T, sqlDB *sql.DB, jobType, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		var status string
		err := sqlDB.QueryRowContext(ctx, `SELECT status FROM jobs WHERE type=? ORDER BY created_at DESC LIMIT 1`, jobType).Scan(&status)
		if err == nil && status == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("job type %s never reached status %q (last err=%v, status=%q)", jobType, want, err, status)
		case <-time.After(50 * time.Millisecond):
		}
	}
}
