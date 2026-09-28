// Package micro_saas turns a product idea into ARCHITECTURE/SPEC drafts for
// Builder (CONTEXT §2 line 2, M2-602).
//
// Schema: reuses content_items (kind=post, format=micro_saas_idea) — no new
// migration. Stages: submitted → spec_drafted → approved → handed_to_builder.
//
// LLM task: llm.TaskBuilder (Claude). Spec drafts feed builder.plan; CLAUDE.md
// reserves Claude for Builder/blog. config.example.yaml still lacks an explicit
// builder route entry (same gap M2-501 flagged) — wiring stays TaskBuilder.
//
// Repo hand-off: option (a) — target must already exist on disk and be listed
// in config.builder.repos (name + path). No auto-create / no guessed names.
//
// Revenue: RevenueNudge only reminds Mayank to POST /api/revenue (M2-601);
// this package never fabricates a revenue row.
package micro_saas

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mayank2/internal/config"
	"mayank2/internal/llm"
	"mayank2/internal/queue"
)

const (
	JobIdeaSubmit  = "idea.submit"
	JobIdeaDraft   = "idea.draft"
	JobIdeaHandOff = "idea.handoff"

	FormatIdea = "micro_saas_idea"

	StageSubmitted       = "submitted"
	StageSpecDrafted     = "spec_drafted"
	StageApproved        = "approved"
	StageHandedToBuilder = "handed_to_builder"

	approvalKindSpec = "micro_saas_spec"
	channelSentinel  = "builder" // same FK pattern as M2-501
)

// Completer is the LLM surface (satisfied by *llm.Router).
type Completer interface {
	Complete(ctx context.Context, task llm.Task, req llm.Request) (llm.Response, error)
}

// ApprovalStarter creates a pending approval (*content.ApprovalService).
type ApprovalStarter interface {
	Start(ctx context.Context, contentID, kind, summary, previewPath string) (string, error)
}

// Enqueuer enqueues follow-up jobs.
type Enqueuer interface {
	Enqueue(ctx context.Context, jobType string, payload any, opts ...queue.EnqueueOpt) (id string, err error)
}

// Service runs the idea → spec → Builder hand-off flow.
type Service struct {
	DB        *sql.DB
	LLM       Completer
	Approvals ApprovalStarter
	Queue     Enqueuer
	Enabled   bool
	Repos     []config.RepoConfig
	ChannelID string
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
	return newULID(s.now)
}

func (s *Service) channel() string {
	if s != nil && strings.TrimSpace(s.ChannelID) != "" {
		return strings.TrimSpace(s.ChannelID)
	}
	return channelSentinel
}

func (s *Service) findRepo(name string) (config.RepoConfig, bool) {
	want := strings.TrimSpace(name)
	for _, r := range s.Repos {
		if strings.TrimSpace(r.Name) == want {
			return r, true
		}
	}
	return config.RepoConfig{}, false
}

// IdeaInput is manual input from Mayank (dashboard / Telegram topic-style).
type IdeaInput struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	TargetUsers string `json:"target_users"`
	Notes       string `json:"notes"`
	// TargetRepo is config.builder.repos[].name (never a free-form guess).
	TargetRepo string `json:"target_repo"`
	RedoNotes  string `json:"redo_notes,omitempty"`
	ContentID  string `json:"content_id,omitempty"` // set on redo
}

// SubmitHandler adapts idea.submit to queue.Handler.
func (s *Service) SubmitHandler() queue.Handler {
	return func(ctx context.Context, job queue.Job) (json.RawMessage, error) {
		var in IdeaInput
		if len(job.Payload) > 0 && string(job.Payload) != "{}" {
			if err := json.Unmarshal(job.Payload, &in); err != nil {
				return nil, queue.Permanent(fmt.Errorf("micro_saas: submit: bad payload: %w", err))
			}
		}
		id, err := s.Submit(ctx, in)
		if err != nil {
			return nil, err
		}
		out, _ := json.Marshal(map[string]any{"content_id": id, "skipped": id == ""})
		return out, nil
	}
}

// DraftHandler adapts idea.draft to queue.Handler.
func (s *Service) DraftHandler() queue.Handler {
	return func(ctx context.Context, job queue.Job) (json.RawMessage, error) {
		var p struct {
			ContentID string `json:"content_id"`
		}
		if err := json.Unmarshal(job.Payload, &p); err != nil || strings.TrimSpace(p.ContentID) == "" {
			return nil, queue.Permanent(fmt.Errorf("micro_saas: draft: content_id required"))
		}
		if err := s.Draft(ctx, p.ContentID); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"content_id": p.ContentID})
	}
}

