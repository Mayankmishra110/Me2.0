package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mayank2/internal/events"
)

func TestSSE_receivesEmittedEvent(t *testing.T) {
	sqlDB := testDB(t)
	bus := events.New(sqlDB)
	h := testServer(t, sqlDB, bus).Handler()
	c := login(t, h)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/events/stream", nil)
	req.AddCookie(c)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(rr, req)
	}()

	// Give Subscribe a moment to register.
	time.Sleep(50 * time.Millisecond)

	ev, err := bus.Emit(context.Background(), events.ActorUser, events.KindApprovalDecided, "ap1", "decided", map[string]any{
		"id": "ap1",
	})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	var body string
	for time.Now().Before(deadline) {
		body = rr.Body.String()
		if strings.Contains(body, "event: approval.decided") && strings.Contains(body, ev.ID) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done

	if !strings.Contains(body, "event: approval.decided") {
		t.Fatalf("SSE body missing event:\n%s", body)
	}
	if !strings.Contains(body, `"kind":"approval.decided"`) && !strings.Contains(body, ev.ID) {
		t.Fatalf("SSE body missing payload:\n%s", body)
	}

	// Sanity: payload is JSON after "data: "
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "data: ") {
			var parsed events.Event
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &parsed); err != nil {
				t.Fatalf("data json: %v (%s)", err, line)
			}
			if parsed.Kind != events.KindApprovalDecided {
				t.Fatalf("kind=%q", parsed.Kind)
			}
			return
		}
	}
	t.Fatalf("no data line in:\n%s", body)
}

func TestSSE_requiresAuth(t *testing.T) {
	sqlDB := testDB(t)
	bus := events.New(sqlDB)
	h := testServer(t, sqlDB, bus).Handler()

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/events/stream", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status %d want 401", rr.Code)
	}
}
