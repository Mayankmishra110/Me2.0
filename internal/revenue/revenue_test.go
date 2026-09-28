package revenue

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"mayank2/internal/db"
)

// testDB returns a migrated *sql.DB, which satisfies this package's
// unexported `db` interface directly (ExecContext/QueryContext/
// QueryRowContext). File-backed (not ":memory:") because internal/db.Open
// sets SQLite pragmas (WAL, busy_timeout) that assume a real file, matching
// internal/httpapi's helper_test.go pattern.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	sqlDB, err := db.Open(ctx, path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := db.Migrate(ctx, sqlDB); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	return sqlDB
}

func TestRecordAndList(t *testing.T) {
	d := testDB(t)

	cases := []struct {
		name    string
		entry   Entry
		wantErr bool
	}{
		{
			name:  "valid ads entry",
			entry: Entry{Line: LineAds, Source: "youtube:ch1", Amount: 12.5, Date: "2026-09-29"},
		},
		{
			name:  "valid affiliate entry with note and currency",
			entry: Entry{Line: LineAffiliate, Source: "amazon", Amount: 3.2, Currency: "INR", Date: "2026-09-28", Note: "sneaker link"},
		},
		{
			name:    "invalid line",
			entry:   Entry{Line: "bogus", Source: "x", Amount: 1, Date: "2026-09-29"},
			wantErr: true,
		},
		{
			name:    "missing source",
			entry:   Entry{Line: LineAgency, Amount: 1, Date: "2026-09-29"},
			wantErr: true,
		},
		{
			name:    "negative amount",
			entry:   Entry{Line: LineAgency, Source: "acme", Amount: -1, Date: "2026-09-29"},
			wantErr: true,
		},
		{
			name:    "missing date",
			entry:   Entry{Line: LineAgency, Source: "acme", Amount: 5},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, err := Record(context.Background(), d, tc.entry)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got id %q", id)
				}
				return
			}
			if err != nil {
				t.Fatalf("Record: %v", err)
			}
			if id == "" {
				t.Fatal("expected non-empty id")
			}
		})
	}

	entries, err := List(context.Background(), d, Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %+v", len(entries), entries)
	}
	// Newest date first.
	if entries[0].Date != "2026-09-29" {
		t.Errorf("expected newest date first, got %s", entries[0].Date)
	}
	if entries[1].Currency != "INR" {
		t.Errorf("expected currency preserved, got %s", entries[1].Currency)
	}
	if entries[1].Note != "sneaker link" {
		t.Errorf("expected note preserved, got %q", entries[1].Note)
	}
}

func TestRecordDefaultsCurrencyAndID(t *testing.T) {
	d := testDB(t)
	id, err := Record(context.Background(), d, Entry{
		Line: LineSponsor, Source: "acme co", Amount: 500, Date: "2026-09-29",
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	entries, err := List(context.Background(), d, Filter{Line: LineSponsor})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != id {
		t.Fatalf("expected 1 entry with id %s, got %+v", id, entries)
	}
	if entries[0].Currency != "USD" {
		t.Errorf("expected default currency USD, got %s", entries[0].Currency)
	}
}

func TestListFilters(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	seed := []Entry{
		{Line: LineAds, Source: "youtube:a", Amount: 10, Date: "2026-09-01"},
		{Line: LineAds, Source: "youtube:a", Amount: 20, Date: "2026-09-15"},
		{Line: LineAffiliate, Source: "amazon", Amount: 5, Date: "2026-09-15"},
		{Line: LineSaaS, Source: "stripe", Amount: 99, Date: "2026-09-20"},
	}
	for _, e := range seed {
		if _, err := Record(ctx, d, e); err != nil {
			t.Fatalf("seed Record: %v", err)
		}
	}

	t.Run("by line", func(t *testing.T) {
		entries, err := List(ctx, d, Filter{Line: LineAds})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(entries) != 2 {
			t.Fatalf("expected 2 ads entries, got %d", len(entries))
		}
	})

	t.Run("by date range", func(t *testing.T) {
		entries, err := List(ctx, d, Filter{From: "2026-09-10", To: "2026-09-16"})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(entries) != 2 {
			t.Fatalf("expected 2 entries in range, got %d: %+v", len(entries), entries)
		}
	})

	t.Run("invalid line filter", func(t *testing.T) {
		if _, err := List(ctx, d, Filter{Line: "bogus"}); err == nil {
			t.Fatal("expected error for invalid line filter")
		}
	})
}

func TestTotals(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	for _, e := range []Entry{
		{Line: LineAds, Source: "youtube:a", Amount: 10, Date: "2026-09-01"},
		{Line: LineAds, Source: "youtube:a", Amount: 20, Date: "2026-09-15"},
		{Line: LineAffiliate, Source: "amazon", Amount: 5, Date: "2026-09-15"},
	} {
		if _, err := Record(ctx, d, e); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	totals, err := Totals(ctx, d, Filter{})
	if err != nil {
		t.Fatalf("Totals: %v", err)
	}
	if totals[LineAds] != 30 {
		t.Errorf("expected ads total 30, got %v", totals[LineAds])
	}
	if totals[LineAffiliate] != 5 {
		t.Errorf("expected affiliate total 5, got %v", totals[LineAffiliate])
	}
}
