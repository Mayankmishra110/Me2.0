package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"mayank2/internal/queue"
)

const (
	cleanupAt   = "03:00"
	scoutAt     = "06:00"
	analyticsAt = "07:00"
)

// Enqueuer is the queue surface the scheduler needs.
type Enqueuer interface {
	Enqueue(ctx context.Context, jobType string, payload any, opts ...queue.EnqueueOpt) (string, error)
	Register(jobType string, res queue.Resource, maxAttempts int, h queue.Handler)
}

// Notifier delivers the daily summary text (Telegram wired from cmd).
type Notifier interface {
	Notify(ctx context.Context, text string) error
}

// FreeDiskFunc reports free bytes on the volume containing path.
type FreeDiskFunc func(path string) (uint64, error)

// Config holds schedule inputs from daemon config.
type Config struct {
	Location       *time.Location
	DailySummaryAt string
	DataDir        string
}

type trigger struct {
	jobType string
	at      string
	hour    int
	minute  int
}

// Scheduler wraps robfig/cron/v3 and catch-up watermarks.
type Scheduler struct {
	db       *sql.DB
	q        Enqueuer
	cfg      Config
	loc      *time.Location
	log      *slog.Logger
	now      func() time.Time
	notify   Notifier
	freeDisk FreeDiskFunc

	triggers []trigger
	cron     *cron.Cron

	mu      sync.Mutex
	started bool
}

// Option configures a Scheduler.
type Option func(*Scheduler)

func WithLogger(l *slog.Logger) Option {
	return func(s *Scheduler) {
		if l != nil {
			s.log = l
		}
	}
}

func WithClock(now func() time.Time) Option {
	return func(s *Scheduler) {
		if now != nil {
			s.now = now
		}
	}
}

func WithNotifier(n Notifier) Option {
	return func(s *Scheduler) { s.notify = n }
}

func WithFreeDisk(fn FreeDiskFunc) Option {
	return func(s *Scheduler) { s.freeDisk = fn }
}

func New(database *sql.DB, q Enqueuer, cfg Config, opts ...Option) (*Scheduler, error) {
	if database == nil {
		return nil, fmt.Errorf("scheduler: nil db")
	}
	if q == nil {
		return nil, fmt.Errorf("scheduler: nil enqueuer")
	}
	loc := cfg.Location
	if loc == nil {
		loc = time.UTC
	}
	summaryAt := cfg.DailySummaryAt
	if summaryAt == "" {
		return nil, fmt.Errorf("scheduler: telegram.daily_summary_at is required")
	}
	summaryH, summaryM, err := parseHHMM(summaryAt)
	if err != nil {
		return nil, err
	}
	mk := func(jobType, at string) (trigger, error) {
		h, m, err := parseHHMM(at)
		if err != nil {
			return trigger{}, err
		}
		return trigger{jobType: jobType, at: at, hour: h, minute: m}, nil
	}
	cleanupTr, err := mk(JobStorageCleanup, cleanupAt)
	if err != nil {
		return nil, err
	}
	scoutTr, err := mk(JobScoutTopics, scoutAt)
	if err != nil {
		return nil, err
	}
	analyticsTr, err := mk(JobAnalyticsPull, analyticsAt)
	if err != nil {
		return nil, err
	}

	s := &Scheduler{
		db:  database,
		q:   q,
		cfg: cfg,
		loc: loc,
		log: slog.Default(),
		now: time.Now,
		triggers: []trigger{
			{jobType: JobSummaryDaily, at: summaryAt, hour: summaryH, minute: summaryM},
			cleanupTr,
			scoutTr,
			analyticsTr,
		},
	}
	for _, opt := range opts {
		opt(s)
	}
	s.cron = cron.New(cron.WithLocation(loc), cron.WithLogger(cron.PrintfLogger(&cronLog{log: s.log})))
	return s, nil
}

func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return fmt.Errorf("scheduler: already started")
	}
	if err := s.catchUpLocked(ctx); err != nil {
		return err
	}
	for _, tr := range s.triggers {
		spec, err := dailyCronSpec(tr.at)
		if err != nil {
			return err
		}
		jobType := tr.jobType
		at := tr.at
		_, err = s.cron.AddFunc(spec, func() {
			s.onCronFire(jobType, at)
		})
		if err != nil {
			return fmt.Errorf("scheduler: add cron %s: %w", jobType, err)
		}
	}
	s.cron.Start()
	s.started = true
	s.log.Info("scheduler: started", "tz", s.loc.String(), "triggers", len(s.triggers))
	return nil
}

func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return
	}
	stopCtx := s.cron.Stop()
	<-stopCtx.Done()
	s.started = false
}

func (s *Scheduler) onCronFire(jobType, at string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	hour, min, err := parseHHMM(at)
	if err != nil {
		s.log.Error("scheduler: cron fire bad time", "job", jobType, "err", err)
		return
	}
	slot := lastScheduledAt(s.now(), s.loc, hour, min)
	last, ok, err := s.getLastFire(ctx, jobType)
	if err != nil {
		s.log.Error("scheduler: cron watermark read", "job", jobType, "err", err)
		return
	}
	if ok && !last.Before(slot) {
		s.log.Info("scheduler: cron skip (already fired)", "job", jobType, "slot", slot)
		return
	}
	if err := s.enqueueTrigger(ctx, jobType, "cron"); err != nil {
		s.log.Error("scheduler: cron enqueue", "job", jobType, "err", err)
		return
	}
	if err := s.setLastFire(ctx, jobType, slot); err != nil {
		s.log.Error("scheduler: cron watermark write", "job", jobType, "err", err)
		return
	}
	s.log.Info("scheduler: cron enqueue", "job", jobType, "slot", slot)
}

func (s *Scheduler) enqueueTrigger(ctx context.Context, jobType, trigger string) error {
	_, err := s.q.Enqueue(ctx, jobType, map[string]string{"trigger": trigger})
	if err != nil {
		return fmt.Errorf("scheduler: enqueue %s (%s): %w", jobType, trigger, err)
	}
	return nil
}

type cronLog struct{ log *slog.Logger }

func (c *cronLog) Printf(format string, v ...any) {
	c.log.Debug(fmt.Sprintf(format, v...))
}
