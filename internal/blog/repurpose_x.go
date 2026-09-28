// Package blog is Mayank's personal blog pipeline (M2-401..404): draft →
// Mayankbuilt → repurpose to LinkedIn / X personal / a Medium import link.
//
// This file (M2-403) is the X-personal leg: it turns the live, canonical
// Mayankbuilt post into an X thread via internal/llm (task "blog", Claude —
// CLAUDE.md "Claude is for Builder and blog only" / CONTEXT D13), then gates
// publishing behind the same approval.request flow as every other publish
// path in this system (D5, F7).
//
// Credential isolation (CONTEXT D12, COMPLIANCE §1 X row): this file never
// reads or schedules against the business X account. The distinct platform
// string is "x_personal" (see internal/publish/x_personal.go), separate from
// the business publisher's "x" (M2-303).
package blog

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"mayank2/internal/llm"
)

const (
	// PlatformXPersonal is the destination platform for this thread — must
	// stay distinct from M2-303's business "x" (COMPLIANCE §1, CONTEXT D12).
	PlatformXPersonal = "x_personal"

	// FormatXThread is the content_items.format value for an X-personal
	// repurpose item.
	FormatXThread = "x_thread"

	// tweetMaxChars is X's plain-text tweet character limit.
	tweetMaxChars = 280

	// maxThreadTweets bounds thread length so the repurpose step can't
	// produce something the ≤3 posts/day X-personal cap (COMPLIANCE §4)
	// would never let us actually publish in a day even as a single thread.
	maxThreadTweets = 12

	// DefaultBlogChannelID is the content_items.channel_id this repurposer
	// uses when XRepurposer.ChannelID is unset. content_items.channel_id is
	// NOT NULL REFERENCES channels(id) (ARCHITECTURE §4); a "blog" channels
	// row is assumed to be seeded elsewhere (config/channels or a migration
	// owned by M2-401) — this file does not create it. See ticket Notes.
	DefaultBlogChannelID = "blog"

	// JobBlogRepurposeXPersonal is this ticket's own job-type name.
	//
	// SPEC §5 lists a single shared "blog.repurpose" job type for all three
	// M2-402/403/404 repurpose legs, with no platform-dispatch mechanism
	// defined yet, and no shared dispatcher exists in this repo to route it
	// by platform. Registering a handler under the shared "blog.repurpose"
	// name from this ticket would collide with the sibling tickets doing the
	// same (queue.Register panics/overwrites on duplicate job types) since
	// M2-402/404 are built in parallel worktrees. Using a distinct,
	// platform-qualified job type avoids that collision; it is a deliberate,
	// documented deviation from SPEC §5's literal string, flagged for a
	// follow-up doc/dispatcher ticket once all three legs exist.
	JobBlogRepurposeXPersonal = "blog.repurpose_x_personal"
)

// Completer is the internal/llm surface this file needs (mirrors
// internal/content.Completer), kept narrow so tests can fake it without a
// real provider/router.
type Completer interface {
	Complete(ctx context.Context, task llm.Task, req llm.Request) (llm.Response, error)
}

// ApprovalStarter creates a pending approval and enqueues approval.request.
// *internal/content.ApprovalService satisfies this via its Start method.
type ApprovalStarter interface {
	Start(ctx context.Context, contentID, kind, summary, previewPath string) (string, error)
}

// SourcePost is the live, approved Mayankbuilt canonical post this thread
// repurposes (M2-401's output: a MDX post merged to main, with a
// publications row platform="mayankbuilt" recording its live URL).
type SourcePost struct {
	// ContentID is the canonical post's content_items.id (M2-401), kept only
	// for traceability in the repurposed item's script JSON — this ticket's
	// own content_items row gets a new id (see Start).
	ContentID string
	Title     string
	URL       string // live Mayankbuilt URL
	Body      string // MDX body (plain text is fine; the LLM only needs the substance)
}

func (s SourcePost) validate() error {
	if strings.TrimSpace(s.ContentID) == "" {
		return fmt.Errorf("blog: source content_id required")
	}
	if strings.TrimSpace(s.Title) == "" {
		return fmt.Errorf("blog: source title required")
	}
	if strings.TrimSpace(s.URL) == "" {
		return fmt.Errorf("blog: source url required")
	}
	if strings.TrimSpace(s.Body) == "" {
		return fmt.Errorf("blog: source body required")
	}
	return nil
}

