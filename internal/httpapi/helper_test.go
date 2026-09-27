package httpapi

import (
	"context"
	"database/sql"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"mayank2/internal/db"
	"mayank2/internal/events"
)

const testToken = "test-dashboard-token-please-change"

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

func testServer(t *testing.T, sqlDB *sql.DB, bus *events.Bus) *Server {
	t.Helper()
	web := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>ok</html>")},
	}
	s, err := New(Options{
		DashboardToken: testToken,
		DB:             sqlDB,
		Events:         bus,
		Version:        "test",
		WebFS:          fs.FS(web),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func login(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"token":"`+testToken+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("login status %d body %s", rr.Code, rr.Body.String())
	}
	for _, c := range rr.Result().Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	t.Fatal("no session cookie")
	return nil
}

func seedApproval(t *testing.T, sqlDB *sql.DB, id, status string) {
	t.Helper()
	ctx := context.Background()
	_, err := sqlDB.ExecContext(ctx, `
INSERT INTO channels (id, platform, handle, language, niche, status)
VALUES ('ch1', 'youtube', 'test', 'en', 'money', 'active')`)
	if err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	_, err = sqlDB.ExecContext(ctx, `
INSERT INTO content_items (id, channel_id, kind, language, stage, compliance, created_at)
VALUES ('c1', 'ch1', 'short', 'en', 'approval', '{"ok":true}', ?)`, "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("seed content: %v", err)
	}
	_, err = sqlDB.ExecContext(ctx, `
INSERT INTO approvals (id, content_id, kind, summary, status, nonce)
VALUES (?, 'c1', 'final', 'preview ready', ?, 'nonce1')`, id, status)
	if err != nil {
		t.Fatalf("seed approval: %v", err)
	}
}
