package queue

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"mayank2/internal/db"
)

// fakeClock is a controllable, concurrency-safe clock for deterministic
// tests: no real sleeps stand in for minutes/hours of backoff or lease TTL.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{now: start}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// testDB opens and migrates a fresh SQLite database in a temp dir, matching
// how the daemon opens it (WAL, single connection).
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "queue-test.db")
	sqlDB, err := db.Open(ctx, path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := db.Migrate(ctx, sqlDB); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	return sqlDB
}

// testQueue returns a Queue wired to a fresh migrated DB and a fake clock
// the test controls.
func testQueue(t *testing.T) (*Queue, *fakeClock) {
	t.Helper()
	clock := newFakeClock(time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC))
	q := New(testDB(t), WithClock(clock.Now), WithWorkerName("test"))
	return q, clock
}

// jobStatus reads back a job's status/attempts/lease/worker/last_error for
// assertions.
type jobRow struct {
	status     string
	attempts   int
	leaseUntil sql.NullString
	worker     sql.NullString
	lastError  sql.NullString
	runAt      string
	result     sql.NullString
}

func readJob(t *testing.T, database *sql.DB, id string) jobRow {
	t.Helper()
	var r jobRow
	err := database.QueryRow(`
SELECT status, attempts, lease_until, worker, last_error, run_at, result FROM jobs WHERE id=?`, id).
		Scan(&r.status, &r.attempts, &r.leaseUntil, &r.worker, &r.lastError, &r.runAt, &r.result)
	if err != nil {
		t.Fatalf("readJob %s: %v", id, err)
	}
	return r
}

func countEvents(t *testing.T, database *sql.DB, kind, ref string) int {
	t.Helper()
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND ref=?`, kind, ref).Scan(&n); err != nil {
		t.Fatalf("countEvents: %v", err)
	}
	return n
}
