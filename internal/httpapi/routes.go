package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mayank2/internal/events"
	"mayank2/internal/queue"

	"golang.org/x/crypto/bcrypt"
)

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// Public.
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("POST /api/login", s.handleLogin)

	// Authenticated API.
	api := http.NewServeMux()
	api.HandleFunc("GET /api/agents", s.handleAgents)
	api.HandleFunc("GET /api/jobs", s.handleJobs)
	api.HandleFunc("POST /api/jobs/{id}/retry", s.handleJobRetry)
	api.HandleFunc("GET /api/approvals", s.handleApprovalsList)
	api.HandleFunc("GET /api/approvals/{id}", s.handleApprovalDetail)
	api.HandleFunc("POST /api/approvals/{id}/decision", s.handleApprovalDecision)
	api.HandleFunc("GET /api/content", s.handleContent)
	api.HandleFunc("GET /api/calendar", s.handleCalendar)
	api.HandleFunc("GET /api/channels/{id}/metrics", s.handleChannelMetrics)
	api.HandleFunc("GET /api/topics", s.handleTopicsList)
	api.HandleFunc("POST /api/topics", s.handleTopicsCreate)
	api.HandleFunc("GET /api/builder", s.handleBuilder)
	api.HandleFunc("GET /api/revenue", s.handleRevenueList)
	api.HandleFunc("POST /api/revenue", s.handleRevenueCreate)
	api.HandleFunc("POST /api/pause", s.handlePause)
	api.HandleFunc("POST /api/resume", s.handleResume)
	api.HandleFunc("GET /api/events/stream", s.handleEventsStream)
	api.HandleFunc("GET /media/{assetId}", s.handleMedia)

	mux.Handle("/api/", s.requireAuth(api))
	mux.Handle("/media/", s.requireAuth(api))

	// SPA last: catch-all for non-API paths.
	mux.Handle("/", spaHandler(s.web))
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	paused, err := s.settingBool(r.Context(), "pause:all")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "health check failed")
		return
	}
	status := "running"
	if paused {
		status = "paused"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                  true,
		"version":             s.version,
		"paused":              paused,
		"status":              status,
		"claudeUsageLimitHit": false,
	})
}

func (s *Server) handleAgents(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"agents": []any{}})
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	status := q.Get("status")
	jobType := q.Get("type")
	limit := 50
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		if n > 200 {
			n = 200
		}
		limit = n
	}

	query := `SELECT id, type, status, content_id, last_error, updated_at FROM jobs WHERE 1=1`
	args := []any{}
	if status != "" {
		query += ` AND status=?`
		args = append(args, status)
	}
	if jobType != "" {
		query += ` AND type=?`
		args = append(args, jobType)
	}
	query += ` ORDER BY updated_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list jobs failed")
		return
	}
	defer rows.Close()

	jobs := make([]map[string]any, 0)
	for rows.Next() {
		var (
			id, typ, st, updated string
			contentID, lastErr   sql.NullString
		)
		if err := rows.Scan(&id, &typ, &st, &contentID, &lastErr, &updated); err != nil {
			writeError(w, http.StatusInternalServerError, "scan jobs failed")
			return
		}
		var channel any
		if contentID.Valid {
			channel = contentID.String
		}
		var errVal any
		if lastErr.Valid {
			errVal = lastErr.String
		}
		jobs = append(jobs, map[string]any{
			"id":        id,
			"type":      typ,
			"status":    st,
			"channel":   channel,
			"error":     errVal,
			"updatedAt": updated,
		})
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "iterate jobs failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (s *Server) handleJobRetry(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(r.Context(), `
UPDATE jobs
SET status='queued', last_error=NULL, lease_until=NULL, worker=NULL,
    updated_at=?, run_at=?
