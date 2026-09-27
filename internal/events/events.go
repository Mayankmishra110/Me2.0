// Package events appends to the append-only `events` table (ARCHITECTURE.md
// §4) and fans each row out live to in-process subscribers, so the HTTP
// layer (M2-106, internal/httpapi) can stream them to the dashboard over
// SSE (SPEC.md §4, GET /api/events/stream) without a second poll of the
// database.
package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Actor identifies who/what caused an event, matching the `events.actor`
// CHECK constraint in migrations/001_init.sql.
type Actor string

const (
	ActorAgent  Actor = "agent"
	ActorUser   Actor = "user"
	ActorSystem Actor = "system"
)

func (a Actor) valid() bool {
	switch a {
	case ActorAgent, ActorUser, ActorSystem:
		return true
	default:
		return false
	}
}

// Event kinds streamed by GET /api/events/stream (SPEC.md §4). `Emit`
// accepts any non-empty kind string — the events table is the general
// append-only audit log for every subsystem (ARCHITECTURE.md §4), not only
// these five — but producers feeding the SSE stream should use these exact
// constants so the dashboard's event names match the spec.
const (
	KindJobUpdated      = "job.updated"
	KindApprovalCreated = "approval.created"
	KindApprovalDecided = "approval.decided"
	KindAgentState      = "agent.state"
	KindAlert           = "alert"
)

// Event is one row of the `events` table.
type Event struct {
	ID      string          `json:"id"`
	At      time.Time       `json:"at"`
	Actor   Actor           `json:"actor"`
	Kind    string          `json:"kind"`
	Ref     string          `json:"ref,omitempty"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// execer is the subset of *sql.DB that Bus needs, so tests (and future
// callers) can pass anything that provides it, e.g. a *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Bus appends events to SQLite and fans them out to live subscribers. The
// zero value is not usable; construct with New.
type Bus struct {
	db  execer
	hub *Hub
}

// New returns a Bus backed by db (typically the *sql.DB from
// internal/db.Open, after migrations have run).
func New(db *sql.DB) *Bus {
	return &Bus{db: db, hub: NewHub()}
}

// Emit appends one event row and publishes it to current subscribers. It
// writes to SQLite first: a caller only ever sees an event on its
// subscription channel once it is durably in the events table.
//
// actor must be one of ActorAgent, ActorUser, ActorSystem. kind must be
// non-empty. ref and message may be empty. data, if non-nil, is marshalled
// to JSON and stored in the `data` column; pass nil for no payload.
func (b *Bus) Emit(ctx context.Context, actor Actor, kind, ref, message string, data any) (Event, error) {
	if !actor.valid() {
		return Event{}, fmt.Errorf("events: invalid actor %q", actor)
	}
	if kind == "" {
		return Event{}, fmt.Errorf("events: kind is required")
	}

	var raw json.RawMessage
	if data != nil {
		encoded, err := json.Marshal(data)
		if err != nil {
			return Event{}, fmt.Errorf("events: marshal data for kind %q: %w", kind, err)
		}
		raw = encoded
	}

	ev := Event{
		ID:      newID(),
		At:      time.Now().UTC(),
		Actor:   actor,
		Kind:    kind,
		Ref:     ref,
		Message: message,
		Data:    raw,
	}

	_, err := b.db.ExecContext(ctx, `
INSERT INTO events (id, at, actor, kind, ref, message, data)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ev.ID,
		ev.At.Format(time.RFC3339Nano),
		string(ev.Actor),
		ev.Kind,
		nullableString(ev.Ref),
		ev.Message,
		nullableJSON(ev.Data),
	)
	if err != nil {
		return Event{}, fmt.Errorf("events: insert kind %q: %w", kind, err)
	}

	b.hub.Publish(ev)
	return ev, nil
}

// Subscribe registers a live subscriber and returns a channel of events
// emitted from now on, plus an unsubscribe function. See Hub.Subscribe for
// the exact delivery and cleanup guarantees (non-blocking, drop-oldest,
// closes on unsubscribe or ctx.Done).
func (b *Bus) Subscribe(ctx context.Context) (<-chan Event, func()) {
	return b.hub.Subscribe(ctx)
}

// Recent returns up to limit of the most recently emitted events, oldest
// first (i.e. in the order they were appended), for callers that need to
// backfill state before switching to Subscribe (e.g. an SSE handler sending
// a snapshot before it starts streaming). limit <= 0 returns no rows.
func (b *Bus) Recent(ctx context.Context, limit int) ([]Event, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := b.db.QueryContext(ctx, `
SELECT id, at, actor, kind, ref, message, data
FROM (
	SELECT id, at, actor, kind, ref, message, data
	FROM events
	ORDER BY at DESC, id DESC
	LIMIT ?
)
ORDER BY at ASC, id ASC`, limit)
	if err != nil {
		return nil, fmt.Errorf("events: query recent: %w", err)
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var (
			ev    Event
			at    string
			actor string
			ref   sql.NullString
			data  sql.NullString
		)
		if err := rows.Scan(&ev.ID, &at, &actor, &ev.Kind, &ref, &ev.Message, &data); err != nil {
			return nil, fmt.Errorf("events: scan recent: %w", err)
		}
		ev.Actor = Actor(actor)
		parsed, err := time.Parse(time.RFC3339Nano, at)
		if err != nil {
			return nil, fmt.Errorf("events: parse at %q: %w", at, err)
		}
		ev.At = parsed
		if ref.Valid {
			ev.Ref = ref.String
		}
		if data.Valid {
			ev.Data = json.RawMessage(data.String)
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("events: iterate recent: %w", err)
	}
	return out, nil
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableJSON(raw json.RawMessage) any {
	if raw == nil {
		return nil
	}
	return string(raw)
}
