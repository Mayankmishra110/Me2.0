// Package blog is the Go side of the blog pipeline that runs after M2-401's
// `blog.merge` job puts the canonical post live on Mayankbuilt. medium.go
// (M2-404) is the Medium leg of that pipeline.
//
// CONTEXT D19: Medium stopped issuing integration tokens, so there is no
// usable publishing API for it. This file never publishes anything and
// never talks to Medium at all. All it does is:
//  1. read the now-live canonical post's public URL off the `publications`
//     row M2-401 wrote (platform = "mayankbuilt"),
//  2. build Medium's "Import a story" URL from that public URL, and
//  3. send that link to Mayank over Telegram, using the same raw Bot API
//     client M2-105/106 already use, so he can tap it and finish the import
//     himself inside Medium's own UI.
//
// That third step is the only "publish" here, and it is Mayank's own manual
// action — not this daemon's. Accordingly this file:
//   - never calls approval.request / D5 (nothing is actually published by
//     this job; there is nothing to approve),
//   - never writes a `publications` row for platform "medium" (that would
//     read as an automated Medium publish that never happened; AC explicitly
//     forbids it),
//   - tracks "link sent" via `content_items.stage` + an `events` row instead.
package blog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"mayank2/internal/events"
	"mayank2/internal/queue"
	"mayank2/internal/telegram"
)

// JobBlogMedium is the queue job type this file registers (SPEC §5's
// job-type list predates this ticket and does not enumerate a Medium job:
// it only lists blog.draft, blog.merge, blog.repurpose. ARCHITECTURE §3.2's
// flow text (line 117) draws Medium as its own line, separate from
// blog.repurpose's LinkedIn/X drafts, immediately after blog.merge. Ticket
// M2-404 Notes says either wiring (own trigger off blog.merge, or a
// blog.repurpose destination case) satisfies the acceptance criteria and to
// pick whichever is simplest. This file registers its own job type;
// M2-121 wires the actual trigger (blog.merge enqueues it directly,
// internal/blog/merge.go's Run, mirroring the existing blog.repurpose
// chain) and the daemon registration (cmd/mayank2/run.go).
const JobBlogMedium = "blog.medium"

// stageMediumLinkSent is the content_items.stage value this file uses to
// remember "the Medium import link was already sent for this content item"
// (content_items.stage has no CHECK constraint in migrations/001_init.sql,
// so this is a safe, additive value). Acceptance criteria explicitly rules
// out a `publications` row for this — that would look like a real, automated
// Medium publish, which never happens here.
const stageMediumLinkSent = "medium_link_sent"

// EventMediumLinkSent is the events.kind written once the Telegram message
// has been sent, for dashboard/audit visibility (ARCHITECTURE §4's events
// table is the general append-only log, not only the SSE-stream kinds).
const EventMediumLinkSent = "blog.medium_link_sent"

// MediumImportBaseURL is Medium's historically documented "Import a story"
// entry point.
//
// UNVERIFIED — flagged explicitly per ticket M2-404 Notes: this is the
// best-documented *historical* shape of Medium's importer
// (medium.com/p/import?url=<source>), not a confirmed-current one. Medium
// has changed this flow's URL before, may require the visitor to already be
// signed in, and may not accept a bare query parameter at all today. Do NOT
// treat a link built by BuildImportURL as reliable for a real import without
// first checking it against Medium's actual current site/UI.
const MediumImportBaseURL = "https://medium.com/p/import"

// mediumImportURLParam is the query parameter Medium's import flow has
// historically read the source article URL from. Same "unverified, must be
// checked against the live site" caveat as MediumImportBaseURL.
const mediumImportURLParam = "url"

