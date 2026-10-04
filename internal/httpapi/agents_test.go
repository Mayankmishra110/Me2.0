package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"mayank2/internal/events"
)

// insertJob writes a minimal jobs row directly (bypassing queue.Queue, which
// this package doesn't depend on) so tests can set up exact job/type/status
// combinations for GET /api/agents to derive from.
func insertJob(t *testing.T, sqlDB *sql.DB, id, jobType, status, runAt, updatedAt string) {
	t.Helper()
	_, err := sqlDB.ExecContext(context.Background(), `
INSERT INTO jobs (id, type, status, resource, priority, payload, run_at, attempts, max_attempts, created_at, updated_at)
VALUES (?, ?, ?, 'light', 0, '{}', ?, 0, 3, ?, ?)`,
		id, jobType, status, runAt, updatedAt, updatedAt)
	if err != nil {
		t.Fatalf("insert job: %v", err)
	}
}

func TestAgentsReflectsRealJobState(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	fmtT := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339Nano) }

	sqlDB := testDB(t)
	bus := events.New(sqlDB)
	h := testServer(t, sqlDB, bus).Handler()
	cookie := login(t, h)

	// scout: a running job -> working + currentJob.
	insertJob(t, sqlDB, "job-scout-running", "scout.topics", "running", fmtT(-time.Hour), fmtT(-time.Minute))
	// research: a succeeded job -> lastSuccess set, idle (no running job).
	insertJob(t, sqlDB, "job-research-done", "research.brief", "succeeded", fmtT(-2*time.Hour), fmtT(-90*time.Minute))
	// script: a queued job scheduled in the future -> nextRun set, idle.
	insertJob(t, sqlDB, "job-script-queued", "script.write", "queued", fmtT(3*time.Hour), fmtT(-time.Hour))
	// render: a failed job and nothing since -> error.
	insertJob(t, sqlDB, "job-render-failed", "render.long", "failed", fmtT(-time.Hour), fmtT(-30*time.Minute))
	// voice: paused via pause:agent:voice.
	if _, err := sqlDB.ExecContext(context.Background(),
		`INSERT INTO settings (key, value) VALUES ('pause:agent:voice', '1')`); err != nil {
		t.Fatalf("seed pause setting: %v", err)
	}
	// analytics: no rows at all -> idle, everything null.

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	req.AddCookie(cookie)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	type agentCardJSON struct {
		ID          string  `json:"id"`
		Name        string  `json:"name"`
		State       string  `json:"state"`
		CurrentJob  *string `json:"currentJob"`
		LastSuccess *string `json:"lastSuccess"`
		NextRun     *string `json:"nextRun"`
	}
	var resp struct {
		Agents []agentCardJSON `json:"agents"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Agents) != len(agentRoster) {
		t.Fatalf("agents=%d want %d (full fixed roster, not an empty stub)", len(resp.Agents), len(agentRoster))
	}

	byID := map[string]agentCardJSON{}
	for _, a := range resp.Agents {
		byID[a.ID] = a
	}

	cases := []struct {
		id              string
		wantState       string
		wantCurrentJob  string // "" means want nil
		wantLastSuccess bool
		wantNextRun     bool
	}{
		{id: "scout", wantState: "working", wantCurrentJob: "job-scout-running"},
		{id: "research", wantState: "idle", wantLastSuccess: true},
		{id: "script", wantState: "idle", wantNextRun: true},
		{id: "render", wantState: "error"},
		{id: "voice", wantState: "paused"},
		{id: "analytics", wantState: "idle"},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			got, ok := byID[tc.id]
			if !ok {
				t.Fatalf("agent %q missing from response", tc.id)
			}
			if got.State != tc.wantState {
				t.Fatalf("state=%q want %q", got.State, tc.wantState)
			}
			if tc.wantCurrentJob == "" {
				if got.CurrentJob != nil {
					t.Fatalf("currentJob=%v want nil", *got.CurrentJob)
				}
			} else {
				if got.CurrentJob == nil || *got.CurrentJob != tc.wantCurrentJob {
					t.Fatalf("currentJob=%v want %q", got.CurrentJob, tc.wantCurrentJob)
				}
			}
			if tc.wantLastSuccess && got.LastSuccess == nil {
				t.Fatalf("lastSuccess=nil want non-nil")
			}
			if !tc.wantLastSuccess && got.LastSuccess != nil {
				t.Fatalf("lastSuccess=%v want nil", *got.LastSuccess)
			}
			if tc.wantNextRun && got.NextRun == nil {
				t.Fatalf("nextRun=nil want non-nil")
			}
			if !tc.wantNextRun && got.NextRun != nil {
				t.Fatalf("nextRun=%v want nil", *got.NextRun)
			}
		})
	}

	// analytics has zero job rows: every field must be null, not fabricated.
	analytics := byID["analytics"]
	if analytics.CurrentJob != nil || analytics.LastSuccess != nil || analytics.NextRun != nil {
		t.Fatalf("analytics (no rows) = %+v, want all nil", analytics)
	}
}

func TestAgentsGlobalPauseOverridesState(t *testing.T) {
	sqlDB := testDB(t)
	bus := events.New(sqlDB)
	h := testServer(t, sqlDB, bus).Handler()
	cookie := login(t, h)

	if _, err := sqlDB.ExecContext(context.Background(),
		`INSERT INTO settings (key, value) VALUES ('pause:all', '1')`); err != nil {
		t.Fatalf("seed pause:all: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	req.AddCookie(cookie)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Agents []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"agents"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, a := range resp.Agents {
		if a.State != "paused" {
			t.Fatalf("agent %q state=%q want paused (pause:all)", a.ID, a.State)
		}
	}
}

func TestAgentsRequiresAuth(t *testing.T) {
	sqlDB := testDB(t)
	bus := events.New(sqlDB)
	h := testServer(t, sqlDB, bus).Handler()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", rr.Code)
	}
}

func TestAgentsDatabaseNotConfigured(t *testing.T) {
	s := &Server{log: nil}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("handler panicked with nil db: %v", r)
		}
	}()
	s.handleAgents(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", rr.Code)
	}
}
