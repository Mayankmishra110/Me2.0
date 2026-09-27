package events

import (
	"context"
	"sync"
)

// subscriberBuffer is the per-subscriber channel capacity. A subscriber that
// falls this far behind starts losing its oldest unread events rather than
// ever blocking a Publish call.
const subscriberBuffer = 32

// Hub fans a stream of Events out to any number of subscribers. It is safe
// for concurrent use by multiple publishers and subscribers. A slow or
// abandoned subscriber can never block Publish or delay delivery to other
// subscribers: each subscriber has a small bounded channel, and Publish
// sends to it without blocking, dropping the subscriber's oldest buffered
// event to make room for the new one if it is full.
type Hub struct {
	mu     sync.Mutex
	next   uint64
	subs   map[uint64]chan Event
	buffer int
}

// NewHub returns an empty, ready-to-use Hub with the default per-subscriber
// buffer size.
func NewHub() *Hub {
	return newHub(subscriberBuffer)
}

// newHub is like NewHub but with a caller-chosen buffer size, so tests can
// force overflow deterministically with a small buffer instead of racing a
// large one.
func newHub(buffer int) *Hub {
	return &Hub{subs: make(map[uint64]chan Event), buffer: buffer}
}

// Subscribe registers a new subscriber and returns a receive-only channel of
// future events plus an unsubscribe function. The channel is closed, and no
// further events are delivered to it, as soon as either the unsubscribe
// function is called or ctx is done — whichever happens first. Calling the
// unsubscribe function more than once is safe.
func (h *Hub) Subscribe(ctx context.Context) (<-chan Event, func()) {
	ch := make(chan Event, h.buffer)
	stop := make(chan struct{})

	h.mu.Lock()
	id := h.next
	h.next++
	h.subs[id] = ch
	h.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			close(stop)
			h.mu.Lock()
			delete(h.subs, id)
			h.mu.Unlock()
			close(ch)
		})
	}

	// Watch ctx in its own goroutine so a subscriber that forgets to call
	// unsubscribe (e.g. an SSE handler that just returns when its request
	// context is cancelled) still gets cleaned up. The goroutine itself
	// exits via stop as soon as unsubscribe runs, so it never leaks.
	go func() {
		select {
		case <-ctx.Done():
			unsubscribe()
		case <-stop:
		}
	}()

	return ch, unsubscribe
}

// Publish delivers e to every current subscriber. It never blocks: a
// subscriber whose buffer is full has its oldest queued event dropped to
// make room. Publish holds the Hub's lock for the duration of the fan-out
// (each send is itself non-blocking), so it is safe to call from many
// goroutines at once and concurrently with Subscribe/unsubscribe.
func (h *Hub) Publish(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.subs {
		select {
		case ch <- e:
			continue
		default:
		}
		// Buffer full: drop the oldest queued event for this subscriber,
		// then retry once. If it is still full (a concurrent receive
		// refilled it), drop this event for this subscriber rather than
		// block the publisher.
		select {
		case <-ch:
		default:
		}
		select {
		case ch <- e:
		default:
		}
	}
}
