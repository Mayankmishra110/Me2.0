// Package db opens the SQLite database and applies embedded migrations.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Open opens (or creates) a SQLite database at path with the required pragmas:
// WAL, busy_timeout=5000, foreign_keys=ON. path's parent directories are created.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir for db %s: %w", path, err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	db.SetMaxOpenConns(1) // SQLite + WAL: one writer; keep it simple for the laptop daemon
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite %s: %w", path, err)
	}
	pragmas := []struct {
		q    string
		want string // empty = no check
	}{
		{q: `PRAGMA busy_timeout=5000`},
		{q: `PRAGMA foreign_keys=ON`},
		{q: `PRAGMA journal_mode=WAL`, want: "wal"},
	}
	for _, p := range pragmas {
		if p.want == "" {
			if _, err := db.ExecContext(ctx, p.q); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("%s: %w", p.q, err)
			}
			continue
		}
		var mode string
		if err := db.QueryRowContext(ctx, p.q).Scan(&mode); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("%s: %w", p.q, err)
		}
		if mode != p.want {
			_ = db.Close()
			return nil, fmt.Errorf("%s: got %q want %q", p.q, mode, p.want)
		}
	}
	return db, nil
}

// DefaultPath returns dataDir/mayank2.db.
func DefaultPath(dataDir string) string {
	return filepath.Join(dataDir, "mayank2.db")
}

// nowUTC returns an ISO-8601 UTC timestamp for schema_migrations.applied_at.
func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
