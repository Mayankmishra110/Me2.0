package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func noopHandler(ctx context.Context, job Job) (json.RawMessage, error) {
	return nil, nil
}

func TestRegister_invalidArgsPanic(t *testing.T) {
	cases := []struct {
		name string
		fn   func(q *Queue)
	}{
		{"empty type", func(q *Queue) { q.Register("", ResourceLight, 1, noopHandler) }},
		{"bad resource", func(q *Queue) { q.Register("t", Resource("gpu"), 1, noopHandler) }},
		{"zero max attempts", func(q *Queue) { q.Register("t", ResourceLight, 0, noopHandler) }},
		{"nil handler", func(q *Queue) { q.Register("t", ResourceLight, 1, nil) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, _ := testQueue(t)
			defer func() {
				if recover() == nil {
					t.Fatalf("expected panic")
				}
			}()
			tc.fn(q)
		})
	}
}

// TestRegister_duplicateJobTypePanics proves M2-116's duplicate-registration
// guard: registering the same jobType twice is a programmer error (the
// analytics.pull collision between internal/scheduler's old placeholder and
// internal/analytics' real handler) and must fail loudly instead of the
// second Register silently overwriting the first via plain map assignment.
// Two distinct types must still both register fine.
func TestRegister_duplicateJobTypePanics(t *testing.T) {
	t.Run("same type twice panics", func(t *testing.T) {
		q, _ := testQueue(t)
		q.Register("t.dup", ResourceLight, 3, noopHandler)
		defer func() {
			r := recover()
			if r == nil {
				t.Fatalf("expected panic on duplicate Register")
			}
			msg := fmt.Sprintf("%v", r)
			if !strings.Contains(msg, "t.dup") {
				t.Errorf("panic message %q does not mention job type", msg)
			}
		}()
		q.Register("t.dup", ResourceNet, 5, noopHandler)
	})

	t.Run("two distinct types both register", func(t *testing.T) {
		q, _ := testQueue(t)
		q.Register("t.one", ResourceLight, 3, noopHandler)
		q.Register("t.two", ResourceNet, 5, noopHandler)

		if _, ok := q.lookup("t.one"); !ok {
			t.Errorf("t.one not registered")
		}
		if _, ok := q.lookup("t.two"); !ok {
			t.Errorf("t.two not registered")
		}
	})
}

func TestEnqueue_requiresRegisteredType(t *testing.T) {
	q, _ := testQueue(t)
	_, err := q.Enqueue(context.Background(), "unregistered.type", map[string]string{"a": "b"})
	if err == nil {
		t.Fatalf("expected error for unregistered job type")
	}
}

func TestEnqueue_badPayload(t *testing.T) {
	q, _ := testQueue(t)
	q.Register("t.badpayload", ResourceLight, 3, noopHandler)
	// func values cannot be JSON-marshaled.
	_, err := q.Enqueue(context.Background(), "t.badpayload", map[string]any{"f": func() {}})
	if err == nil {
		t.Fatalf("expected marshal error")
	}
}

func TestEnqueue_defaultsAndOpts(t *testing.T) {
	q, clock := testQueue(t)
	q.Register("t.defaults", ResourceNet, 5, noopHandler)

	id, err := q.Enqueue(context.Background(), "t.defaults", map[string]string{"k": "v"})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var resource string
	var priority, maxAttempts, attempts int
	var runAt string
	var parentID, contentID *string
	err = q.db.QueryRowContext(context.Background(), `
SELECT resource, priority, max_attempts, attempts, run_at, parent_id, content_id FROM jobs WHERE id=?`, id).
		Scan(&resource, &priority, &maxAttempts, &attempts, &runAt, &parentID, &contentID)
	if err != nil {
		t.Fatalf("query job: %v", err)
	}
	if resource != string(ResourceNet) {
		t.Errorf("resource = %q, want %q", resource, ResourceNet)
	}
	if priority != 0 {
		t.Errorf("priority = %d, want 0", priority)
	}
	if maxAttempts != 5 {
		t.Errorf("max_attempts = %d, want 5", maxAttempts)
	}
	if attempts != 0 {
		t.Errorf("attempts = %d, want 0", attempts)
	}
	if runAt != formatTime(clock.Now()) {
		t.Errorf("run_at = %q, want %q (now)", runAt, formatTime(clock.Now()))
	}
	if parentID != nil || contentID != nil {
		t.Errorf("parentID/contentID = %v/%v, want nil/nil", parentID, contentID)
	}

	// Now exercise every EnqueueOpt.
	future := clock.Now().Add(3 * time.Hour)
	id2, err := q.Enqueue(context.Background(), "t.defaults", map[string]string{"k": "v2"},
		RunAt(future), Priority(7), Parent(id), ContentID("content-123"))
	if err != nil {
		t.Fatalf("Enqueue with opts: %v", err)
	}
	err = q.db.QueryRowContext(context.Background(), `
SELECT resource, priority, run_at, parent_id, content_id FROM jobs WHERE id=?`, id2).
		Scan(&resource, &priority, &runAt, &parentID, &contentID)
	if err != nil {
		t.Fatalf("query job2: %v", err)
	}
	if priority != 7 {
		t.Errorf("priority = %d, want 7", priority)
	}
	if runAt != formatTime(future) {
		t.Errorf("run_at = %q, want %q", runAt, formatTime(future))
	}
	if parentID == nil || *parentID != id {
		t.Errorf("parentID = %v, want %q", parentID, id)
	}
	if contentID == nil || *contentID != "content-123" {
		t.Errorf("contentID = %v, want content-123", contentID)
	}
}

func TestPermanent_wrapsAndUnwraps(t *testing.T) {
	base := errors.New("boom")
	err := Permanent(base)
	if !IsPermanent(err) {
		t.Fatalf("IsPermanent(Permanent(err)) = false, want true")
	}
	if !errors.Is(err, base) {
		t.Fatalf("errors.Is(Permanent(err), base) = false, want true")
	}
	if IsPermanent(base) {
		t.Fatalf("IsPermanent(plain err) = true, want false")
	}
	if Permanent(nil) != nil {
		t.Fatalf("Permanent(nil) should be nil")
	}
}

func TestULIDGen_sortableAndUnique(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	g := newULIDGen(clock.Now)

	ids := make([]string, 0, 100)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := g.New()
		if len(id) != 26 {
			t.Fatalf("id %q length = %d, want 26", id, len(id))
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
		ids = append(ids, id)
		if i%10 == 0 {
			clock.Advance(time.Millisecond)
		}
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] <= ids[i-1] {
			t.Fatalf("ids not strictly increasing: %q then %q", ids[i-1], ids[i])
		}
	}
}
