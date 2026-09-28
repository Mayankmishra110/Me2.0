// Package blog is the Mayankbuilt blog pipeline: the canonical post
// (M2-401: internal/blog/draft.go, merge.go — not yet built as of this
// file) and its cross-platform repurposing legs (ARCHITECTURE §3.2,
// SPEC §5 job type `blog.repurpose`). This file is the LinkedIn leg
// (M2-402): it turns the live, approved-and-merged Mayankbuilt post into a
// LinkedIn-native repost — shorter, its own hook, never a copy-paste — via
// internal/llm.Complete(ctx, "blog", req). Task "blog" is the one deliberate
// Claude route outside Builder (CLAUDE.md "Claude is for Builder and blog
// only"; CONTEXT.md D13): this is still rewriting the same canonical post,
// not a content-engine task, so it stays on that route.
//
// Nothing here calls the LinkedIn API directly — internal/publish/linkedin.go
// owns that, and only after this draft clears human approval (D5/F7).
package blog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"unicode"

	"mayank2/internal/llm"
	"mayank2/internal/queue"
)

const (
	// PlatformMayankbuilt is publications.platform for the canonical post
	// (M2-401). PlatformLinkedIn is publications.platform / oauth_tokens.platform
	// for this leg (matches internal/publish/linkedin.go's Platform()).
	PlatformMayankbuilt = "mayankbuilt"
	PlatformLinkedIn    = "linkedin"

	// approvalKindLinkedIn is approvals.kind for this leg's approval row.
	// approvals.kind has no CHECK constraint (unlike content_items.kind), so
	// this is a plain descriptive tag — distinct from the "short"/"long"
	// video kinds and from the "blog" kind used for the original
	// draft/merge approval on the same content_id. ApprovalService.RequireApproved
	// (internal/content/approval.go) resolves the most-recently-decided
	// approval per content_id, so this only works correctly once the
	// original blog-merge approval has already been decided — true by
	// construction, since this leg only runs after the post is live
	// (ARCHITECTURE §3.2: draft → approve → merge → live → repurpose).
	approvalKindLinkedIn = "linkedin"

	// minSharedRun mirrors COMPLIANCE.md G1's "no shared run > 12 words"
	// threshold — not a re-run of G1 itself (that gate is script-side, not
	// ours to invoke), just the same originality bar applied to the LinkedIn
	// rewrite per this ticket's own AC ("not a raw copy-paste").
	minSharedRun = 12

	maxCompleteAttempts = 2

	// linkedInSoftCharLimit is a defensive trim so publish/linkedin.go's
	// CreatePost never gets rejected for length. *(verify)* LinkedIn's
	// documented UGC post text limit as of last known docs is ~3000
	// characters for personal posts — confirm against live API docs
	// before relying on this for anything but a safety margin.
	linkedInSoftCharLimit = 2900
)

// ErrDraftNotOriginal means the LLM's draft failed the "not a copy-paste"
// check after all retries.
var ErrDraftNotOriginal = errors.New("blog: linkedin draft not sufficiently original")

// Completer is the LLM surface this stage needs (satisfied by *llm.Router).
// Deliberately a local, minimal interface (small-package convention used
// throughout internal/content) rather than importing internal/content's.
type Completer interface {
	Complete(ctx context.Context, task llm.Task, req llm.Request) (llm.Response, error)
}

// Approver creates the human approval gate for this draft. Satisfied by
// *internal/content.ApprovalService (its Start method has this exact
// signature); a minimal local interface keeps this package's tests free of
// a full ApprovalService/DB wire-up.
type Approver interface {
	Start(ctx context.Context, contentID, kind, summary, previewPath string) (string, error)
}

// SourcePost is the live, canonical Mayankbuilt post this leg repurposes.
type SourcePost struct {
	ContentID string
	URL       string // canonical live URL
	Body      string // plain-text/markdown body (MDX with frontmatter stripped)
}

