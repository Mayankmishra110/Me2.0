package scheduler

import (
	"mayank2/internal/queue"
)

const (
	JobSummaryDaily   = "summary.daily"
	JobStorageCleanup = "storage.cleanup"
	JobScoutTopics    = "scout.topics"
	JobAnalyticsPull  = "analytics.pull"
)

// RegisterHandlers wires the scheduler's own job types. JobAnalyticsPull and
// JobScoutTopics are deliberately NOT registered here: internal/analytics.Service
// and internal/content.Scout own the real "analytics.pull" (M2-116) and
// "scout.topics" (M2-119) handlers respectively (both audit findings — this
// package used to also register a lower-maxAttempts placeholder for each
// identical string, and queue.Queue.Register had no duplicate guard, so
// whichever call ran last silently won). cmd/mayank2's run.go is the single
// place that registers them now; calling this method a second time for
// either type would panic against queue.Queue's new duplicate guard.
func (s *Scheduler) RegisterHandlers() {
	s.q.Register(JobSummaryDaily, queue.ResourceLight, 3, s.handleSummaryDaily)
	s.q.Register(JobStorageCleanup, queue.ResourceLight, 3, s.handleStorageCleanup)
}