// BuildImportURL builds Medium's "Import a story" link for sourceURL, which
// must be the canonical Mayankbuilt post's own absolute http(s) public URL
// (e.g. one read from `publications.url` where platform = "mayankbuilt").
//
// See the UNVERIFIED caveat on MediumImportBaseURL: this function encodes
// this package's best documented guess at Medium's import-URL shape, not a
// confirmed one.
func BuildImportURL(sourceURL string) (string, error) {
	trimmed := strings.TrimSpace(sourceURL)
	if trimmed == "" {
		return "", fmt.Errorf("blog: medium: empty source url")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("blog: medium: parse source url %q: %w", trimmed, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("blog: medium: source url %q is not an absolute http(s) url", trimmed)
	}
	if u.Host == "" {
		return "", fmt.Errorf("blog: medium: source url %q has no host", trimmed)
	}

	base, err := url.Parse(MediumImportBaseURL)
	if err != nil {
		// MediumImportBaseURL is a package constant; a parse failure here
		// would be a programming error, not caller input.
		return "", fmt.Errorf("blog: medium: parse import base url: %w", err)
	}
	q := base.Query()
	q.Set(mediumImportURLParam, trimmed)
	base.RawQuery = q.Encode()
	return base.String(), nil
}

// TelegramSender is the subset of *telegram.Client (internal/telegram,
// M2-105) this file needs. The real *telegram.Client satisfies this
// signature already — this file deliberately reuses telegram.Client's own
// SendMessage rather than inventing a second delivery path (ticket
// instruction: "match that existing delivery pattern, don't invent a new
// one"). Tests inject a fake implementation instead of standing up an
// httptest Bot API server, since no HTTP/multipart wiring is exercised here
// beyond what internal/telegram already tests itself.
//
// Note: telegram.InlineKeyboardButton (internal/telegram/types.go) only
// supports callback_data buttons, not a Telegram "url" button — adding that
// would mean editing internal/telegram, which is out of this ticket's
// `touches` (internal/blog/medium.go only). So this file delivers the
// import link as plain message text with markup=nil; Telegram's clients
// auto-linkify a bare http(s) URL in message text, so it is still one tap
// for Mayank, just not rendered as a distinct button.
type TelegramSender interface {
	SendMessage(ctx context.Context, chatID int64, text string, markup *telegram.InlineKeyboardMarkup) (int64, error)
}

// MediumPayload is the blog.medium job body (exported, matching
// MergePayload/RepurposePayload/DraftPayload's naming convention, so
// blog.merge — M2-121 — can construct one directly when chaining into this
// job type after a live merge).
type MediumPayload struct {
	ContentID string `json:"content_id"`
}

// Medium is the Medium leg of the blog pipeline: no API, no OAuth, no
// token — it only builds an import link and delivers it to Mayank.
type Medium struct {
	DB     *sql.DB
	Bus    *events.Bus // optional; nil skips the events row
	Sender TelegramSender
	ChatID int64 // Mayank's allowlisted Telegram chat (same config as internal/telegram)
	Now    func() time.Time
	Log    *slog.Logger
}

func (m *Medium) now() time.Time {
	if m != nil && m.Now != nil {
		return m.Now().UTC()
	}
	return time.Now().UTC()
}

func (m *Medium) log() *slog.Logger {
	if m != nil && m.Log != nil {
		return m.Log
	}
	return slog.Default()
}

// RegisterHandler wires blog.medium onto the net resource class (it makes
// one outbound Telegram HTTPS call).
func (m *Medium) RegisterHandler(q *queue.Queue) {
	if q == nil || m == nil {
		return
	}
	q.Register(JobBlogMedium, queue.ResourceNet, 5, m.Handle)
}

// Handle runs one blog.medium job: idempotent — a content item whose Medium
// link was already sent is a no-op success, not a duplicate Telegram
// message. Re-sending on request is a separate, explicit re-request path
// (a fresh job with the stage reset by whatever calls this), never an
// automatic re-trigger from re-running this handler.
func (m *Medium) Handle(ctx context.Context, job queue.Job) (json.RawMessage, error) {
	if m == nil || m.DB == nil {
		return nil, queue.Permanent(fmt.Errorf("blog: medium: nil service/db"))
	}
	if m.Sender == nil {
		return nil, queue.Permanent(fmt.Errorf("blog: medium: nil telegram sender"))
	}

	var p MediumPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return nil, queue.Permanent(fmt.Errorf("blog: medium: decode payload: %w", err))
	}
	p.ContentID = strings.TrimSpace(p.ContentID)
	if p.ContentID == "" {
		return nil, queue.Permanent(fmt.Errorf("blog: medium: missing content_id"))
	}

	stage, err := m.loadStage(ctx, p.ContentID)
	if err != nil {
		return nil, err
	}
	if stage == stageMediumLinkSent {
		return json.RawMessage(`{"skipped":"already_sent"}`), nil
	}

	postURL, err := m.canonicalPublicURL(ctx, p.ContentID)
	if err != nil {
		return nil, err
	}
	importURL, err := BuildImportURL(postURL)
	if err != nil {
		return nil, queue.Permanent(fmt.Errorf("blog: medium: build import url for content %s: %w", p.ContentID, err))
	}

	msgID, err := m.Sender.SendMessage(ctx, m.ChatID, importMessageText(postURL, importURL), nil)
	if err != nil {
		return nil, fmt.Errorf("blog: medium: send telegram message for content %s: %w", p.ContentID, err)
	}

	if err := m.markSent(ctx, p.ContentID); err != nil {
		// The Telegram message already went out; a failure recording that
		// fact must not silently look like success, but it also must not
		// pretend nothing was sent — surface it so a retry can be judged by
		// a human rather than automatically re-spamming Mayank.
		return nil, fmt.Errorf("blog: medium: mark link sent for content %s (telegram message %d already sent): %w", p.ContentID, msgID, err)
	}

	if m.Bus != nil {
		_, emitErr := m.Bus.Emit(ctx, events.ActorSystem, EventMediumLinkSent, p.ContentID,
			"Medium import link sent to Mayank; import itself is a manual step in Medium's own UI",
			map[string]string{"import_url": importURL, "post_url": postURL},
		)
		if emitErr != nil {
			m.log().Warn("blog: medium: emit event", "content_id", p.ContentID, "error", emitErr)
		}
	}

	result, _ := json.Marshal(map[string]any{
		"telegram_message_id": msgID,
		"import_url":          importURL,
	})
	return result, nil
}

