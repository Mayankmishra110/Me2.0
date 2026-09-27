// Package queue is a durable job queue backed by the `jobs` table
// (docs/ARCHITECTURE.md §5, docs/SPEC.md §3). SQLite is the source of
// truth: there is no separate in-memory queue. Handlers must be safe to run
// at-least-once — a crashed worker's lease expires and the job is reclaimed.
package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Resource is a worker pool class. It bounds concurrency to protect the
// 16 GB laptop: heavy=1 (render/TTS/STT/local LLM), light=4, net=2 (uploads).
type Resource string

const (
	ResourceHeavy Resource = "heavy"
	ResourceLight Resource = "light"
	ResourceNet   Resource = "net"
)

func (r Resource) valid() bool {
	switch r {
	case ResourceHeavy, ResourceLight, ResourceNet:
		return true
	default:
		return false
	}
}

// Handler processes one job and returns its result payload. Handlers must
// be idempotent-safe: the queue delivers jobs at-least-once (a worker can
// die mid-job and the job is reclaimed after its lease expires), and a
// handler may also be re-run after an ordinary failure. Return
// Permanent(err) for an error that must not be retried.
type Handler func(ctx context.Context, job Job) (result json.RawMessage, err error)

// Job mirrors a row of the `jobs` table.
type Job struct {
	ID          string
	Type        string
	Status      string
	Resource    Resource
	Priority    int
	Payload     json.RawMessage
	Result      json.RawMessage
	RunAt       time.Time
	Attempts    int
	MaxAttempts int
	LeaseUntil  *time.Time
	Worker      string
	LastError   string
	ParentID    *string
	ContentID   *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type registration struct {
	resource    Resource
	maxAttempts int
	handler     Handler
}

// Queue is the durable job queue. The jobs table is the only source of
// truth; Queue holds no job state itself besides the type registry.
type Queue struct {
	db         *sql.DB
	log        *slog.Logger
	now        func() time.Time
	ids        *ulidGen
	workerName string

	regMu sync.RWMutex
	types map[string]registration
}

// Option configures a Queue at construction.
type Option func(*Queue)

// WithClock overrides the queue's notion of "now". Tests use this to
// control lease expiry and backoff timing deterministically, without real
// sleeps.
func WithClock(now func() time.Time) Option {
	return func(q *Queue) { q.now = now }
}

// WithLogger sets the logger; the default is slog.Default().
func WithLogger(l *slog.Logger) Option {
	return func(q *Queue) { q.log = l }
}

// WithWorkerName sets the identity written to jobs.worker for jobs claimed
// by this process (defaults to a host:pid-derived name if unset by callers;
// here it simply defaults to "queue").
func WithWorkerName(name string) Option {
	return func(q *Queue) { q.workerName = name }
}

// New creates a Queue over an already-open, already-migrated database
// handle (internal/db.Open + db.Migrate).
func New(database *sql.DB, opts ...Option) *Queue {
	q := &Queue{
		db:         database,
		log:        slog.Default(),
		now:        time.Now,
		workerName: "queue",
		types:      make(map[string]registration),
	}
	for _, opt := range opts {
		opt(q)
	}
	q.ids = newULIDGen(q.now)
	return q
}

// Register declares a job type: which resource pool it runs on, how many
// attempts it gets before going dead-letter, and the handler that runs it.
// Register is not safe to call concurrently with Enqueue/StartWorkers for
// the same job type; call it during startup wiring before the queue runs.
func (q *Queue) Register(jobType string, res Resource, maxAttempts int, h Handler) {
	if jobType == "" {
		panic("queue: Register: empty jobType")
	}
	if !res.valid() {
		panic(fmt.Sprintf("queue: Register %s: invalid resource %q", jobType, res))
	}
	if maxAttempts < 1 {
		panic(fmt.Sprintf("queue: Register %s: maxAttempts must be >= 1, got %d", jobType, maxAttempts))
	}
	if h == nil {
		panic(fmt.Sprintf("queue: Register %s: nil handler", jobType))
	}
	q.regMu.Lock()
	defer q.regMu.Unlock()
	q.types[jobType] = registration{resource: res, maxAttempts: maxAttempts, handler: h}
}

func (q *Queue) lookup(jobType string) (registration, bool) {
	q.regMu.RLock()
	defer q.regMu.RUnlock()
	reg, ok := q.types[jobType]
	return reg, ok
}

// enqueueConfig collects EnqueueOpt values.
type enqueueConfig struct {
	runAt     time.Time
	hasRunAt  bool
	priority  int
	parentID  string
	contentID string
}

// EnqueueOpt configures one Enqueue call.
type EnqueueOpt func(*enqueueConfig)

// RunAt schedules the job to become claimable at (or after) t. Default is
// now.
func RunAt(t time.Time) EnqueueOpt {
	return func(c *enqueueConfig) { c.runAt = t; c.hasRunAt = true }
}

// Priority sets claim priority; higher claims first. Default 0.
func Priority(n int) EnqueueOpt {
	return func(c *enqueueConfig) { c.priority = n }
}

// Parent links this job to a parent job id (for fan-out/fan-in pipelines).
func Parent(id string) EnqueueOpt {
	return func(c *enqueueConfig) { c.parentID = id }
}

// ContentID links this job to a content_items row.
func ContentID(id string) EnqueueOpt {
	return func(c *enqueueConfig) { c.contentID = id }
}

// Enqueue inserts a new queued job row for a registered jobType. payload is
// JSON-marshaled; pass json.RawMessage to supply pre-encoded JSON as-is.
func (q *Queue) Enqueue(ctx context.Context, jobType string, payload any, opts ...EnqueueOpt) (string, error) {
	reg, ok := q.lookup(jobType)
	if !ok {
		return "", fmt.Errorf("queue: enqueue %s: job type not registered", jobType)
	}

	cfg := enqueueConfig{priority: 0}
	for _, opt := range opts {
		opt(&cfg)
	}
	runAt := q.now()
	if cfg.hasRunAt {
		runAt = cfg.runAt
	}

	var body []byte
	var err error
	if payload == nil {
		body = []byte("{}")
	} else {
		body, err = json.Marshal(payload)
		if err != nil {
			return "", fmt.Errorf("queue: enqueue %s: marshal payload: %w", jobType, err)
		}
	}

	id := q.ids.New()
	now := formatTime(q.now())

	var parentID, contentID any
	if cfg.parentID != "" {
		parentID = cfg.parentID
	}
	if cfg.contentID != "" {
		contentID = cfg.contentID
	}

	_, err = q.db.ExecContext(ctx, `
INSERT INTO jobs (id, type, status, resource, priority, payload, run_at, attempts, max_attempts,
                   parent_id, content_id, created_at, updated_at)
VALUES (?, ?, 'queued', ?, ?, ?, ?, 0, ?, ?, ?, ?, ?)`,
		id, jobType, string(reg.resource), cfg.priority, string(body), formatTime(runAt), reg.maxAttempts,
		parentID, contentID, now, now,
	)
	if err != nil {
		return "", fmt.Errorf("queue: enqueue %s: insert: %w", jobType, err)
	}
	return id, nil
}

// formatTime renders t as the UTC ISO-8601 text the schema expects.
func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// parseTime parses a UTC ISO-8601 timestamp stored by this package.
func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, s)
}

// permanentError marks an error as non-retryable.
type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// Permanent wraps err so the queue does not retry the job: it is finalized
// as "failed" on the first failure instead of being rescheduled.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err: err}
}

// IsPermanent reports whether err (or something it wraps) was produced by
// Permanent.
func IsPermanent(err error) bool {
	var pe *permanentError
	return errors.As(err, &pe)
}