// Draft is the produced LinkedIn-native repost, still pending approval.
type Draft struct {
	Text       string // LinkedIn post body, includes the link back to Source.URL
	ApprovalID string
}

// LoadSourcePost reads the live Mayankbuilt post for contentID.
//
// NOT FULLY CERTAIN — flagged for review once M2-401 lands: `publications`
// (migrations/001_init.sql) has no body/content column, only
// id/content_id/platform/account/scheduled_at/status/external_id/url/
// idempotency_key/error/published_at. So URL comes from the `publications`
// row (platform="mayankbuilt", status="published"), but Body is read from
// this content item's MDX asset instead (`assets` row, kind="mdx", whose
// `path` M2-401's blog.draft/merge presumably write the local Mayankbuilt
// checkout file to) — the closest fit in the current schema for "the live
// post's body". If M2-401 ships storing the body some other way (e.g. a
// dedicated column added in a later migration), update this loader; the
// rest of this file only depends on the SourcePost struct, not on this
// particular loading strategy, so callers can also build one directly
// (tests do exactly this against a mocked Completer).
func LoadSourcePost(ctx context.Context, db *sql.DB, contentID string) (SourcePost, error) {
	if db == nil {
		return SourcePost{}, fmt.Errorf("blog: nil db")
	}
	if strings.TrimSpace(contentID) == "" {
		return SourcePost{}, fmt.Errorf("blog: content_id required")
	}
	var url string
	err := db.QueryRowContext(ctx, `
SELECT url FROM publications
WHERE content_id=? AND platform=? AND status='published' AND url IS NOT NULL AND url != ''
ORDER BY published_at DESC LIMIT 1`, contentID, PlatformMayankbuilt).Scan(&url)
	if errors.Is(err, sql.ErrNoRows) {
		return SourcePost{}, fmt.Errorf("blog: no live mayankbuilt publication for content %s", contentID)
	}
	if err != nil {
		return SourcePost{}, fmt.Errorf("blog: load publication for %s: %w", contentID, err)
	}

	var mdxPath string
	err = db.QueryRowContext(ctx, `
SELECT path FROM assets WHERE content_id=? AND kind='mdx' ORDER BY rowid DESC LIMIT 1`, contentID).Scan(&mdxPath)
	if errors.Is(err, sql.ErrNoRows) {
		return SourcePost{}, fmt.Errorf("blog: no mdx asset for content %s", contentID)
	}
	if err != nil {
		return SourcePost{}, fmt.Errorf("blog: load mdx asset for %s: %w", contentID, err)
	}
	raw, err := os.ReadFile(mdxPath)
	if err != nil {
		return SourcePost{}, fmt.Errorf("blog: read mdx %s: %w", mdxPath, err)
	}
	return SourcePost{ContentID: contentID, URL: url, Body: stripMDXFrontmatter(string(raw))}, nil
}

// Options configures Repurposer. Zero values mean defaults.
type Options struct {
	Completer Completer // required
	Approver  Approver  // required
	Logger    *slog.Logger
}

// Repurposer produces and gates the LinkedIn repost.
type Repurposer struct {
	opts Options
	log  *slog.Logger
}