func (m *Medium) loadStage(ctx context.Context, contentID string) (string, error) {
	var stage string
	err := m.DB.QueryRowContext(ctx, `SELECT stage FROM content_items WHERE id = ?`, contentID).Scan(&stage)
	if err == sql.ErrNoRows {
		return "", queue.Permanent(fmt.Errorf("blog: medium: content_items %s not found", contentID))
	}
	if err != nil {
		return "", fmt.Errorf("blog: medium: load content_items %s: %w", contentID, err)
	}
	return stage, nil
}

// canonicalPublicURL reads the live Mayankbuilt URL M2-401's blog.merge
// wrote to `publications` (platform = "mayankbuilt"). A missing row is
// treated as retryable, not permanent: it can legitimately mean blog.merge
// hasn't committed yet if this job ever gets enqueued ahead of that (e.g. a
// racing retry), and retrying costs nothing since no Telegram send has
// happened yet.
func (m *Medium) canonicalPublicURL(ctx context.Context, contentID string) (string, error) {
	var postURL string
	err := m.DB.QueryRowContext(ctx, `
SELECT url FROM publications
WHERE content_id = ? AND platform = 'mayankbuilt' AND url IS NOT NULL AND url != ''
ORDER BY published_at DESC, id DESC
LIMIT 1`, contentID).Scan(&postURL)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("blog: medium: no mayankbuilt publication url yet for content %s", contentID)
	}
	if err != nil {
		return "", fmt.Errorf("blog: medium: load mayankbuilt publication for content %s: %w", contentID, err)
	}
	return postURL, nil
}

func (m *Medium) markSent(ctx context.Context, contentID string) error {
	_, err := m.DB.ExecContext(ctx, `UPDATE content_items SET stage = ? WHERE id = ?`, stageMediumLinkSent, contentID)
	return err
}

func importMessageText(postURL, importURL string) string {
	return fmt.Sprintf(
		"Blog post is live: %s\n\n"+
			"Medium import link (tap it, then finish the import yourself in Medium's own UI — this is not an automated publish, just a prepared link):\n%s",
		postURL, importURL,
	)
}
