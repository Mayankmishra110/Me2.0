package media

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestMemoryUsageStore(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryUsageStore()
	t0 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	if _, ok, err := m.LastUsed(ctx, "c", "pexels", "1"); ok || err != nil {
		t.Fatalf("empty store: ok=%v err=%v", ok, err)
	}
	tests := []struct {
		name    string
		channel string
		at      time.Time
		wantErr bool
	}{
		{"first use", "c", t0, false},
		{"later use wins", "c", t0.Add(48 * time.Hour), false},
		{"older use ignored", "c", t0.Add(24 * time.Hour), false},
		{"missing channel", "", t0, true},
	}
	for _, tt := range tests {
		err := m.RecordUse(ctx, tt.channel, "pexels", "1", tt.at)
		if (err != nil) != tt.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tt.name, err, tt.wantErr)
		}
	}
	got, ok, err := m.LastUsed(ctx, "c", "pexels", "1")
	if err != nil || !ok || !got.Equal(t0.Add(48*time.Hour)) {
		t.Errorf("LastUsed = %v %v %v, want t0+48h", got, ok, err)
	}
	if _, ok, _ := m.LastUsed(ctx, "other", "pexels", "1"); ok {
		t.Error("use leaked across channels")
	}
	if _, ok, _ := m.LastUsed(ctx, "c", "pixabay", "1"); ok {
		t.Error("use leaked across providers")
	}

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := m.LastUsed(cctx, "c", "pexels", "1"); err == nil {
		t.Error("LastUsed ignored cancelled context")
	}
	if err := m.RecordUse(cctx, "c", "pexels", "1", t0); err == nil {
		t.Error("RecordUse ignored cancelled context")
	}
}

func TestMemoryUsageStoreConcurrent(t *testing.T) {
	m := NewMemoryUsageStore()
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.RecordUse(context.Background(), "c", "p", "1", time.Unix(int64(i), 0))
			_, _, _ = m.LastUsed(context.Background(), "c", "p", "1")
		}()
	}
	wg.Wait()
	got, _, _ := m.LastUsed(context.Background(), "c", "p", "1")
	if got.Unix() != 49 {
		t.Errorf("latest = %d, want 49", got.Unix())
	}
}