// NewRepurposer returns a Repurposer.
func NewRepurposer(opts Options) (*Repurposer, error) {
	if opts.Completer == nil {
		return nil, fmt.Errorf("blog: Completer is required")
	}
	if opts.Approver == nil {
		return nil, fmt.Errorf("blog: Approver is required")
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Repurposer{opts: opts, log: log}, nil
}

// RunInput is one blog.repurpose (LinkedIn leg) invocation.
type RunInput struct {
	Source SourcePost
	// RedoNotes, when set, means this is a re-run after a human Reject/Redo
	// decision on a previous draft's approval (same pattern as script.write's
	// redo notes in internal/content/script.go) — appended to the prompt as
	// a must-apply instruction.
	RedoNotes string
}

// Run produces a LinkedIn-native draft and opens the approval gate for it.
// Never calls the LinkedIn API — that only happens after approval, from
// internal/publish/linkedin.go.
func (r *Repurposer) Run(ctx context.Context, in RunInput) (*Draft, error) {
	if r == nil {
		return nil, fmt.Errorf("blog: nil repurposer")
	}
	contentID := strings.TrimSpace(in.Source.ContentID)
	if contentID == "" {
		return nil, fmt.Errorf("blog: source content_id required")
	}
	sourceURL := strings.TrimSpace(in.Source.URL)
	if sourceURL == "" {
		return nil, fmt.Errorf("blog: source url required")
	}
	sourceBody := strings.TrimSpace(in.Source.Body)
	if sourceBody == "" {
		return nil, fmt.Errorf("blog: source body required")
	}

	text, err := r.complete(ctx, in, sourceBody, sourceURL)
	if err != nil {
		return nil, err
	}

	summary := "LinkedIn draft (auto-generated, pending approval):\n\n" + text + "\n\nSource: " + sourceURL
	approvalID, err := r.opts.Approver.Start(ctx, contentID, approvalKindLinkedIn, summary, "")
	if err != nil {
		return nil, fmt.Errorf("blog: start approval for %s: %w", contentID, err)
	}
	r.log.Info("blog: linkedin draft pending approval", "content_id", contentID, "approval_id", approvalID)
	return &Draft{Text: text, ApprovalID: approvalID}, nil
}

func (r *Repurposer) complete(ctx context.Context, in RunInput, sourceBody, sourceURL string) (string, error) {
	system := strings.TrimSpace(linkedInSystemPrompt)
	user := buildLinkedInUserPrompt(sourceBody, sourceURL, in.RedoNotes)

	var lastErr error
	for attempt := 0; attempt < maxCompleteAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		msgs := []llm.Message{{Role: "user", Content: user}}
		if attempt > 0 && lastErr != nil {
			msgs = append(msgs, llm.Message{
				Role:    "user",
				Content: "That draft did not pass the originality check: " + lastErr.Error() + ". Write a genuinely different draft — a new hook, your own words, not a trimmed version of the article. Reply with the LinkedIn post text only.",
			})
		}
		resp, err := r.opts.Completer.Complete(ctx, llm.TaskBlog, llm.Request{
			System:    system,
			Messages:  msgs,
			MaxTokens: 1024,
		})
		if err != nil {
			return "", fmt.Errorf("blog: llm: %w", err)
		}
		text := normalizeDraft(resp.Text, sourceURL)
		if err := validateDraft(text, sourceBody, sourceURL); err != nil {
			lastErr = err
			r.log.Info("blog: linkedin draft retry", "attempt", attempt+1, "err", err.Error())
			continue
		}
		return text, nil
	}
	return "", queue.Permanent(fmt.Errorf("%w: %v", ErrDraftNotOriginal, lastErr))
}

const linkedInSystemPrompt = `You are Mayank's ghostwriter for his personal LinkedIn account. You are
repurposing his own tech blog post — already live on his portfolio — into a
short, native LinkedIn post. Rules:
- Open with your own hook: a specific claim, number, or question. Never
  reuse the article's opening line or its title as your first line.
- Write substantially shorter than the source (a native LinkedIn post, not
  a teaser dump of the article) — a few short paragraphs, not the article.
- Plain, first-person, specific — no generic "In today's fast-paced world"
  filler, no hashtag stuffing.
- End with one line inviting the reader to the full post, followed by the
  exact source URL you are given, on its own.
- Reply with the LinkedIn post text only — no markdown headers, no code
  fences, no commentary about what you wrote.`

func buildLinkedInUserPrompt(sourceBody, sourceURL, redoNotes string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Source URL: %s\n\n", sourceURL)
	b.WriteString("Source post body:\n")
	b.WriteString(sourceBody)
	if notes := strings.TrimSpace(redoNotes); notes != "" {
		fmt.Fprintf(&b, "\n\nREDO NOTES from approval (must apply):\n%s\n", notes)
	}
	b.WriteString("\n\nWrite the LinkedIn post now.")
	return b.String()
}

