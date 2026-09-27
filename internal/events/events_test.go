package events_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"mayank2/internal/db"
	"mayank2/internal/events"
)

func newTestBus(t *testing.T) *events.Bus {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mayank2.db")

	sqlDB, err := db.Open(ctx, path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	if _, err := db.Migrate(ctx, sqlDB); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	return events.New(sqlDB)
}

func TestEmit_ValidationErrors(t *testing.T) {
	cases := []struct {
		name  string
		actor events.Actor
		kind  string
	}{
		{"empty kind", events.ActorSystem, ""},
		{"invalid actor", events.Actor("robot"), events.KindAlert},
		{"empty actor", events.Actor(""), events.KindAlert},
	}

	bus := newTestBus(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := bus.Emit(context.Background(), tc.actor, tc.kind, "", "", nil)
			if err == nil {
				t.Fatalf("Emit(actor=%q, kind=%q) = nil error, want error", tc.actor, tc.kind)
			}
		})
	}
}

func TestEmit_PersistsAndBroadcasts(t *testing.T) {
	bus := newTestBus(t)
	ctx := context.Background()

	sub, unsub := bus.Subscribe(ctx)
	defer unsub()

	type payload struct {
		JobID string `json:"job_id"`
	}
	ev, err := bus.Emit(ctx, events.ActorSystem, events.KindJobUpdated, "job-42", "job finished", payload{JobID: "job-42"})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if ev.ID == "" {
		t.Fatal("Emit did not assign an id")
	}
	if ev.At.IsZero() {
		t.Fatal("Emit did not set At")
	}

	select {
	case got := <-sub:
		if got.ID != ev.ID || got.Kind != events.KindJobUpdated || got.Ref != "job-42" || got.Message != "job finished" {
			t.Fatalf("broadcast event=%+v want id/kind/ref/message to match %+v", got, ev)
		}
		var p payload
		if err := json.Unmarshal(got.Data, &p); err != nil {
			t.Fatalf("unmarshal broadcast data: %v", err)
		}
		if p.JobID != "job-42" {
			t.Fatalf("broadcast data job_id=%q want job-42", p.JobID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broadcast event")
	}
}

func TestEmit_NoDataIsNilPayload(t *testing.T) {
	bus := newTestBus(t)
	ctx := context.Background()

	ev, err := bus.Emit(ctx, events.ActorUser, events.KindApprovalCreated, "", "no payload here", nil)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if ev.Data != nil {
		t.Fatalf("Data=%q want nil for Emit with nil data", ev.Data)
	}

	recent, err := bus.Recent(ctx, 10)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(recent) != 1 {
		t.Fatalf("len(Recent)=%d want 1", len(recent))
	}
	if recent[0].Data != nil {
		t.Fatalf("persisted Data=%q want nil", recent[0].Data)
	}
}

func TestRecent_ReturnsRowsInChronologicalOrder(t *testing.T) {
	bus := newTestBus(t)
	ctx := context.Background()

	const n = 5
	var emitted []events.Event
	for i := 0; i < n; i++ {
		ev, err := bus.Emit(ctx, events.ActorAgent, events.KindAgentState, "", "", map[string]int{"seq": i})
		if err != nil {
			t.Fatalf("Emit #%d: %v", i, err)
		}
		emitted = append(emitted, ev)
	}

	got, err := bus.Recent(ctx, n)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(got) != n {
		t.Fatalf("len(Recent)=%d want %d", len(got), n)
	}
	for i := range got {
		if got[i].ID != emitted[i].ID {
			t.Fatalf("Recent[%d].ID=%q want %q (not in emission order)", i, got[i].ID, emitted[i].ID)
		}
	}
}

func TestRecent_LimitCapsAndKeepsMostRecent(t *testing.T) {
	bus := newTestBus(t)
	ctx := context.Background()

	var last events.Event
	for i := 0; i < 5; i++ {
		ev, err := bus.Emit(ctx, events.ActorSystem, events.KindAlert, "", "", nil)
		if err != nil {
			t.Fatalf("Emit #%d: %v", i, err)
		}
		last = ev
	}

	got, err := bus.Recent(ctx, 1)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(Recent)=%d want 1", len(got))
	}
	if got[0].ID != last.ID {
		t.Fatalf("Recent(1)[0].ID=%q want most recent %q", got[0].ID, last.ID)
	}
}

func TestRecent_NonPositiveLimitReturnsNothing(t *testing.T) {
	bus := newTestBus(t)
	ctx := context.Background()

	if _, err := bus.Emit(ctx, events.ActorSystem, events.KindAlert, "", "", nil); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	got, err := bus.Recent(ctx, 0)
	if err != nil {
		t.Fatalf("Recent(0): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Recent(0) len=%d want 0", len(got))
	}
}

func TestEmit_ManySubscribersAllReceive(t *testing.T) {
	bus := newTestBus(t)
	ctx := context.Background()

	const subs = 4
	chans := make([]<-chan events.Event, subs)
	for i := range chans {
		ch, unsub := bus.Subscribe(ctx)
		chans[i] = ch
		defer unsub()
	}

	ev, err := bus.Emit(ctx, events.ActorSystem, events.KindAlert, "", "fan out", nil)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}

	for i, ch := range chans {
		select {
		case got := <-ch:
			if got.ID != ev.ID {
				t.Fatalf("subscriber %d got id %q want %q", i, got.ID, ev.ID)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d: timed out waiting for event", i)
		}
	}
}

func TestEventKindConstants_MatchSpecSSEList(t *testing.T) {
	want := map[string]string{
		"KindJobUpdated":      "job.updated",
		"KindApprovalCreated": "approval.created",
		"KindApprovalDecided": "approval.decided",
		"KindAgentState":      "agent.state",
		"KindAlert":           "alert",
	}
	got := map[string]string{
		"KindJobUpdated":      events.KindJobUpdated,
		"KindApprovalCreated": events.KindApprovalCreated,
		"KindApprovalDecided": events.KindApprovalDecided,
		"KindAgentState":      events.KindAgentState,
		"KindAlert":           events.KindAlert,
	}
	for name, wantVal := range want {
		if got[name] != wantVal {
			t.Errorf("%s=%q want %q (SPEC.md §4)", name, got[name], wantVal)
		}
	}
}