// Thread is the repurposed X thread.
type Thread struct {
	Tweets []string `json:"tweets"`
}

// XRepurposer builds and stores the X-personal thread repurpose of a live
// blog post, gated behind approval.
type XRepurposer struct {
	DB       *sql.DB
	LLM      Completer
	Approval ApprovalStarter

	// ChannelID is the content_items.channel_id used for repurposed items.
	// Defaults to DefaultBlogChannelID.
	ChannelID string

	Now   func() time.Time
	NewID func() string
	Log   *slog.Logger
}

func (r *XRepurposer) now() time.Time {
	if r != nil && r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

func (r *XRepurposer) newID() string {
	if r != nil && r.NewID != nil {
		return r.NewID()
	}
	return newBlogULID(r.now)
}

func (r *XRepurposer) log() *slog.Logger {
	if r != nil && r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

func (r *XRepurposer) channelID() string {
	if r != nil && strings.TrimSpace(r.ChannelID) != "" {
		return r.ChannelID
	}
	return DefaultBlogChannelID
}

// Start repurposes src into an X thread, stores it as a new content_items
// row, and creates the approval.request. Returns the new content_id and the
// approval id.
func (r *XRepurposer) Start(ctx context.Context, src SourcePost) (contentID, approvalID string, err error) {
	return r.run(ctx, src, "")
}

// Redo re-runs the repurpose step for an existing content item with a
// human's redo note appended to the prompt, storing a fresh thread on the
// same content_items row and opening a new approval for it.
//
// This deliberately does not go through internal/content.ApprovalService's
// Decide/redo branch: that branch unconditionally enqueues "script.write"
// (internal/content/approval.go), which is a video-script job type and does
// not fit a blog repurpose. Wiring "who calls Redo with the human's note"
// (Telegram/dashboard decision routing for kind="blog" items) is an
// integration point outside this ticket's touches — flagged in ticket
// Notes/CONTEXT §5 as an open question, not guessed at here.
func (r *XRepurposer) Redo(ctx context.Context, contentID string, src SourcePost, note string) (approvalID string, err error) {
	if r == nil || r.DB == nil {
		return "", fmt.Errorf("blog: nil repurposer/db")
	}
	if r.Approval == nil {
		return "", fmt.Errorf("blog: nil approval starter")
	}
	if strings.TrimSpace(contentID) == "" {
		return "", fmt.Errorf("blog: redo requires contentID")
	}
	if strings.TrimSpace(note) == "" {
		return "", fmt.Errorf("blog: redo requires a note")
	}

	thread, err := r.repurpose(ctx, src, note)
	if err != nil {
		return "", err
	}
	if err := r.updateThread(ctx, contentID, src, thread, note); err != nil {
		return "", err
	}

	approvalID, err = r.Approval.Start(ctx, contentID, "blog", summarizeThread(thread), "")
	if err != nil {
		return "", fmt.Errorf("blog: approval start for redo %s: %w", contentID, err)
	}
	r.log().Info("blog: x_personal thread redone", "content_id", contentID, "approval_id", approvalID, "tweets", len(thread.Tweets))
	return approvalID, nil
}

func (r *XRepurposer) run(ctx context.Context, src SourcePost, note string) (contentID, approvalID string, err error) {
	if r == nil || r.DB == nil {
		return "", "", fmt.Errorf("blog: nil repurposer/db")
	}
	if r.LLM == nil {
		return "", "", fmt.Errorf("blog: nil llm completer")
	}
	if r.Approval == nil {
		return "", "", fmt.Errorf("blog: nil approval starter")
	}
	if err := src.validate(); err != nil {
		return "", "", err
	}

	thread, err := r.repurpose(ctx, src, note)
	if err != nil {
		return "", "", err
	}

	contentID = r.newID()
	if err := r.insertContentItem(ctx, contentID, src, thread, note); err != nil {
		return "", "", err
	}

	summary := summarizeThread(thread)
	approvalID, err = r.Approval.Start(ctx, contentID, "blog", summary, "")
	if err != nil {
		return "", "", fmt.Errorf("blog: approval start for %s: %w", contentID, err)
	}
	r.log().Info("blog: x_personal thread repurposed", "content_id", contentID, "approval_id", approvalID, "tweets", len(thread.Tweets))
	return contentID, approvalID, nil
}

// updateThread overwrites contentID's stored thread with an
// already-repurposed one (Redo calls repurpose exactly once, before this).
func (r *XRepurposer) updateThread(ctx context.Context, contentID string, src SourcePost, thread Thread, note string) error {
	script, err := json.Marshal(scriptDoc{
		Platform:      PlatformXPersonal,
		SourceURL:     src.URL,
		SourceTitle:   src.Title,
		SourceContent: src.ContentID,
		Tweets:        thread.Tweets,
		RedoNote:      note,
	})
	if err != nil {
		return fmt.Errorf("blog: marshal thread: %w", err)
	}
	_, err = r.DB.ExecContext(ctx, `UPDATE content_items SET script=?, stage='draft' WHERE id=?`, string(script), contentID)
	if err != nil {
		return fmt.Errorf("blog: update thread %s: %w", contentID, err)
	}
	return nil
}

// scriptDoc is the content_items.script JSON shape for an X-personal
// repurpose item.
type scriptDoc struct {
	Platform      string   `json:"platform"`
	SourceURL     string   `json:"source_url"`
	SourceTitle   string   `json:"source_title"`
	SourceContent string   `json:"source_content_id"`
	Tweets        []string `json:"tweets"`
	RedoNote      string   `json:"redo_note,omitempty"`
}

func (r *XRepurposer) insertContentItem(ctx context.Context, contentID string, src SourcePost, thread Thread, note string) error {
	script, err := json.Marshal(scriptDoc{
		Platform:      PlatformXPersonal,
		SourceURL:     src.URL,
		SourceTitle:   src.Title,
		SourceContent: src.ContentID,
		Tweets:        thread.Tweets,
		RedoNote:      note,
	})
	if err != nil {
		return fmt.Errorf("blog: marshal thread: %w", err)
	}
	_, err = r.DB.ExecContext(ctx, `
INSERT INTO content_items (id, channel_id, kind, format, language, stage, script, created_at)
VALUES (?, ?, 'blog', ?, 'en', 'draft', ?, ?)`,
		contentID, r.channelID(), FormatXThread, string(script), r.now().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("blog: insert content_item %s: %w", contentID, err)
	}
	return nil
}

// repurpose calls the LLM and validates/normalizes the thread. note, when
// set, is a human redo instruction appended to the prompt.
func (r *XRepurposer) repurpose(ctx context.Context, src SourcePost, note string) (Thread, error) {
	resp, err := r.LLM.Complete(ctx, llm.TaskBlog, llm.Request{
		System:     xThreadSystemPrompt,
		Messages:   []llm.Message{{Role: "user", Content: buildXThreadPrompt(src, note)}},
		MaxTokens:  2048,
		JSONSchema: xThreadSchema,
	})
	if err != nil {
		return Thread{}, fmt.Errorf("blog: llm complete: %w", err)
	}
	text := strings.TrimSpace(resp.Text)
	if err := llm.ValidateJSONSchema(xThreadSchema, text); err != nil {
		return Thread{}, fmt.Errorf("blog: llm response failed schema: %w", err)
	}
	var parsed Thread
	if err := json.Unmarshal([]byte(stripFence(text)), &parsed); err != nil {
		return Thread{}, fmt.Errorf("blog: parse llm response: %w", err)
	}
	return normalizeThread(parsed, src)
}

const xThreadSystemPrompt = `You are repurposing Mayank's personal tech blog post into a short X (Twitter) thread for his PERSONAL account.
Write your own hook tweet — never copy or lightly reword the blog post's opening line.
Each tweet must stand on its own under 280 characters. Use plain, direct language, no hashtag stuffing, no engagement bait.
The last tweet links back to the canonical post.
Reply with JSON only: {"tweets": ["tweet 1", "tweet 2", ...]}.`

var xThreadSchema = json.RawMessage(`{
  "type": "object",
  "required": ["tweets"],
  "properties": {
    "tweets": {
      "type": "array",
      "items": {"type": "string"}
    }
  }
}`)

func buildXThreadPrompt(src SourcePost, note string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Source title: %s\n", src.Title)
	fmt.Fprintf(&b, "Source URL: %s\n", src.URL)
	b.WriteString("Source body:\n")
	b.WriteString(src.Body)
	if n := strings.TrimSpace(note); n != "" {
		fmt.Fprintf(&b, "\n\nREDO NOTES from Mayank (must apply):\n%s\n", n)
	}
	b.WriteString("\n\nProduce the thread JSON now.")
	return b.String()
}

// normalizeThread enforces the per-tweet character limit and the
// not-a-copy-paste rule, and truncates the thread to maxThreadTweets.
func normalizeThread(t Thread, src SourcePost) (Thread, error) {
	if len(t.Tweets) == 0 {
		return Thread{}, fmt.Errorf("blog: empty thread")
	}
	if len(t.Tweets) > maxThreadTweets {
		t.Tweets = t.Tweets[:maxThreadTweets]
	}
	out := make([]string, 0, len(t.Tweets))
	for i, raw := range t.Tweets {
		tw := strings.TrimSpace(raw)
		if tw == "" {
			return Thread{}, fmt.Errorf("blog: tweet %d empty", i)
		}
		enforced, err := enforceTweetLength(tw)
		if err != nil {
			return Thread{}, fmt.Errorf("blog: tweet %d: %w", i, err)
		}
		out = append(out, enforced)
	}
	if looksCopyPasted(out[0], src.Body) {
		return Thread{}, fmt.Errorf("blog: hook tweet looks copy-pasted from the source body, not a genuine repurpose")
	}
	return Thread{Tweets: out}, nil
}

// enforceTweetLength truncates at the last word boundary at or under
// tweetMaxChars, never cutting mid-word, matching the discipline M2-303
// applies to captions. It rejects (returns an error) rather than emitting a
// near-empty or word-boundary-less tweet.
func enforceTweetLength(tw string) (string, error) {
	if utf8.RuneCountInString(tw) <= tweetMaxChars {
		return tw, nil
	}
	runes := []rune(tw)
	limit := tweetMaxChars - 1 // reserve 1 char for the "…" truncation marker
	if limit < 1 {
		return "", fmt.Errorf("tweet too long to truncate at a word boundary")
	}
	cut := limit
	for cut > 0 && runes[cut] != ' ' {
		cut--
	}
	if cut < tweetMaxChars/4 {
		// No reasonable word boundary near the limit — reject rather than
		// mangle the tweet mid-thought.
		return "", fmt.Errorf("over %d chars with no usable word boundary", tweetMaxChars)
	}
	truncated := strings.TrimRight(string(runes[:cut]), " ") + "…"
	return truncated, nil
}

// looksCopyPasted flags a hook that is essentially the blog's opening text
// verbatim (normalized for whitespace/case), which fails the "own hook, not
// copy-paste" requirement.
func looksCopyPasted(hook, body string) bool {
	h := normalizeForCompare(hook)
	b := normalizeForCompare(body)
	if h == "" || b == "" {
		return false
	}
	n := len(h)
	if n > 40 {
		n = 40
	}
	if len(b) < n {
		n = len(b)
	}
	return n >= 20 && h[:n] == b[:n]
}

func normalizeForCompare(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}

func stripFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func summarizeThread(t Thread) string {
	if len(t.Tweets) == 0 {
		return ""
	}
	hook := t.Tweets[0]
	if utf8.RuneCountInString(hook) > 120 {
		r := []rune(hook)
		hook = string(r[:120]) + "…"
	}
	return fmt.Sprintf("%d-tweet X thread (personal): %s", len(t.Tweets), hook)
}

// newBlogULID mirrors internal/content's unexported approval-id generator;
// there is no shared exported ID package to import instead.
func newBlogULID(now func() time.Time) string {
	const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	ms := uint64(now().UTC().UnixMilli())
	var buf [26]byte
	for i := 9; i >= 0; i-- {
		buf[i] = crockford[ms&31]
		ms >>= 5
	}
	var rnd [16]byte
	_, _ = rand.Read(rnd[:])
	for i := 10; i < 26; i++ {
		buf[i] = crockford[int(rnd[i-10])%32]
	}
	return string(buf[:])
}
