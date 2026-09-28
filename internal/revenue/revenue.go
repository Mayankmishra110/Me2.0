package revenue

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Line mirrors the `revenue.line` CHECK constraint in migrations/001_init.sql.
type Line string

const (
	LineAds       Line = "ads"
	LineAffiliate Line = "affiliate"
	LineAgency    Line = "agency"
	LineSaaS      Line = "saas"
	LineSponsor   Line = "sponsor"
)

func (l Line) valid() bool {
	switch l {
	case LineAds, LineAffiliate, LineAgency, LineSaaS, LineSponsor:
		return true
	default:
		return false
	}
}

// ValidLines lists every accepted `line` value, in CHECK-constraint order.
func ValidLines() []Line {
	return []Line{LineAds, LineAffiliate, LineAgency, LineSaaS, LineSponsor}
}

// Entry is one row of the `revenue` table (ARCHITECTURE.md §4).
//
// Every entry is attributable by source, date and amount at minimum
// (SPEC.md §1 P6 exit check). There is intentionally no separate `status`
// column: a recorded entry is treated as an already-confirmed fact — ad
// revenue pulled from analytics, or an affiliate/sponsor/agency/saas amount
// a human enters after the money is real. `Note` carries free-text status
// if ever needed (e.g. "invoiced, not yet paid"). See tickets/M2-601.md
// Notes for the (a)/(b) tradeoff this picks (a).
type Entry struct {
	ID       string  `json:"id"`
	Line     Line    `json:"line"`
	Source   string  `json:"source"`
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
	Date     string  `json:"date"` // YYYY-MM-DD, matches the dashboard date-range filter
	Note     string  `json:"note,omitempty"`
}

// Filter narrows List to a line and/or a date range (`date` is a plain
// YYYY-MM-DD string column, so comparisons are lexicographic — callers pass
// the same format).
type Filter struct {
	Line  Line   // empty = all lines
	From  string // inclusive, empty = no lower bound
	To    string // inclusive, empty = no upper bound
	Limit int    // <=0 defaults to 200
}

// execer is the subset of *sql.DB this package needs, so tests can pass
// anything that provides it (mirrors internal/events' execer).
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Record validates and inserts one entry, generating an ID if e.ID is
// empty. Returns the id written.
func Record(ctx context.Context, d execer, e Entry) (string, error) {
	if d == nil {
		return "", fmt.Errorf("revenue: db is required")
	}
	if !e.Line.valid() {
		return "", fmt.Errorf("revenue: invalid line %q", e.Line)
	}
	if strings.TrimSpace(e.Source) == "" {
		return "", fmt.Errorf("revenue: source is required")
	}
	if e.Amount < 0 {
		return "", fmt.Errorf("revenue: amount must be >= 0")
	}
	if strings.TrimSpace(e.Date) == "" {
		return "", fmt.Errorf("revenue: date is required")
	}
	if strings.TrimSpace(e.Currency) == "" {
		e.Currency = "USD"
	}
	if strings.TrimSpace(e.ID) == "" {
		e.ID = newID()
	}

	_, err := d.ExecContext(ctx, `
INSERT INTO revenue (id, line, source, amount, currency, date, note)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.ID, string(e.Line), e.Source, e.Amount, e.Currency, e.Date, nullableString(e.Note),
	)
	if err != nil {
		return "", fmt.Errorf("revenue: insert: %w", err)
	}
	return e.ID, nil
}

// List returns entries matching filter, newest date first (ties broken by
// id descending, so same-day inserts still come back most-recent-first).
func List(ctx context.Context, d execer, filter Filter) ([]Entry, error) {
	if d == nil {
		return nil, fmt.Errorf("revenue: db is required")
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 200
	}

	query := `SELECT id, line, source, amount, currency, date, COALESCE(note, '') FROM revenue WHERE 1=1`
	args := []any{}
	if filter.Line != "" {
		if !filter.Line.valid() {
			return nil, fmt.Errorf("revenue: invalid line %q", filter.Line)
		}
		query += ` AND line = ?`
		args = append(args, string(filter.Line))
	}
	if filter.From != "" {
		query += ` AND date >= ?`
		args = append(args, filter.From)
	}
	if filter.To != "" {
		query += ` AND date <= ?`
		args = append(args, filter.To)
	}
	query += ` ORDER BY date DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := d.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("revenue: query: %w", err)
	}
	defer rows.Close()

	out := []Entry{}
	for rows.Next() {
		var e Entry
		var line string
		if err := rows.Scan(&e.ID, &line, &e.Source, &e.Amount, &e.Currency, &e.Date, &e.Note); err != nil {
			return nil, fmt.Errorf("revenue: scan: %w", err)
		}
		e.Line = Line(line)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("revenue: iterate: %w", err)
	}
	return out, nil
}

// Totals sums amount by line for the given filter (dashboard summary row).
// Currency is not converted; callers that mix currencies should filter to
// one currency first — the API/dashboard assume USD today (no multi-
// currency conversion is in scope for this ticket).
func Totals(ctx context.Context, d execer, filter Filter) (map[Line]float64, error) {
	entries, err := List(ctx, d, filter)
	if err != nil {
		return nil, err
	}
	out := map[Line]float64{}
	for _, e := range entries {
		out[e.Line] += e.Amount
	}
	return out, nil
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Today returns the current UTC date as YYYY-MM-DD, the format Entry.Date
// and Filter.From/To expect.
func Today() string {
	return time.Now().UTC().Format("2006-01-02")
}
