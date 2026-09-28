// Package agency tracks leads and drafts outreach/proposal text (CONTEXT §2
// line 3, M2-603). Drafts route through llm.TaskScript (free-tier chain) —
// never Claude (CLAUDE.md non-negotiable). No send/email/DM clients exist
// here; approved drafts sit until Mayank sends them manually (PRD §5).
package agency

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"mayank2/internal/llm"
	"mayank2/internal/queue"
)

const (
	JobDraftProposal = "draft.proposal"

	StatusNew          = "new"
	StatusContacted    = "contacted"
	StatusProposalSent = "proposal_sent"
	StatusWon          = "won"
	StatusLost         = "lost"

	PropDrafting        = "drafting"
	PropPendingApproval = "pending_approval"
	PropApproved        = "approved"
	PropRejected        = "rejected"
)

// Completer is the LLM surface — tests assert TaskScript, never TaskBlog/Builder.
type Completer interface {
	Complete(ctx context.Context, task llm.Task, req llm.Request) (llm.Response, error)
}

// ApprovalStarter creates pending approvals.
type ApprovalStarter interface {
	Start(ctx context.Context, contentID, kind, summary, previewPath string) (string, error)
}

// Service owns lead CRUD + proposal drafts.
type Service struct {
	DB        *sql.DB
	LLM       Completer
	Approvals ApprovalStarter
	Now       func() time.Time
	NewID     func() string
}

