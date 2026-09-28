package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"mayank2/internal/content"
	"mayank2/internal/events"
)

type decisionRequest struct {
	Decision string          `json:"decision"`
	Note     string          `json:"note"`
	Edits    json.RawMessage `json:"edits"`
}

func (s *Server) handleApprovalsList(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "pending"
	}

	rows, err := s.db.QueryContext(r.Context(), `
SELECT id, content_id, kind, summary, preview_path, status, note, decided_at
FROM approvals
WHERE status=?
ORDER BY id DESC
LIMIT 200`, status)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list approvals failed")
		return
	}
	defer rows.Close()

	items := make([]map[string]any, 0)
	for rows.Next() {
		item, err := scanApprovalRow(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "scan approvals failed")
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "iterate approvals failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approvals": items})
}

func (s *Server) handleApprovalDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	row := s.db.QueryRowContext(r.Context(), `
SELECT a.id, a.content_id, a.kind, a.summary, a.preview_path, a.status, a.note, a.decided_at,
       c.compliance
FROM approvals a
LEFT JOIN content_items c ON c.id = a.content_id
WHERE a.id=?`, id)

	var (
		approvalID, contentID, kind, summary, status string
		preview, note, decidedAt, compliance         sql.NullString
	)
	if err := row.Scan(&approvalID, &contentID, &kind, &summary, &preview, &status, &note, &decidedAt, &compliance); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "load approval failed")
		return
	}

	out := map[string]any{
		"id":           approvalID,
		"contentId":    contentID,
		"kind":         kind,
		"summary":      summary,
		"status":       status,
		"sources":      []any{},
		"destinations": []any{},
	}
	if preview.Valid {
		out["previewPath"] = preview.String
	}
	if note.Valid {
		out["note"] = note.String
	}
	if decidedAt.Valid {
		out["decidedAt"] = decidedAt.String
	}
	if compliance.Valid && compliance.String != "" {
		out["compliance"] = json.RawMessage(compliance.String)
	} else {
		out["compliance"] = map[string]any{}
	}

	dests, err := s.approvalDestinations(r.Context(), contentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load destinations failed")
		return
	}
	out["destinations"] = dests

	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleApprovalDecision(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body decisionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	status, ok := mapDecision(body.Decision)
	if !ok {
		writeError(w, http.StatusBadRequest, "decision must be approve, reject, or redo")
		return
	}
	if s.approvals == nil {
		writeError(w, http.StatusServiceUnavailable, "approval service not configured")
		return
	}

	var current string
	err := s.db.QueryRowContext(r.Context(), `SELECT status FROM approvals WHERE id=?`, id).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load approval failed")
		return
	}
	if current != "pending" {
		writeError(w, http.StatusConflict, "already decided")
		return
	}

	note := strings.TrimSpace(body.Note)
	if len(body.Edits) > 0 && !json.Valid(body.Edits) {
		writeError(w, http.StatusBadRequest, "edits must be valid json")
		return
	}

	dec := content.Decision(strings.ToLower(strings.TrimSpace(body.Decision)))
	err = s.approvals.Decide(r.Context(), content.DecideRequest{
		ApprovalID: id,
		Decision:   dec,
		Note:       note,
	})
	if err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "not found"):
			writeError(w, http.StatusNotFound, "not found")
		case strings.Contains(msg, "already"):
			writeError(w, http.StatusConflict, "already decided")
		case strings.Contains(msg, "redo requires"), strings.Contains(msg, "cap"):
			writeError(w, http.StatusConflict, msg)
		default:
			writeError(w, http.StatusInternalServerError, "decision failed")
		}
		return
	}

	// Extra dashboard-only fields (edits) on a secondary event; Decide already
	// emitted approval.decided with decision + content_id.
	if len(body.Edits) > 0 && s.events != nil {
		_, _ = s.events.Emit(r.Context(), events.ActorUser, events.KindApprovalDecided, id, "approval edits", map[string]any{
			"id":     id,
			"edits":  body.Edits,
			"status": status,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"id":       id,
		"status":   status,
		"decision": body.Decision,
	})
}

func mapDecision(decision string) (status string, ok bool) {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "approve":
		return "approved", true
	case "reject":
		return "rejected", true
	case "redo":
		return "redo", true
	default:
		return "", false
	}
}

func (s *Server) approvalDestinations(ctx context.Context, contentID string) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT platform, account, status, scheduled_at, url
FROM publications
WHERE content_id=?
ORDER BY platform, account`, contentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]map[string]any, 0)
	for rows.Next() {
		var platform, account, status string
		var scheduled, url sql.NullString
		if err := rows.Scan(&platform, &account, &status, &scheduled, &url); err != nil {
			return nil, err
		}
		item := map[string]any{
			"platform": platform,
			"account":  account,
			"status":   status,
		}
		if scheduled.Valid {
			item["scheduledAt"] = scheduled.String
		}
		if url.Valid {
			item["url"] = url.String
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanApprovalRow(rows *sql.Rows) (map[string]any, error) {
	var (
		id, contentID, kind, summary, status string
		preview, note, decidedAt             sql.NullString
	)
	if err := rows.Scan(&id, &contentID, &kind, &summary, &preview, &status, &note, &decidedAt); err != nil {
		return nil, err
	}
	item := map[string]any{
		"id":        id,
		"contentId": contentID,
		"kind":      kind,
		"summary":   summary,
		"status":    status,
	}
	if preview.Valid {
		item["previewPath"] = preview.String
	}
	if note.Valid {
		item["note"] = note.String
	}
	if decidedAt.Valid {
		item["decidedAt"] = decidedAt.String
	}
	return item, nil
}
