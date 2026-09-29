package httpapi

import (
	"context"
	"database/sql"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"mayank2/internal/content"
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
		Approvals:      &content.ApprovalService{DB: sqlDB},
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

// freePort asks the OS for an unused loopback TCP port, for tests that need
// to bind a real listener (ListenAndServe tests can't use httptest.Server
// since ListenAndServe owns its own net.Listen calls).
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return strconv.Itoa(port)
}

// waitForServer polls addr until it accepts TCP connections or the test
// deadline (5s) is hit.
func waitForServer(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server at %s did not come up in time", addr)
}

func seedApproval(t *testing.T, sqlDB *sql.DB, id, status string) {
	t.Helper()
	ctx := context.Background()
	warmup := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339Nano)
	_, err := sqlDB.ExecContext(ctx, `
INSERT INTO channels (id, platform, handle, language, niche, status, warmup_started_at)
VALUES ('ch1', 'youtube', 'test', 'en', 'money', 'active', ?)`, warmup)
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
VALUES (?, 'c1', 'short', 'preview ready', ?, 'nonce1')`, id, status)
	if err != nil {
		t.Fatalf("seed approval: %v", err)
	}
}
