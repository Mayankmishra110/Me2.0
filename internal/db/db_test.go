package db_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"mayank2/internal/db"
)

var wantTables = []string{
	"schema_migrations",
	"jobs",
	"channels",
	"topics",
	"content_items",
	"assets",
	"approvals",
	"publications",
	"metrics",
	"scores",
	"script_fingerprints",
	"oauth_tokens",
	"builds",
	"quotas",
	"revenue",
	"events",
	"settings",
	"agency_leads",
	"agency_proposals",
}

func TestOpenAndMigrate_emptyAndIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mayank2.db")

	sqlDB, err := db.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlDB.Close()

	assertPragmas(t, ctx, sqlDB)

	first, err := db.Migrate(ctx, sqlDB)
	if err != nil {
		t.Fatalf("Migrate #1: %v", err)
	}
	if len(first) < 1 || first[0] != "001_init" {
		t.Fatalf("first applied=%v want leading 001_init", first)
	}
	seen := map[string]bool{}
	for _, v := range first {
		seen[v] = true
	}
	if !seen["002_agency_leads"] {
		t.Fatalf("first applied=%v missing 002_agency_leads", first)
	}
	assertTables(t, ctx, sqlDB)
	assertIndex(t, ctx, sqlDB, "jobs_status_resource_run_at")
	assertIndex(t, ctx, sqlDB, "events_at")
	assertPublicationsUnique(t, ctx, sqlDB)

	second, err := db.Migrate(ctx, sqlDB)
	if err != nil {
		t.Fatalf("Migrate #2: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("second applied=%v want empty (idempotent)", second)
	}
	assertTables(t, ctx, sqlDB)
}

func TestMigrate_onFreshFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := db.DefaultPath(dir)

	sqlDB, err := db.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlDB.Close()

	applied, err := db.Migrate(ctx, sqlDB)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(applied) == 0 {
		t.Fatal("expected at least 001_init")
	}
	assertTables(t, ctx, sqlDB)
}

func assertPragmas(t *testing.T, ctx context.Context, sqlDB *sql.DB) {
	t.Helper()
	var mode string
	if err := sqlDB.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode=%q want wal", mode)
	}
	var fk int
	if err := sqlDB.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Fatalf("foreign_keys=%d want 1", fk)
	}
	var timeout int
	if err := sqlDB.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&timeout); err != nil {
		t.Fatalf("busy_timeout: %v", err)
	}
	if timeout != 5000 {
		t.Fatalf("busy_timeout=%d want 5000", timeout)
	}
}

func assertTables(t *testing.T, ctx context.Context, sqlDB *sql.DB) {
	t.Helper()
	for _, name := range wantTables {
		var n int
		err := sqlDB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name,
		).Scan(&n)
		if err != nil {
			t.Fatalf("lookup table %s: %v", name, err)
		}
		if n != 1 {
			t.Fatalf("table %s missing", name)
		}
	}
}

func assertIndex(t *testing.T, ctx context.Context, sqlDB *sql.DB, name string) {
	t.Helper()
	var n int
	err := sqlDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, name,
	).Scan(&n)
	if err != nil {
		t.Fatalf("lookup index %s: %v", name, err)
	}
	if n != 1 {
		t.Fatalf("index %s missing", name)
	}
}

func assertPublicationsUnique(t *testing.T, ctx context.Context, sqlDB *sql.DB) {
	t.Helper()
	var sqlText string
	err := sqlDB.QueryRowContext(ctx,
		`SELECT sql FROM sqlite_master WHERE type='table' AND name='publications'`,
	).Scan(&sqlText)
	if err != nil {
		t.Fatalf("publications ddl: %v", err)
	}
	upper := strings.ToUpper(sqlText)
	if !strings.Contains(upper, "IDEMPOTENCY_KEY") || !strings.Contains(upper, "UNIQUE") {
		t.Fatalf("publications ddl missing UNIQUE idempotency_key: %s", sqlText)
	}
}