// HandOffHandler adapts idea.handoff to queue.Handler.
func (s *Service) HandOffHandler() queue.Handler {
	return func(ctx context.Context, job queue.Job) (json.RawMessage, error) {
		var p struct {
			ContentID string `json:"content_id"`
		}
		if err := json.Unmarshal(job.Payload, &p); err != nil || strings.TrimSpace(p.ContentID) == "" {
			return nil, queue.Permanent(fmt.Errorf("micro_saas: handoff: content_id required"))
		}
		if err := s.HandOff(ctx, p.ContentID); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"content_id": p.ContentID})
	}
}

// Submit creates/updates a content_items row and enqueues idea.draft.
// When builder.enabled is false, returns ("", nil) — clean no-op.
func (s *Service) Submit(ctx context.Context, in IdeaInput) (contentID string, err error) {
	if s == nil || s.DB == nil {
		return "", errors.New("micro_saas: nil service/db")
	}
	if !s.Enabled {
		return "", nil
	}
	title := strings.TrimSpace(in.Title)
	if title == "" && strings.TrimSpace(in.ContentID) == "" {
		return "", queue.Permanent(errors.New("micro_saas: title required"))
	}
	repoName := strings.TrimSpace(in.TargetRepo)
	if repoName == "" && strings.TrimSpace(in.ContentID) == "" {
		return "", queue.Permanent(errors.New("micro_saas: target_repo required"))
	}
	if repoName != "" {
		if _, ok := s.findRepo(repoName); !ok {
			return "", queue.Permanent(fmt.Errorf("micro_saas: target_repo %q not in builder.repos", repoName))
		}
	}

	id := strings.TrimSpace(in.ContentID)
	if id == "" {
		id = s.id()
		payload, _ := json.Marshal(map[string]string{
			"title": title, "description": in.Description,
			"target_users": in.TargetUsers, "notes": in.Notes,
			"target_repo": repoName, "redo_notes": in.RedoNotes,
		})
		_, err = s.DB.ExecContext(ctx, `
INSERT INTO content_items (id, channel_id, kind, format, language, stage, script, created_at)
VALUES (?, ?, 'post', ?, 'en', ?, ?, ?)`,
			id, s.channel(), FormatIdea, StageSubmitted, string(payload),
			s.now().Format(time.RFC3339Nano))
		if err != nil {
			return "", fmt.Errorf("micro_saas: insert idea: %w", err)
		}
	} else {
		meta, err := s.loadMeta(ctx, id)
		if err != nil {
			return "", err
		}
		if title != "" {
			meta["title"] = title
		}
		if strings.TrimSpace(in.Description) != "" {
			meta["description"] = in.Description
		}
		if strings.TrimSpace(in.TargetUsers) != "" {
			meta["target_users"] = in.TargetUsers
		}
		if strings.TrimSpace(in.Notes) != "" {
			meta["notes"] = in.Notes
		}
		if repoName != "" {
			meta["target_repo"] = repoName
		}
		meta["redo_notes"] = strings.TrimSpace(in.RedoNotes)
		updated, _ := json.Marshal(meta)
		_, err = s.DB.ExecContext(ctx, `
UPDATE content_items SET stage=?, script=? WHERE id=? AND format=?`,
			StageSubmitted, string(updated), id, FormatIdea)
		if err != nil {
			return "", fmt.Errorf("micro_saas: redo update: %w", err)
		}
	}
	if s.Queue != nil {
		if _, err := s.Queue.Enqueue(ctx, JobIdeaDraft, map[string]string{"content_id": id}); err != nil {
			return "", fmt.Errorf("micro_saas: enqueue draft: %w", err)
		}
	}
	return id, nil
}

