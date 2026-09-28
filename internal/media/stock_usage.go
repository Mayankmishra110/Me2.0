package media

import (
	"context"
	"errors"
	"sync"
	"time"
)

// UsageStore remembers which stock clip was used on which channel and when,
// so a clip is not reused on one channel within the reuse window (30 days).
// The SQLite implementation lands after M2-102; MemoryUsageStore serves tests.
type UsageStore interface {
	// LastUsed returns the most recent use of provider/assetID on channelID.
	LastUsed(ctx context.Context, channelID, provider, assetID string) (at time.Time, ok bool, err error)
	// RecordUse stores a use at time at.
	RecordUse(ctx context.Context, channelID, provider, assetID string, at time.Time) error
}

type usageKey struct{ channel, provider, asset string }

// MemoryUsageStore is an in-process UsageStore. Safe for concurrent use.
type MemoryUsageStore struct {
	mu   sync.Mutex
	last map[usageKey]time.Time
}

// NewMemoryUsageStore returns an empty store.
func NewMemoryUsageStore() *MemoryUsageStore {
	return &MemoryUsageStore{last: map[usageKey]time.Time{}}
}

// LastUsed implements UsageStore.
func (m *MemoryUsageStore) LastUsed(ctx context.Context, channelID, provider, assetID string) (time.Time, bool, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	at, ok := m.last[usageKey{channelID, provider, assetID}]
	return at, ok, nil
}

// RecordUse implements UsageStore. It keeps the latest time per key.
func (m *MemoryUsageStore) RecordUse(ctx context.Context, channelID, provider, assetID string, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if channelID == "" || provider == "" || assetID == "" {
		return errors.New("record usage: channel, provider and asset id are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := usageKey{channelID, provider, assetID}
	if prev, ok := m.last[k]; !ok || at.After(prev) {
		m.last[k] = at
	}
	return nil
}
