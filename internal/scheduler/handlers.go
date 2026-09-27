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

func (s *Scheduler) RegisterHandlers() {
	s.q.Register(JobSummaryDaily, queue.ResourceLight, 3, s.handleSummaryDaily)
	s.q.Register(JobStorageCleanup, queue.ResourceLight, 3, s.handleStorageCleanup)
	s.q.Register(JobScoutTopics, queue.ResourceLight, 3, placeholderHandler(JobScoutTopics))
	s.q.Register(JobAnalyticsPull, queue.ResourceNet, 3, placeholderHandler(JobAnalyticsPull))
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