// Draft generates ARCHITECTURE.md + SPEC.md text via TaskBuilder and requests approval.
func (s *Service) Draft(ctx context.Context, contentID string) error {
	if s == nil || s.DB == nil || s.LLM == nil {
		return errors.New("micro_saas: nil service/llm")
	}
	if !s.Enabled {
		return nil
	}
	meta, err := s.loadMeta(ctx, contentID)
	if err != nil {
		return err
	}
	prompt := buildSpecPrompt(meta)
	resp, err := s.LLM.Complete(ctx, llm.TaskBuilder, llm.Request{
		System:   "You draft ARCHITECTURE.md and SPEC.md for a new micro-SaaS. Output markdown only.",
		Messages: []llm.Message{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return fmt.Errorf("micro_saas: llm: %w", err)
	}
	meta["spec_draft"] = resp.Text
	updated, _ := json.Marshal(meta)
	_, err = s.DB.ExecContext(ctx, `
UPDATE content_items SET stage=?, script=? WHERE id=?`, StageSpecDrafted, string(updated), contentID)
	if err != nil {
		return err
	}
	if s.Approvals != nil {
		summary := fmt.Sprintf("Micro-SaaS idea: %s → approve spec for %s", meta["title"], meta["target_repo"])
		if _, err := s.Approvals.Start(ctx, contentID, approvalKindSpec, summary, ""); err != nil {
			return fmt.Errorf("micro_saas: approval: %w", err)
		}
	}
	return nil
}

// HandOff writes ARCHITECTURE.md / SPEC.md into the registered repo path after
// approval, advances stages approved → handed_to_builder, and enqueues builder.plan.
func (s *Service) HandOff(ctx context.Context, contentID string) error {
	if s == nil || s.DB == nil {
		return errors.New("micro_saas: nil service")
	}
	if !s.Enabled {
		return nil
	}
	// Latest row for this kind only (rowid = insert order). A newer pending/redo
	// after a prior approve must block hand-off — never prefer a stale approved.
	var status string
	err := s.DB.QueryRowContext(ctx, `
SELECT COALESCE(status,'') FROM approvals
WHERE content_id=? AND kind=?
ORDER BY rowid DESC
LIMIT 1`, contentID, approvalKindSpec).Scan(&status)
	if err == sql.ErrNoRows {
		return queue.Permanent(fmt.Errorf("micro_saas: not approved (no approval row)"))
	}
	if err != nil {
		return fmt.Errorf("micro_saas: load approval: %w", err)
	}
	if status != "approved" {
		return queue.Permanent(fmt.Errorf("micro_saas: not approved (status=%q)", status))
	}

	meta, err := s.loadMeta(ctx, contentID)
	if err != nil {
		return err
	}
	repo, ok := s.findRepo(meta["target_repo"])
	if !ok {
		return queue.Permanent(fmt.Errorf("micro_saas: target_repo %q not in builder.repos", meta["target_repo"]))
	}
	root := strings.TrimSpace(repo.Path)
	if root == "" {
		return queue.Permanent(fmt.Errorf("micro_saas: builder.repos[%q].path empty", repo.Name))
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return queue.Permanent(fmt.Errorf("micro_saas: target repo path missing (option a: Mayank must create+register it): %s", root))
	}

	if _, err := s.DB.ExecContext(ctx, `UPDATE content_items SET stage=? WHERE id=?`, StageApproved, contentID); err != nil {
		return err
	}

	arch, rem := splitArchSpec(meta["spec_draft"])
	if err := os.WriteFile(filepath.Join(root, "ARCHITECTURE.md"), []byte(arch+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "SPEC.md"), []byte(rem+"\n"), 0o644); err != nil {
		return err
	}

	if _, err := s.DB.ExecContext(ctx, `UPDATE content_items SET stage=? WHERE id=?`, StageHandedToBuilder, contentID); err != nil {
		return err
	}
	if s.Queue != nil {
		if _, err := s.Queue.Enqueue(ctx, "builder.plan", map[string]string{"repo": repo.Name}); err != nil {
			return fmt.Errorf("micro_saas: enqueue builder.plan: %w", err)
		}
	}
	return nil
}

// RevenueNudge reminds Mayank to record SaaS income after Builder ships a PR.
// Does not fabricate a revenue row (M2-601 owns POST /api/revenue).
func RevenueNudge(ideaTitle string) string {
	return fmt.Sprintf("Builder shipped %q — record first SaaS income on the Revenue screen when it arrives.", ideaTitle)
}

func (s *Service) loadMeta(ctx context.Context, contentID string) (map[string]string, error) {
	var scriptRaw string
	err := s.DB.QueryRowContext(ctx, `
SELECT COALESCE(script,'') FROM content_items WHERE id=? AND format=?`,
		contentID, FormatIdea).Scan(&scriptRaw)
	if err != nil {
		return nil, fmt.Errorf("micro_saas: load idea: %w", err)
	}
	meta := map[string]string{}
	_ = json.Unmarshal([]byte(scriptRaw), &meta)
	return meta, nil
}

func buildSpecPrompt(meta map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Title: %s\nDescription: %s\nTarget users: %s\nNotes: %s\n",
		meta["title"], meta["description"], meta["target_users"], meta["notes"])
	if n := strings.TrimSpace(meta["redo_notes"]); n != "" {
		fmt.Fprintf(&b, "\nRedo notes from Mayank:\n%s\n", n)
	}
	b.WriteString("\nProduce two sections headed exactly:\n# ARCHITECTURE.md\n# SPEC.md\n")
	return b.String()
}

func splitArchSpec(text string) (arch, spec string) {
	const aH, sH = "# ARCHITECTURE.md", "# SPEC.md"
	i := strings.Index(text, aH)
	j := strings.Index(text, sH)
	if i >= 0 && j > i {
		arch = strings.TrimSpace(text[i+len(aH) : j])
		spec = strings.TrimSpace(text[j+len(sH):])
		return arch, spec
	}
	return text, text
}

func newULID(now func() time.Time) string {
	var b [10]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%d%x", now().UnixMilli(), b[:])
}