WHERE id=? AND status IN ('failed','dead')`,
		now, now, id,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "retry failed")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if s.events != nil {
		_, _ = s.events.Emit(r.Context(), events.ActorUser, events.KindJobUpdated, id, "job re-queued", map[string]any{
			"id":     id,
			"status": "queued",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

func (s *Server) handleContent(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
}

func (s *Server) handleCalendar(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"entries": []any{}})
}

func (s *Server) handleChannelMetrics(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rangeParam := r.URL.Query().Get("range")
	if rangeParam == "" {
		rangeParam = "7d"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"channelId": id,
		"range":     rangeParam,
		"points":    []any{},
	})
}

func (s *Server) handleTopicsList(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"topics": []any{}})
}

func (s *Server) handleTopicsCreate(w http.ResponseWriter, r *http.Request) {
	var body json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "topic": json.RawMessage(body)})
}

func (s *Server) handleBuilder(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"plans":   []any{},
		"threads": []any{},
		"audits":  []any{},
	})
}

func (s *Server) handleRevenueList(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"entries": []any{}})
}

func (s *Server) handleRevenueCreate(w http.ResponseWriter, r *http.Request) {
	var body json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "entry": json.RawMessage(body)})
}

type pauseRequest struct {
	Scope string `json:"scope"`
	PIN   string `json:"pin"`
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	var body pauseRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.Scope == "" {
		writeError(w, http.StatusBadRequest, "scope required")
		return
	}
	if err := s.applyPause(r.Context(), body.Scope, true); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.events != nil {
		_, _ = s.events.Emit(r.Context(), events.ActorUser, events.KindAgentState, body.Scope, "paused", map[string]any{
			"scope":  body.Scope,
			"paused": true,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "paused": true, "scope": body.Scope})
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	var body pauseRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.Scope == "" {
		writeError(w, http.StatusBadRequest, "scope required")
		return
	}
	if err := s.checkPIN(r.Context(), body.PIN); err != nil {
		writeError(w, http.StatusUnauthorized, "pin required")
		return
	}
	if err := s.applyPause(r.Context(), body.Scope, false); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.events != nil {
		_, _ = s.events.Emit(r.Context(), events.ActorUser, events.KindAgentState, body.Scope, "resumed", map[string]any{
			"scope":  body.Scope,
			"paused": false,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "paused": false, "scope": body.Scope})
}

func (s *Server) checkPIN(ctx context.Context, pin string) error {
	var hash string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, "pin_hash").Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) || hash == "" {
		// PIN not configured yet (set-pin lands later). Still require a non-empty
		// value so resume is never a silent no-check.
		if strings.TrimSpace(pin) == "" {
			return errors.New("pin required")
		}
		return nil
	}
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(pin)) != nil {
		return errors.New("bad pin")
	}
	return nil
}

func (s *Server) applyPause(ctx context.Context, scope string, pause bool) error {
	if scope == "all" {
		if s.queue != nil {
			if pause {
				return s.queue.PauseAll(ctx)
			}
			return s.queue.ResumeAll(ctx)
		}
		return s.setSetting(ctx, "pause:all", pause)
	}
	res := queue.Resource(scope)
	switch res {
	case queue.ResourceHeavy, queue.ResourceLight, queue.ResourceNet:
		if s.queue != nil {
			if pause {
				return s.queue.PauseResource(ctx, res)
			}
			return s.queue.ResumeResource(ctx, res)
		}
		return s.setSetting(ctx, "pause:resource:"+scope, pause)
	default:
		return s.setSetting(ctx, "pause:agent:"+scope, pause)
	}
}

func (s *Server) handleMedia(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotFound, "not found")
}

func (s *Server) settingBool(ctx context.Context, key string) (bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("httpapi: read setting %s: %w", key, err)
	}
	return value == "1", nil
}

func (s *Server) setSetting(ctx context.Context, key string, on bool) error {
	value := "0"
	if on {
		value = "1"
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		key, value,
	)
	if err != nil {
		return fmt.Errorf("httpapi: set setting %s: %w", key, err)
	}
	return nil
}