// normalizeDraft strips code fences / surrounding quotes the model may add,
// trims to the soft char limit, and guarantees the source link is present
// regardless of whether the model included it — the link back to the
// canonical post is a hard requirement (AC), not left to model compliance.
func normalizeDraft(raw, sourceURL string) string {
	text := strings.TrimSpace(raw)
	if strings.HasPrefix(text, "```") {
		text = strings.TrimPrefix(text, "```")
		if i := strings.Index(text, "\n"); i >= 0 {
			text = text[i+1:]
		}
		text = strings.TrimSuffix(strings.TrimSpace(text), "```")
		text = strings.TrimSpace(text)
	}
	if !strings.Contains(text, sourceURL) {
		text = strings.TrimSpace(text) + "\n\n" + sourceURL
	}
	if len(text) > linkedInSoftCharLimit {
		// Trim on a rune boundary, keep the trailing source URL intact.
		keep := linkedInSoftCharLimit - len(sourceURL) - 5
		if keep < 0 {
			keep = 0
		}
		body := []rune(strings.TrimSuffix(text, sourceURL))
		if len(body) > keep {
			body = body[:keep]
		}
		text = strings.TrimSpace(string(body)) + "…\n\n" + sourceURL
	}
	return text
}

// validateDraft is the "not a raw copy-paste" check this ticket's AC
// requires a test for: shorter than the source, a different opening line,
// and no long verbatim run shared with the source.
func validateDraft(draft, sourceBody, sourceURL string) error {
	if strings.TrimSpace(draft) == "" {
		return fmt.Errorf("empty draft")
	}
	if !strings.Contains(draft, sourceURL) {
		return fmt.Errorf("draft missing link back to source")
	}
	dw := countWords(draft)
	sw := countWords(sourceBody)
	if sw > 0 && dw >= sw {
		return fmt.Errorf("draft (%d words) is not shorter than the source (%d words)", dw, sw)
	}
	if dw == 0 {
		return fmt.Errorf("draft has no words")
	}
	if normalizeLine(firstLine(draft)) == normalizeLine(firstLine(sourceBody)) {
		return fmt.Errorf("draft opening line matches the source verbatim")
	}
	if run := longestSharedRun(wordsOf(draft), wordsOf(sourceBody)); run > minSharedRun {
		return fmt.Errorf("draft shares a %d-word run with the source (max %d)", run, minSharedRun)
	}
	return nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\n."); i > 0 {
		return s[:i]
	}
	return s
}

func normalizeLine(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func countWords(s string) int {
	return len(strings.Fields(strings.TrimSpace(s)))
}

func wordsOf(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return unicode.IsSpace(r) || (unicode.IsPunct(r) && r != '\'')
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// longestSharedRun returns the longest run of consecutive words shared
// between a and b (a simple O(n*m) shingle scan — inputs here are single
// posts/paragraphs, not corpora, so this stays cheap).
func longestSharedRun(a, b []string) int {
	best := 0
	for i := range a {
		for j := range b {
			k := 0
			for i+k < len(a) && j+k < len(b) && a[i+k] == b[j+k] {
				k++
			}
			if k > best {
				best = k
			}
		}
	}
	return best
}

func stripMDXFrontmatter(raw string) string {
	s := strings.TrimPrefix(raw, string(rune(0xFEFF))) // strip UTF-8 BOM if present
	s = strings.TrimLeft(s, " \t\r\n")
	if !strings.HasPrefix(s, "---") {
		return strings.TrimSpace(raw)
	}
	rest := s[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return strings.TrimSpace(raw)
	}
	body := rest[end+4:]
	body = strings.TrimLeft(body, "\r\n")
	return strings.TrimSpace(body)
}
