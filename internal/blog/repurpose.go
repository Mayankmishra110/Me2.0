// Repurpose dispatcher (M2-117): SPEC §5 lists one shared job type,
// "blog.repurpose", for turning a live Mayankbuilt post into its
// LinkedIn (M2-402, repurpose_linkedin.go) and X-personal (M2-403,
// repurpose_x.go) drafts (ARCHITECTURE §3.2: "blog.repurpose ->
// linkedin.post + x_personal.thread drafts -> approval -> publish").
//
// Neither leg registered a handler for that shared name: repurpose_x.go
// deliberately used its own "blog.repurpose_x_personal" job type instead,
// with a comment explaining why (registering "blog.repurpose" from two
// sibling tickets built in parallel worktrees would collide — queue.Register
// panics on a duplicate job type) and flagging "a follow-up doc/dispatcher
// ticket once all three legs exist" as the real fix. repurpose_linkedin.go
// has no queue.Handler at all (its Run method is designed to be called
// directly). This file is that follow-up: one handler that owns the literal
// "blog.repurpose" job type and fans out to both legs in-process, so SPEC's
// job-type list is satisfied without re-registering XRepurposer under a
// second name.
package blog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"mayank2/internal/queue"
)

// JobRepurpose is the queue job type (SPEC §5).
const JobRepurpose = "blog.repurpose"

// RepurposeOptions configures Dispatcher. DB is required; at least one of
// LinkedIn/X should be set or the job is a no-op.
type RepurposeOptions struct {
	DB       *sql.DB
	LinkedIn *Repurposer  // M2-402 LinkedIn leg; nil skips it
	X        *XRepurposer // M2-403 X-personal leg; nil skips it
	Logger   *slog.Logger
}

// Dispatcher fans "blog.repurpose" out to the LinkedIn and X-personal legs.
type Dispatcher struct {
	opts RepurposeOptions
	log  *slog.Logger
}

// NewDispatcher returns a Dispatcher.
func NewDispatcher(opts RepurposeOptions) (*Dispatcher, error) {
	if opts.DB == nil {
		return nil, fmt.Errorf("blog: repurpose: DB is required")
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Dispatcher{opts: opts, log: log}, nil
}

// RegisterHandler registers blog.repurpose on the net resource class (LLM
// calls + DB reads only — not a local model or render).
func (d *Dispatcher) RegisterHandler(q *queue.Queue) {
	if q == nil || d == nil {
		return
	}
	q.Register(JobRepurpose, queue.ResourceNet, 3, d.handle)
}

// RepurposePayload is the blog.repurpose job body: the source content_id of
// the now-live Mayankbuilt post (blog.merge's ContentID).
type RepurposePayload struct {
	ContentID string `json:"content_id"`
}

// RepurposeResult reports each leg's outcome. A leg that errors does not
// fail the whole job — the other leg still gets its own approval gate — but
// the job itself returns an error (so the queue retries) unless at least
// one leg succeeded.
type RepurposeResult struct {
	ContentID          string `json:"content_id"`
	LinkedInApprovalID string `json:"linkedin_approval_id,omitempty"`
	LinkedInError      string `json:"linkedin_error,omitempty"`
	XContentID         string `json:"x_content_id,omitempty"`
	XApprovalID        string `json:"x_approval_id,omitempty"`
	XError             string `json:"x_error,omitempty"`
}

func (d *Dispatcher) handle(ctx context.Context, job queue.Job) (json.RawMessage, error) {
	var p RepurposePayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return nil, queue.Permanent(fmt.Errorf("blog: repurpose: payload: %w", err))
	}
	contentID := strings.TrimSpace(p.ContentID)
	if contentID == "" && job.ContentID != nil {
		contentID = strings.TrimSpace(*job.ContentID)
	}
	if contentID == "" {
		return nil, queue.Permanent(fmt.Errorf("blog: repurpose: content_id required"))
	}

	src, err := LoadSourcePost(ctx, d.opts.DB, contentID)
	if err != nil {
		return nil, fmt.Errorf("blog: repurpose: %w", err)
	}

	res := RepurposeResult{ContentID: contentID}
	ok := false

	if d.opts.LinkedIn != nil {
		draft, err := d.opts.LinkedIn.Run(ctx, RunInput{Source: src})
		if err != nil {
			res.LinkedInError = err.Error()
			d.log.Warn("blog: repurpose linkedin leg failed", "content_id", contentID, "error", err.Error())
		} else {
			res.LinkedInApprovalID = draft.ApprovalID
			ok = true
		}
	}

	if d.opts.X != nil {
		// XRepurposer.Start requires src.Title (LoadSourcePost does not set
		// it — the mayankbuilt MDX body has its title stripped along with
		// the rest of the frontmatter). Best-effort fallback: the topic
		// string stored on this content item at draft/merge time.
		xSrc := src
		if strings.TrimSpace(xSrc.Title) == "" {
			if topic, _, terr := loadTopicFromDB(ctx, d.opts.DB, contentID); terr == nil {
				xSrc.Title = topic
			}
		}
		xContentID, approvalID, err := d.opts.X.Start(ctx, xSrc)
		if err != nil {
			res.XError = err.Error()
			d.log.Warn("blog: repurpose x_personal leg failed", "content_id", contentID, "error", err.Error())
		} else {
			res.XContentID = xContentID
			res.XApprovalID = approvalID
			ok = true
		}
	}

	out, merr := json.Marshal(res)
	if merr != nil {
		return nil, fmt.Errorf("blog: repurpose: marshal result: %w", merr)
	}
	if !ok && (d.opts.LinkedIn != nil || d.opts.X != nil) {
		return out, fmt.Errorf("blog: repurpose: both legs failed (linkedin=%q x=%q)", res.LinkedInError, res.XError)
	}
	return out, nil
}
