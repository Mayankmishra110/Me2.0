package telegram

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mayank2/internal/content"
	"mayank2/internal/queue"
)

// approvalRequestPayload is the approval.request job body.
type approvalRequestPayload struct {
	ApprovalID string `json:"approval_id"`
}

type approvalRow struct {
	ID          string
	Kind        string
	Summary     string
	PreviewPath sql.NullString
	Status      string
	Nonce       string
	MessageID   sql.NullInt64
}

func (b *Bot) handleApprovalRequest(ctx context.Context, job queue.Job) (json.RawMessage, error) {
	var p approvalRequestPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return nil, queue.Permanent(fmt.Errorf("telegram: approval.request payload: %w", err))
	}
	if p.ApprovalID == "" {
		return nil, queue.Permanent(fmt.Errorf("telegram: approval.request missing approval_id"))
	}

	row, err := b.loadApproval(ctx, p.ApprovalID)
	if err != nil {
		return nil, err
	}
	if row.Status != "pending" {
		// Already decided — idempotent no-op (at-least-once delivery).
		return json.RawMessage(`{"skipped":"not_pending"}`), nil
	}

	markup := approvalKeyboard(row.ID, row.Nonce)
	caption := row.Summary
	if caption == "" {
		caption = fmt.Sprintf("Approval %s (%s)", row.ID, row.Kind)
	}

	var msgID int64
	preview := ""
	if row.PreviewPath.Valid {
		preview = row.PreviewPath.String
	}
	// Only upload previews that exist and stay under dataRoot (fall back to text).
	usePreview := preview != "" && fileExists(preview) && underDataRoot(b.dataRoot, preview)
	switch {
	case usePreview && isVideo(preview):
		msgID, err = b.client.SendVideo(ctx, b.chatID, preview, caption, markup)
	case usePreview && isImage(preview):
		msgID, err = b.client.SendPhoto(ctx, b.chatID, preview, caption, markup)
	default:
		msgID, err = b.client.SendMessage(ctx, b.chatID, caption, markup)
	}
	if err != nil {
		return nil, fmt.Errorf("telegram: send approval preview %s: %w", p.ApprovalID, err)
	}

	_, err = b.db.ExecContext(ctx, `
UPDATE approvals SET telegram_message_id=? WHERE id=? AND status='pending'`,
		msgID, p.ApprovalID,
	)
	if err != nil {
		return nil, fmt.Errorf("telegram: store telegram_message_id: %w", err)
	}
	_ = b.emitEvent(ctx, "system", "approval.created", p.ApprovalID, "approval preview sent", "")
	return json.RawMessage(fmt.Sprintf(`{"telegram_message_id":%d}`, msgID)), nil
}

func approvalKeyboard(id, nonce string) *InlineKeyboardMarkup {
	return &InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: "✅ Approve", CallbackData: "approve:" + id + ":" + nonce},
				{Text: "❌ Reject", CallbackData: "reject:" + id + ":" + nonce},
				{Text: "🔁 Redo…", CallbackData: "redo:" + id + ":" + nonce},
			},
		},
	}
}

func (b *Bot) handleCallback(ctx context.Context, cq *CallbackQuery) error {
	chatID := b.chatID
	if cq.Message != nil {
		chatID = cq.Message.Chat.ID
	}
	if !b.allowed(cq.From.ID, chatID) {
		_ = b.client.AnswerCallbackQuery(ctx, cq.ID, "ignored")
		return b.rejectStranger(ctx, cq.From.ID, chatID, "callback")
	}

	action, approvalID, nonce, ok := parseCallback(cq.Data)
	if !ok {
		_ = b.client.AnswerCallbackQuery(ctx, cq.ID, "bad callback")
		return nil
	}

	row, err := b.loadApproval(ctx, approvalID)
	if err != nil {
		_ = b.client.AnswerCallbackQuery(ctx, cq.ID, "not found")
		return err
	}

	// Double-tap / replay: only pending + matching nonce may decide.
	if row.Status != "pending" || row.Nonce != nonce {
		_ = b.client.AnswerCallbackQuery(ctx, cq.ID, "already decided")
		return nil
	}

	switch action {
	case "approve", "reject":
		if err := b.decideApproval(ctx, row, action, ""); err != nil {
			_ = b.client.AnswerCallbackQuery(ctx, cq.ID, "error")
			return err
		}
		_ = b.client.AnswerCallbackQuery(ctx, cq.ID, strings.ToUpper(action[:1])+action[1:]+"d")
		return b.editDecisionMessage(ctx, cq, row, action, "")
	case "redo":
		// Consume the nonce immediately so a second Redo tap is ignored,
		// then ask for a one-line note before finalizing status=redo.
		if err := b.consumeNonce(ctx, row.ID, nonce); err != nil {
			_ = b.client.AnswerCallbackQuery(ctx, cq.ID, "error")
			return err
		}
		b.mu.Lock()
		b.awaitingRedo[chatID] = row.ID
		b.mu.Unlock()
		_ = b.client.AnswerCallbackQuery(ctx, cq.ID, "Send a redo note")
		_ = b.editDecisionMessage(ctx, cq, row, "redo", "(awaiting note)")
		return b.reply(ctx, chatID, "Reply with a one-line redo note for "+row.ID+":")
	default:
		_ = b.client.AnswerCallbackQuery(ctx, cq.ID, "unknown")
		return nil
	}
}

