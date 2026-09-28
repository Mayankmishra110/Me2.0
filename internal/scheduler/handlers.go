package scheduler

import (
	"context"
	"encoding/json"

	"mayank2/internal/queue"
)

const (
	JobSummaryDaily   = "summary.daily"
	JobStorageCleanup = "storage.cleanup"
	JobScoutTopics    = "scout.topics"
	JobAnalyticsPull  = "analytics.pull"
)

// RegisterHandlers wires the scheduler's own job types. JobAnalyticsPull is
// deliberately NOT registered here: internal/analytics.Service owns the
// real "analytics.pull" handler (M2-116 audit finding — this package used
// to also register a lower-maxAttempts placeholder for the identical
// string, and queue.Queue.Register had no duplicate guard, so whichever
// call ran last silently won). cmd/mayank2's run.go is the single place
// that registers analytics.pull now; calling this method a second time for
// that type would panic against queue.Queue's new duplicate guard.
func (s *Scheduler) RegisterHandlers() {
	s.q.Register(JobSummaryDaily, queue.ResourceLight, 3, s.handleSummaryDaily)
	s.q.Register(JobStorageCleanup, queue.ResourceLight, 3, s.handleStorageCleanup)
	s.q.Register(JobScoutTopics, queue.ResourceLight, 3, placeholderHandler(JobScoutTopics))
}

func placeholderHandler(jobType string) queue.Handler {
	return func(ctx context.Context, job queue.Job) (json.RawMessage, error) {
		_ = ctx
		_ = job
		return json.Marshal(map[string]string{
			"status": "placeholder",
			"type":   jobType,
		})
	}
}
