package events

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

// waitFor polls cond every 2ms until it returns true or timeout elapses,
// failing the test on timeout. Used instead of a fixed sleep so tests don't
// depend on guessing the right delay.
func waitFor(t *testing.T, timeout time.Duration, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for: %s", timeout, msg)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func mustReceive(t *testing.T, ch <-chan Event, timeout time.Duration) Event {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatalf("channel closed while expecting an event")
		}
		return ev
	case <-time.After(timeout):
		t.Fatalf("timed out after %s waiting to receive an event", timeout)
		return Event{}
	}
}

func TestHub_FanOutToMultipleSubscribers(t *testing.T) {
	cases := []struct {
		name        string
		subscribers int
	}{
		{"single subscriber", 1},
		{"few subscribers", 2},
		{"many subscribers", 5},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHub()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			chans := make([]<-chan Event, tc.subscribers)
			for i := range chans {
				ch, unsub := h.Subscribe(ctx)
				chans[i] = ch
				defer unsub()
			}

			want := Event{ID: "evt-1", Kind: KindAlert, Message: "hello"}
			h.Publish(want)

			for i, ch := range chans {
				got := mustReceive(t, ch, time.Second)
				if got.ID != want.ID || got.Kind != want.Kind || got.Message != want.Message {
					t.Fatalf("subscriber %d got %+v want %+v", i, got, want)
				}
			}
		})
	}
}

func TestHub_PublishNeverBlocksOnSlowSubscriber(t *testing.T) {
	h := newHub(2) // small buffer so overflow happens well within the test
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	slowCh, unsub := h.Subscribe(ctx)
	defer unsub()
	// slowCh is deliberately never read from below.

	const n = 20
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < n; i++ {
			h.Publish(Event{ID: fmt.Sprintf("evt-%02d", i), Kind: KindAlert})
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a subscriber that never reads")
	}

	// Buffer of 2, drop-oldest: only the last two published events remain.
	if got := len(slowCh); got != 2 {
		t.Fatalf("len(slowCh)=%d want 2", got)
	}
	first := <-slowCh
	second := <-slowCh
	if first.ID != "evt-18" || second.ID != "evt-19" {
		t.Fatalf("got ids %q, %q want evt-18, evt-19 (drop-oldest)", first.ID, second.ID)
	}
}

func TestHub_ActiveSubscriberUnaffectedBySlowOne(t *testing.T) {
	h := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	slowCh, unsubSlow := h.Subscribe(ctx)
	defer unsubSlow()

	activeCh, unsubActive := h.Subscribe(ctx)

	var mu sync.Mutex
	var got []Event
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		for ev := range activeCh {
			mu.Lock()
			got = append(got, ev)
			mu.Unlock()
		}
	}()

	const n = 10
	for i := 0; i < n; i++ {
		h.Publish(Event{ID: fmt.Sprintf("evt-%02d", i), Kind: KindJobUpdated})
	}

	unsubActive() // closes activeCh, ending the range loop above
	select {
	case <-drainDone:
	case <-time.After(2 * time.Second):
		t.Fatal("active subscriber's drain goroutine did not finish")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != n {
		t.Fatalf("active subscriber received %d events, want %d (got=%v)", len(got), n, got)
	}
	for i, ev := range got {
		want := fmt.Sprintf("evt-%02d", i)
		if ev.ID != want {
			t.Fatalf("event %d id=%q want %q (out of order or dropped)", i, ev.ID, want)
		}
	}

	// The slow subscriber, never drained, must not have blocked delivery to
	// the active one and must be bounded by the hub's buffer.
	if got := len(slowCh); got > subscriberBuffer {
		t.Fatalf("slow subscriber buffered %d events, want <= %d", got, subscriberBuffer)
	}
}

func TestHub_UnsubscribeStopsDeliveryAndClosesChannel(t *testing.T) {
	h := NewHub()
	ch, unsub := h.Subscribe(context.Background())

	unsub()

	select {
	case ev, ok := <-ch:
		if ok {
			t.Fatalf("expected channel closed, got event %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("channel was not closed after unsubscribe")
	}

	// Publishing after unsubscribe must not panic or deliver anything.
	h.Publish(Event{ID: "evt-after-unsub", Kind: KindAlert})
}

func TestHub_UnsubscribeIsIdempotent(t *testing.T) {
	h := NewHub()
	_, unsub := h.Subscribe(context.Background())
	unsub()
	unsub() // must not panic (double close, double delete)
}

func TestHub_ContextCancelUnsubscribes(t *testing.T) {
	h := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	ch, unsub := h.Subscribe(ctx)
	defer unsub()

	cancel()

	waitFor(t, time.Second, "channel closed after context cancel", func() bool {
		select {
		case _, ok := <-ch:
			return !ok
		default:
			return false
		}
	})
}

func TestHub_NoGoroutineLeakAfterUnsubscribe(t *testing.T) {
	h := NewHub()

	runtime.GC()
	baseline := runtime.NumGoroutine()

	const rounds = 50
	for i := 0; i < rounds; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		_, unsub := h.Subscribe(ctx)
		unsub()
		cancel()
	}

	waitFor(t, 500*time.Millisecond, "goroutine count back to baseline", func() bool {
		runtime.GC()
		return runtime.NumGoroutine() <= baseline+1 // small slack for the test runner itself
	})
}
