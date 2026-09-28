package blog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path"
	"strings"
	"time"

	"mayank2/internal/content"
	"mayank2/internal/queue"
)

// PlatformMayankbuilt is the publications.platform value for the canonical
// portfolio post (ARCHITECTURE section 3.2). Downstream tickets (M2-402/403/404)
// look this row up by content_id + platform.
const PlatformMayankbuilt = "mayankbuilt"

// MergeEnqueuer enqueues the follow-up blog.repurpose / blog.medium jobs.
type MergeEnqueuer interface {
	Enqueue(ctx context.Context, jobType string, payload any, opts ...queue.EnqueueOpt) (id string, err error)
}

// MergeOptions configures Merge. Git and DB are required.
type MergeOptions struct {
	Config Config
	Git    GitRepo
	DB     DB
	Now    func() time.Time
	NewID  func() string
	Logger *slog.Logger
	// Account is publications.account for the mayankbuilt row. Default
	// "mayankbuilt" -- a single portfolio site, not a multi-account platform.
	Account string
	// Enqueue, when set, chains a freshly-merged post to blog.repurpose
	// (M2-117 dispatcher, repurpose.go) and blog.medium (M2-404, medium.go)
	// on a genuinely new merge (ARCHITECTURE §3.2: merge -> live ->
	// repurpose / medium link). Optional — nil just skips the chain; a
	// replayed/already-merged run never re-chains (see Run).
	Enqueue MergeEnqueuer
}

// Merge implements the blog.merge job: after F7 approval, fast-forward
// merge blog/<slug> onto the configured base branch in the local
// Mayankbuilt clone and record the live URL on a publications row.
//
// blog.merge independently re-checks content.RequireApproved every run so
// it is safe even if the approve->enqueue wiring (outside this ticket's
// touches) is incomplete or raced.
type Merge struct {
	opts MergeOptions
	log  *slog.Logger
}

