// GET /api/agents (SPEC §4: "agent cards (state, current job, last success,
// next run)"). The card set and names are fixed by DESIGN.md's Home screen
// spec (Scout, Research, Script, Compliance, Voice, Visuals, Render, each
// Publisher, Analytics, Blog, Builder Implementer, Builder Auditor); each
// agent maps to one or more SPEC §5 job types. httpapi.Server only has the
// queue through the narrow Pauser interface (pause/resume), not *queue.Queue
// itself, so this handler cannot read the live Register-ed handler map
// in-process — instead every card's state is computed from real `jobs` table
// rows for that agent's job types (see tickets/M2-126.md Notes for why this
// is the conservative, non-fabricated choice).
package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
)

// agentDef is one fixed roster entry: a display agent and the job types
// (SPEC.md §5) whose rows answer its state/currentJob/lastSuccess/nextRun.
type agentDef struct {
	id       string
	name     string
	jobTypes []string
}

// agentRoster mirrors DESIGN.md's Home-screen agent card list exactly.
// builder.plan/builder.implement/builder.gate are grouped under "Builder
// Implementer" and builder.audit under "Builder Auditor": DESIGN.md names
// only those two Builder cards even though SPEC §5 lists four builder.*
// job types (see tickets/M2-126.md Notes).
var agentRoster = []agentDef{
	{"scout", "Scout", []string{"scout.topics"}},
	{"research", "Research", []string{"research.brief"}},
	{"script", "Script", []string{"script.write"}},
	{"compliance", "Compliance", []string{"compliance.script", "compliance.final"}},
	{"voice", "Voice", []string{"voice.tts"}},
	{"visuals", "Visuals", []string{"visuals.fetch"}},
	{"render", "Render", []string{"render.long", "render.short", "render.thumbnail"}},
	{"publish_youtube", "Publisher: YouTube", []string{"publish.youtube"}},
	{"publish_instagram", "Publisher: Instagram", []string{"publish.instagram"}},
	{"publish_facebook", "Publisher: Facebook", []string{"publish.facebook"}},
	{"publish_x", "Publisher: X", []string{"publish.x"}},
	{"publish_pinterest", "Publisher: Pinterest", []string{"publish.pinterest"}},
	{"publish_linkedin", "Publisher: LinkedIn", []string{"publish.linkedin"}},
	{"analytics", "Analytics", []string{"analytics.pull"}},
	{"blog", "Blog", []string{"blog.draft", "blog.merge", "blog.repurpose"}},
	{"builder_implementer", "Builder Implementer", []string{"builder.plan", "builder.implement", "builder.gate"}},
	{"builder_auditor", "Builder Auditor", []string{"builder.audit"}},
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database not configured")
		return
	}
	ctx := r.Context()

	globalPaused, err := s.settingBool(ctx, "pause:all")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "agents: read pause state failed")
		return
	}

	now := time.Now().UTC()
	agents := make([]map[string]any, 0, len(agentRoster))
	for _, def := range agentRoster {
		card, err := s.agentCard(ctx, def, globalPaused, now)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "agents: load agent state failed")
			return
		}
		agents = append(agents, card)
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": agents})
}

// agentCard computes one agent's real card from the jobs table.
func (s *Server) agentCard(ctx context.Context, def agentDef, globalPaused bool, now time.Time) (map[string]any, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(def.jobTypes)), ",")
	args := make([]any, len(def.jobTypes))
	for i, t := range def.jobTypes {
		args[i] = t
	}

	var currentJob any
	var jobID string
	row := s.db.QueryRowContext(ctx,
		`SELECT id FROM jobs WHERE type IN (`+placeholders+`) AND status='running' ORDER BY updated_at DESC LIMIT 1`,
		args...)
	switch err := row.Scan(&jobID); {
	case err == nil:
		currentJob = jobID
	case errors.Is(err, sql.ErrNoRows):
		currentJob = nil
	default:
		return nil, err
	}

	var lastSuccess any
	var lastSuccessStr sql.NullString
	row = s.db.QueryRowContext(ctx,
		`SELECT MAX(updated_at) FROM jobs WHERE type IN (`+placeholders+`) AND status='succeeded'`,
		args...)
	if err := row.Scan(&lastSuccessStr); err != nil {
		return nil, err
	}
	if lastSuccessStr.Valid {
		lastSuccess = lastSuccessStr.String
	}

	var nextRun any
	var nextRunStr sql.NullString
	nextArgs := append(append([]any{}, args...), now.Format(time.RFC3339Nano))
	row = s.db.QueryRowContext(ctx,
		`SELECT MIN(run_at) FROM jobs WHERE type IN (`+placeholders+`) AND status='queued' AND run_at > ?`,
		nextArgs...)
	if err := row.Scan(&nextRunStr); err != nil {
		return nil, err
	}
	if nextRunStr.Valid {
		nextRun = nextRunStr.String
	}

	var lastStatus sql.NullString
	row = s.db.QueryRowContext(ctx,
		`SELECT status FROM jobs WHERE type IN (`+placeholders+`) ORDER BY updated_at DESC LIMIT 1`,
		args...)
	if err := row.Scan(&lastStatus); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	paused := globalPaused
	if !paused {
		p, err := s.settingBool(ctx, "pause:agent:"+def.id)
		if err != nil {
			return nil, err
		}
		paused = p
	}

	state := "idle"
	switch {
	case currentJob != nil:
		state = "working"
	case paused:
		state = "paused"
	case lastStatus.Valid && (lastStatus.String == "failed" || lastStatus.String == "dead"):
		state = "error"
	}

	return map[string]any{
		"id":          def.id,
		"name":        def.name,
		"state":       state,
		"currentJob":  currentJob,
		"lastSuccess": lastSuccess,
		"nextRun":     nextRun,
	}, nil
}