func (b *Bot) completeRedoNote(ctx context.Context, chatID int64, approvalID, note string) error {
	b.mu.Lock()
	delete(b.awaitingRedo, chatID)
	b.mu.Unlock()

	note = strings.TrimSpace(note)
	if note == "" {
		return b.reply(ctx, chatID, "Empty note ignored. Send /queue to see pending items.")
	}
	row, err := b.loadApproval(ctx, approvalID)
	if err != nil {
		return err
	}
	// Nonce already consumed; finalize redo if still pending.
	if row.Status != "pending" {
		return b.reply(ctx, chatID, "Approval already decided.")
	}
	if err := b.finalizeRedo(ctx, row, note); err != nil {
		return err
	}
	return b.reply(ctx, chatID, "Redo noted for "+approvalID+".")
}

func (b *Bot) decideApproval(ctx context.Context, row approvalRow, decision, note string) error {
	if b.approvals == nil {
		return fmt.Errorf("telegram: approval service not configured")
	}
	var d content.Decision
	switch decision {
	case "approve":
		d = content.DecisionApprove
	case "reject":
		d = content.DecisionReject
	case "redo":
		d = content.DecisionRedo
	default:
		return fmt.Errorf("telegram: unknown decision %q", decision)
	}
	err := b.approvals.Decide(ctx, content.DecideRequest{
		ApprovalID: row.ID,
		Decision:   d,
		Note:       note,
	})
	if err != nil {
		// Lost race / double-tap after status flip — treat as success/no-op.
		if strings.Contains(err.Error(), "already") {
			return nil
		}
		return fmt.Errorf("telegram: decide %s: %w", row.ID, err)
	}
	return nil
}

func (b *Bot) consumeNonce(ctx context.Context, id, nonce string) error {
	// Rotate nonce so a second callback with the old nonce fails, while
	// keeping status=pending until the redo note arrives.
	newNonce, err := newID()
	if err != nil {
		return err
	}
	res, err := b.db.ExecContext(ctx, `
UPDATE approvals SET nonce=? WHERE id=? AND status='pending' AND nonce=?`,
		newNonce, id, nonce,
	)
	if err != nil {
		return fmt.Errorf("telegram: consume nonce %s: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("telegram: approval %s nonce already used", id)
	}
	return nil
}

func (b *Bot) finalizeRedo(ctx context.Context, row approvalRow, note string) error {
	return b.decideApproval(ctx, row, "redo", note)
}

func (b *Bot) editDecisionMessage(ctx context.Context, cq *CallbackQuery, row approvalRow, action, extra string) error {
	if cq.Message == nil {
		return nil
	}
	when := time.Now().UTC().Format("15:04")
	label := map[string]string{
		"approve": "Approved",
		"reject":  "Rejected",
		"redo":    "Redo",
	}[action]
	text := fmt.Sprintf("%s\n\n%s by you %s", row.Summary, label, when)
	if extra != "" {
		text += " " + extra
	}
	if text == "\n\n"+label+" by you "+when+" "+extra || strings.TrimSpace(row.Summary) == "" {
		text = fmt.Sprintf("%s (%s)\n\n%s by you %s %s", row.ID, row.Kind, label, when, extra)
	}

	// Media messages use caption; text messages use text. Try caption first
	// when we know a preview was sent; fall back to text edit.
	hasPreview := row.PreviewPath.Valid && row.PreviewPath.String != ""
	var err error
	if hasPreview {
		err = b.client.EditMessageCaption(ctx, cq.Message.Chat.ID, cq.Message.MessageID, text, true)
		if err != nil {
			err = b.client.EditMessageText(ctx, cq.Message.Chat.ID, cq.Message.MessageID, text, true)
		}
	} else {
		err = b.client.EditMessageText(ctx, cq.Message.Chat.ID, cq.Message.MessageID, text, true)
	}
	if err != nil {
		b.log.Warn("telegram: edit decision message", "approval", row.ID, "error", err)
	}
	return nil
}

func (b *Bot) loadApproval(ctx context.Context, id string) (approvalRow, error) {
	var r approvalRow
	err := b.db.QueryRowContext(ctx, `
SELECT id, kind, summary, preview_path, status, nonce, telegram_message_id
FROM approvals WHERE id=?`, id).Scan(
		&r.ID, &r.Kind, &r.Summary, &r.PreviewPath, &r.Status, &r.Nonce, &r.MessageID,
	)
	if err == sql.ErrNoRows {
		return r, fmt.Errorf("telegram: approval %s not found", id)
	}
	if err != nil {
		return r, fmt.Errorf("telegram: load approval %s: %w", id, err)
	}
	return r, nil
}

func parseCallback(data string) (action, id, nonce string, ok bool) {
	parts := strings.Split(data, ":")
	if len(parts) != 3 {
		return "", "", "", false
	}
	action, id, nonce = parts[0], parts[1], parts[2]
	switch action {
	case "approve", "reject", "redo":
		if id == "" || nonce == "" {
			return "", "", "", false
		}
		return action, id, nonce, true
	default:
		return "", "", "", false
	}
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// underDataRoot reports whether path resolves inside root (config data_dir).
func underDataRoot(root, path string) bool {
	return confineUnderRoot(root, path) == nil
}

// confineUnderRoot refuses empty roots and any path that escapes root
// (including via .. or symlink-equivalent absolute paths outside).
func confineUnderRoot(root, path string) error {
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("data root not configured; refusing file upload")
	}
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("empty preview path")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve data root: %w", err)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve preview path: %w", err)
	}
	// EvalSymlinks so a symlink under data/ pointing outside is caught.
	if resolved, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = resolved
	}
	// File may not exist yet for the root check on the parent; EvalSymlinks
	// on the file itself fails if missing — fall back to cleaned abs path.
	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		absPath = resolved
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return fmt.Errorf("preview path not under data root")
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("preview path %s escapes data root", filepath.Base(path))
	}
	return nil
}

func isVideo(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".mov", ".webm", ".mkv":
		return true
	default:
		return false
	}
}

func isImage(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif":
		return true
	default:
		return false
	}
}