// NewMerge returns a Merge stage.
func NewMerge(opts MergeOptions) (*Merge, error) {
	if opts.Git == nil {
		return nil, fmt.Errorf("blog: Merge: Git is required")
	}
	if opts.DB == nil {
		return nil, fmt.Errorf("blog: Merge: DB is required")
	}
	opts.Config = opts.Config.withDefaults()
	if strings.TrimSpace(opts.Account) == "" {
		opts.Account = PlatformMayankbuilt
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Merge{opts: opts, log: log}, nil
}

func (m *Merge) now() time.Time {
	if m.opts.Now != nil {
		return m.opts.Now().UTC()
	}
	return time.Now().UTC()
}

func (m *Merge) newID() string {
	if m.opts.NewID != nil {
		return m.opts.NewID()
	}
	return newULID(m.now)
}

// MergePayload is the blog.merge job payload.
type MergePayload struct {
	ContentID string `json:"content_id"`
	Branch    string `json:"branch,omitempty"`
	Slug      string `json:"slug,omitempty"`
}

// MergeResult is the blog.merge job result payload.
type MergeResult struct {
	ContentID     string `json:"content_id"`
	Branch        string `json:"branch"`
	Slug          string `json:"slug"`
	LiveURL       string `json:"live_url,omitempty"`
	PublicationID string `json:"publication_id"`
	AlreadyMerged bool   `json:"already_merged,omitempty"`
}

// Handler adapts Merge to queue.Handler for Queue.Register(JobMerge, ...).
func (m *Merge) Handler() queue.Handler {
	return func(ctx context.Context, job queue.Job) (json.RawMessage, error) {
		var p MergePayload
		if len(job.Payload) > 0 && string(job.Payload) != "{}" {
			if err := json.Unmarshal(job.Payload, &p); err != nil {
				return nil, queue.Permanent(fmt.Errorf("blog: merge: bad payload: %w", err))
			}
		}
		res, err := m.Run(ctx, p)
		if err != nil {
			return nil, err
		}
		out, err := json.Marshal(res)
		if err != nil {
			return nil, fmt.Errorf("blog: merge: marshal result: %w", err)
		}
		return out, nil
	}
}

// Run executes one blog.merge attempt.
func (m *Merge) Run(ctx context.Context, p MergePayload) (*MergeResult, error) {
	contentID := strings.TrimSpace(p.ContentID)
	if contentID == "" {
		return nil, queue.Permanent(fmt.Errorf("blog: merge: content_id required"))
	}

	if err := requireBlogApproved(ctx, m.opts.DB, contentID); err != nil {
		if errors.Is(err, content.ErrNotApproved) {
			return nil, queue.Permanent(err)
		}
		return nil, fmt.Errorf("blog: merge: %w", err)
	}

	slug := strings.TrimSpace(p.Slug)
	branch := strings.TrimSpace(p.Branch)
	if slug == "" || branch == "" {
		topic, _, err := loadTopicFromDB(ctx, m.opts.DB, contentID)
		if err != nil {
			return nil, queue.Permanent(fmt.Errorf("blog: merge: resolve branch: %w", err))
		}
		if slug == "" {
			slug = slugify(topic)
		}
		if branch == "" {
			branch = "blog/" + slug
		}
	}
	if slug == "" || branch == "" {
		return nil, queue.Permanent(fmt.Errorf("blog: merge: empty slug/branch for content %q", contentID))
	}

	idemKey := "mayankbuilt:" + contentID
	if existing, ok, err := m.lookupPublished(ctx, idemKey); err != nil {
		return nil, err
	} else if ok {
		return &MergeResult{
			ContentID: contentID, Branch: branch, Slug: slug,
			LiveURL: existing.URL, PublicationID: existing.ID, AlreadyMerged: true,
		}, nil
	}

	if err := m.opts.Git.Fetch(ctx); err != nil {
		return nil, fmt.Errorf("blog: merge: %w", err)
	}

	already, err := m.opts.Git.AlreadyMerged(ctx, branch, m.opts.Config.BaseBranch)
	if err != nil {
		return nil, fmt.Errorf("blog: merge: %w", err)
	}
	if !already {
		if err := m.opts.Git.FastForwardMerge(ctx, branch, m.opts.Config.BaseBranch); err != nil {
			return nil, fmt.Errorf("blog: merge: %w", err)
		}
	}

	liveURL := livePostURL(m.opts.Config.SiteBaseURL, slug)
	pubID, err := m.recordPublication(ctx, contentID, idemKey, liveURL)
	if err != nil {
		return nil, fmt.Errorf("blog: merge: %w", err)
	}

	if _, err := m.opts.DB.ExecContext(ctx, `
UPDATE content_items SET stage=? WHERE id=?`, "published", contentID); err != nil {
		m.log.Warn("blog: merge: update stage failed (non-fatal)", "content_id", contentID, "error", err.Error())
	}

	// blog.medium (M2-404) is deliberately not chained here: it needs a
	// *telegram.Client sender (internal/blog/medium.go's TelegramSender),
	// which internal/telegram.Bot does not currently expose publicly, and
	// wiring that is outside this ticket's touches (cmd/mayank2/,
	// internal/config/, internal/content/, internal/media/,
	// internal/blog/{draft,merge,repurpose*}.go) — flagged in CONTEXT.md.
	if m.opts.Enqueue != nil {
		if _, err := m.opts.Enqueue.Enqueue(ctx, JobRepurpose, RepurposePayload{ContentID: contentID}, queue.ContentID(contentID)); err != nil {
			m.log.Warn("blog: merge: enqueue blog.repurpose failed (non-fatal)", "content_id", contentID, "error", err.Error())
		}
	}

	return &MergeResult{
		ContentID: contentID, Branch: branch, Slug: slug,
		LiveURL: liveURL, PublicationID: pubID, AlreadyMerged: already,
	}, nil
}

type pubRow struct {
	ID  string
	URL string
}

func (m *Merge) lookupPublished(ctx context.Context, idemKey string) (pubRow, bool, error) {
	var row pubRow
	err := m.opts.DB.QueryRowContext(ctx, `
SELECT id, COALESCE(url, '') FROM publications
WHERE idempotency_key=? AND status='published'`, idemKey).Scan(&row.ID, &row.URL)
	if errors.Is(err, sql.ErrNoRows) {
		return pubRow{}, false, nil
	}
	if err != nil {
		return pubRow{}, false, fmt.Errorf("lookup publication: %w", err)
	}
	return row, true, nil
}

func (m *Merge) recordPublication(ctx context.Context, contentID, idemKey, liveURL string) (string, error) {
	id := m.newID()
	now := m.now().Format(time.RFC3339Nano)
	_, err := m.opts.DB.ExecContext(ctx, `
INSERT INTO publications (
  id, content_id, platform, account, scheduled_at, status,
  external_id, url, idempotency_key, error, published_at
) VALUES (?, ?, ?, ?, NULL, 'published', ?, ?, ?, NULL, ?)
ON CONFLICT(idempotency_key) DO UPDATE SET
  status='published',
  external_id=excluded.external_id,
  url=excluded.url,
  error=NULL,
  published_at=excluded.published_at
`, id, contentID, PlatformMayankbuilt, m.opts.Account, slugFromBranchOrURL(liveURL, idemKey), liveURL, idemKey, now)
	if err != nil {
		return "", fmt.Errorf("insert publications: %w", err)
	}
	var stored string
	if err := m.opts.DB.QueryRowContext(ctx, `
SELECT id FROM publications WHERE idempotency_key=?`, idemKey).Scan(&stored); err != nil {
		return id, nil
	}
	return stored, nil
}

func requireBlogApproved(ctx context.Context, db DB, contentID string) error {
	if sqlDB, ok := db.(*sql.DB); ok {
		return content.RequireApproved(ctx, sqlDB, contentID)
	}
	if contentID == "" {
		return fmt.Errorf("%w: empty content_id", content.ErrNotApproved)
	}
	var status string
	err := db.QueryRowContext(ctx, `
SELECT status FROM approvals
WHERE content_id=? AND status='approved'
ORDER BY decided_at DESC LIMIT 1`, contentID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s", content.ErrNotApproved, contentID)
	}
	if err != nil {
		return fmt.Errorf("blog: check approval %s: %w", contentID, err)
	}
	return nil
}

func loadTopicFromDB(ctx context.Context, db DB, contentID string) (topic, source string, err error) {
	var raw sql.NullString
	err = db.QueryRowContext(ctx, `SELECT script FROM content_items WHERE id=?`, contentID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", fmt.Errorf("content_items %s not found", contentID)
	}
	if err != nil {
		return "", "", err
	}
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return "", "", fmt.Errorf("content_items %s has no stored topic", contentID)
	}
	var parsed struct {
		Topic        string `json:"topic"`
		TopicsSource string `json:"topics_source"`
	}
	if err := json.Unmarshal([]byte(raw.String), &parsed); err != nil {
		return "", "", fmt.Errorf("content_items %s: parse stored topic: %w", contentID, err)
	}
	if strings.TrimSpace(parsed.Topic) == "" {
		return "", "", fmt.Errorf("content_items %s: stored topic is empty", contentID)
	}
	return parsed.Topic, parsed.TopicsSource, nil
}

func livePostURL(siteBase, slug string) string {
	base := strings.TrimSpace(siteBase)
	slug = strings.Trim(strings.TrimSpace(slug), "/")
	if base == "" || slug == "" {
		return ""
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return strings.TrimRight(base, "/") + "/blog/" + slug
	}
	u.Path = path.Join(strings.TrimSuffix(u.Path, "/"), "blog", slug)
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func slugFromBranchOrURL(liveURL, idemKey string) string {
	if liveURL != "" {
		if u, err := url.Parse(liveURL); err == nil {
			base := path.Base(strings.TrimSuffix(u.Path, "/"))
			if base != "" && base != "." && base != "/" {
				return base
			}
		}
	}
	if i := strings.LastIndex(idemKey, ":"); i >= 0 && i+1 < len(idemKey) {
		return idemKey[i+1:]
	}
	return idemKey
}