func (s *Service) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) id() string {
	if s != nil && s.NewID != nil {
		return s.NewID()
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%d%x", s.now().UnixMilli(), b[:])
}

// Lead is one agency_leads row.
type Lead struct {
	ID        string `json:"id"`
	Company   string `json:"company"`
	Contact   string `json:"contact"`
	NicheFit  string `json:"niche_fit"`
	Status    string `json:"status"`
	Source    string `json:"source"`
	CreatedAt string `json:"created_at"`
}

// CreateLead inserts a manual lead entry.
func (s *Service) CreateLead(ctx context.Context, company, contact, nicheFit, source string) (Lead, error) {
	var zero Lead
	if s == nil || s.DB == nil {
		return zero, errors.New("agency: nil service")
	}
	company = strings.TrimSpace(company)
	if company == "" {
		return zero, errors.New("agency: company required")
	}
	id := s.id()
	at := s.now().Format(time.RFC3339Nano)
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO agency_leads (id, company, contact, niche_fit, status, source, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, company, strings.TrimSpace(contact), strings.TrimSpace(nicheFit),
		StatusNew, strings.TrimSpace(source), at)
	if err != nil {
		return zero, fmt.Errorf("agency: insert lead: %w", err)
	}
	return Lead{ID: id, Company: company, Contact: contact, NicheFit: nicheFit,
		Status: StatusNew, Source: source, CreatedAt: at}, nil
}

// ListLeads returns leads newest first.
func (s *Service) ListLeads(ctx context.Context) ([]Lead, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT id, company, contact, niche_fit, status, source, created_at
FROM agency_leads ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Lead
	for rows.Next() {
		var l Lead
		if err := rows.Scan(&l.ID, &l.Company, &l.Contact, &l.NicheFit, &l.Status, &l.Source, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// SetLeadStatus updates status; won nudges revenue recording (no auto row).
func (s *Service) SetLeadStatus(ctx context.Context, id, status string) (revenueNudge string, err error) {
	switch status {
	case StatusNew, StatusContacted, StatusProposalSent, StatusWon, StatusLost:
	default:
		return "", fmt.Errorf("agency: bad status %q", status)
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE agency_leads SET status=? WHERE id=?`, status, id)
	if err != nil {
		return "", err
	}
	if status == StatusWon {
		return fmt.Sprintf("Lead %s marked won — record agency income on the Revenue screen.", id), nil
	}
	return "", nil
}

// DraftProposal generates outreach text via TaskScript and requests approval.
// Never imports or calls any email/DM sending client.
func (s *Service) DraftProposal(ctx context.Context, leadID, redoNotes string) (proposalID string, err error) {
	if s == nil || s.DB == nil || s.LLM == nil {
		return "", errors.New("agency: nil service/llm")
	}
	var company, contact, niche string
	err = s.DB.QueryRowContext(ctx, `
SELECT company, contact, niche_fit FROM agency_leads WHERE id=?`, leadID).
		Scan(&company, &contact, &niche)
	if err != nil {
		return "", fmt.Errorf("agency: load lead: %w", err)
	}
	user := fmt.Sprintf("Draft a short cold outreach / proposal email for:\nCompany: %s\nContact: %s\nNiche fit: %s\n",
		company, contact, niche)
	if strings.TrimSpace(redoNotes) != "" {
		user += "\nRedo notes from Mayank:\n" + redoNotes + "\n"
	}
	resp, err := s.LLM.Complete(ctx, llm.TaskScript, llm.Request{
		System:   "You write concise US small-business automation agency proposals. Plain text only. No Claude.",
		Messages: []llm.Message{{Role: "user", Content: user}},
	})
	if err != nil {
		return "", fmt.Errorf("agency: llm: %w", err)
	}
	pid := s.id()
	at := s.now().Format(time.RFC3339Nano)
	_, err = s.DB.ExecContext(ctx, `
INSERT INTO agency_proposals (id, lead_id, draft_text, status, created_at)
VALUES (?, ?, ?, ?, ?)`, pid, leadID, resp.Text, PropDrafting, at)
	if err != nil {
		return "", err
	}

	// Approvals table requires content_id FK → seed a lightweight content_items row.
	if err := s.ensureContentRow(ctx, leadID); err != nil {
		return "", err
	}
	var approvalID string
	if s.Approvals != nil {
		approvalID, err = s.Approvals.Start(ctx, leadID, "agency_proposal",
			fmt.Sprintf("Agency proposal for %s", company), "")
		if err != nil {
			return "", fmt.Errorf("agency: approval: %w", err)
		}
	}
	_, err = s.DB.ExecContext(ctx, `
UPDATE agency_proposals SET status=?, approval_id=? WHERE id=?`,
		PropPendingApproval, nullStr(approvalID), pid)
	if err != nil {
		return "", err
	}
	_ = json.RawMessage(nil) // keep encoding/json for future payload shapes
	return pid, nil
}

// MarkApproved sets proposal approved after human decision — never sends.
func (s *Service) MarkApproved(ctx context.Context, proposalID string) error {
	_, err := s.DB.ExecContext(ctx, `
UPDATE agency_proposals SET status=? WHERE id=?`, PropApproved, proposalID)
	return err
}

func (s *Service) ensureContentRow(ctx context.Context, id string) error {
	// channel sentinel for agency text approvals
	_, err := s.DB.ExecContext(ctx, `
INSERT OR IGNORE INTO channels (id, platform, handle, language, niche, account_ref, status)
VALUES ('agency', 'agency', '', 'en', 'agency', '', 'active')`)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `
INSERT OR IGNORE INTO content_items (id, channel_id, kind, format, language, stage, created_at)
VALUES (?, 'agency', 'post', 'agency_proposal', 'en', 'approval', ?)`,
		id, s.now().Format(time.RFC3339Nano))
	return err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Handler returns a queue handler for JobDraftProposal.
func (s *Service) Handler() queue.Handler {
	return func(ctx context.Context, job queue.Job) (json.RawMessage, error) {
		var p struct {
			LeadID    string `json:"lead_id"`
			RedoNotes string `json:"redo_notes"`
		}
		if err := json.Unmarshal(job.Payload, &p); err != nil {
			return nil, queue.Permanent(err)
		}
		id, err := s.DraftProposal(ctx, p.LeadID, p.RedoNotes)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"proposal_id": id})
	}
}
