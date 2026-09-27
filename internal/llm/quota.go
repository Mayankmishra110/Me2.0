package llm

import (
	"sync"
	"time"
)

// QuotaTracker tracks providers marked unavailable until a reset time.
// Mirrors the future DB `quotas` table semantics without touching migrations
// (out of this ticket's touches).
type QuotaTracker struct {
	mu   sync.Mutex
	down map[string]quotaEntry
	now  func() time.Time
}

type quotaEntry struct {
	Until  time.Time
	Reason string
}

// NewQuotaTracker returns an empty tracker.
func NewQuotaTracker() *QuotaTracker {
	return &QuotaTracker{
		down: map[string]quotaEntry{},
		now:  time.Now,
	}
}

// MarkUnavailable records that provider is down until until.
func (q *QuotaTracker) MarkUnavailable(provider string, until time.Time, reason string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.down[provider] = quotaEntry{Until: until, Reason: reason}
}

// Available reports whether provider may be tried now.
func (q *QuotaTracker) Available(provider string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.now
	if now == nil {
		now = time.Now
	}
	e, ok := q.down[provider]
	if !ok {
		return true
	}
	if !now().Before(e.Until) {
		delete(q.down, provider)
		return true
	}
	return false
}

// Until returns the reset time and reason if the provider is currently down.
func (q *QuotaTracker) Until(provider string) (time.Time, string, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.now
	if now == nil {
		now = time.Now
	}
	e, ok := q.down[provider]
	if !ok || !now().Before(e.Until) {
		return time.Time{}, "", false
	}
	return e.Until, e.Reason, true
}

// SetClock overrides the clock (tests).
func (q *QuotaTracker) SetClock(now func() time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.now = now
}
