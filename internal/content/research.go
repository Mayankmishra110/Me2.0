// Research stage (M2-203): sourced brief per topic for EN/HI scripts.
// Fetches pages (robots.txt + readable text) or a video transcript (research
// notes only — COMPLIANCE §2 / D17). Writes brief.json and source texts for G1.
package content

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"mayank2/internal/llm"
	"mayank2/internal/queue"
)

// JobResearchBrief is the queue job type (SPEC §5).
const JobResearchBrief = "research.brief"

// Defaults for Research. Override through ResearchOptions.
const (
	DefaultResearchMaxSources  = 5
	DefaultResearchHTTPTimeout = 30 * time.Second
	maxResearchBody            = 2 << 20 // 2 MiB
	researchUserAgent          = "Mayank2Research/1.0 (+https://mayank2.local; research bot)"
)

// ErrResearchNoSources means every candidate URL was blocked or empty.
var ErrResearchNoSources = errors.New("research: no usable sources")

// Completer is the LLM surface Research needs (satisfied by *llm.Router).
type Completer interface {
	Complete(ctx context.Context, task llm.Task, req llm.Request) (llm.Response, error)
}

// TranscriptFunc fetches a creator video transcript for research notes only.
// Must not download or store footage/audio (COMPLIANCE §2).
type TranscriptFunc func(ctx context.Context, videoURL string) (string, error)

// ResearchOptions configures Research. Zero values mean defaults.
type ResearchOptions struct {
	HTTPClient *http.Client
	// MaxSources caps how many pages/transcripts are fetched (N).
	MaxSources int
	// OutDir receives brief.json and sources/*.txt. Required for Run.
	OutDir string
	// Completer builds the brief from fetched texts. Required.
	Completer Completer
	// FetchTranscript handles video URLs. When nil, video URLs error.
	FetchTranscript TranscriptFunc
	Logger          *slog.Logger

	// DB, when set, lets the research.brief queue handler create a new
	// content_items row (kind="short", stage="researching") when a job
	// payload has no ContentID yet — see RegisterHandler. Optional: a
	// caller-supplied ContentID always skips this.
	DB ResearchDB
	// Enqueue, when set, lets the research.brief handler chain to
	// script.write on success (ARCHITECTURE §3.1: research.brief ->
	// script.write). Optional — nil just skips the chain.
	Enqueue ResearchEnqueuer
	NewID   func() string
}

// ResearchDB is the subset of *sql.DB the research.brief handler needs to
// create a content_items row when a job doesn't already carry a ContentID.
type ResearchDB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// ResearchEnqueuer enqueues the follow-up script.write job.
type ResearchEnqueuer interface {
	Enqueue(ctx context.Context, jobType string, payload any, opts ...queue.EnqueueOpt) (id string, err error)
}

// Research builds a sourced brief for one topic.
type Research struct {
	client *http.Client
	opts   ResearchOptions
	log    *slog.Logger
}

// NewResearch returns a Research stage with defaults applied.
func NewResearch(opts ResearchOptions) (*Research, error) {
	if opts.Completer == nil {
		return nil, fmt.Errorf("research: Completer is required")
	}
	if strings.TrimSpace(opts.OutDir) == "" {
		return nil, fmt.Errorf("research: OutDir is required")
	}
	if opts.MaxSources <= 0 {
		opts.MaxSources = DefaultResearchMaxSources
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: DefaultResearchHTTPTimeout}
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Research{client: client, opts: opts, log: log}, nil
}

// ResearchInput is one research.brief job.
type ResearchInput struct {
	Topic    string   // required topic title / angle seed
	URLs     []string // article / page URLs (fetched up to MaxSources)
	VideoURL string   // optional; transcript path instead of HTML (COMPLIANCE §2)
}

// Fact is one sourced claim in the brief.
type Fact struct {
	Claim   string   `json:"claim"`
	Sources []string `json:"sources"`
}

// KeyNumber is a numeric highlight tied to a source URL.
type KeyNumber struct {
	Label  string `json:"label"`
	Value  string `json:"value"`
	Source string `json:"source"`
}

// SourceRef records a fetched page or transcript stored for the originality gate.
type SourceRef struct {
	URL    string `json:"url"`
	Path   string `json:"path"` // relative to OutDir
	Kind   string `json:"kind"` // "page" | "transcript"
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Brief is brief.json (ARCHITECTURE §3.1 + ticket AC).
type Brief struct {
	Angle         string      `json:"angle"`
	Facts         []Fact      `json:"facts"`
	KeyNumbers    []KeyNumber `json:"key_numbers"`
	OpenQuestions []string    `json:"open_questions"`
	Sources       []SourceRef `json:"sources"`
	Topic         string      `json:"topic"`
	Provider      string      `json:"provider,omitempty"`
	Model         string      `json:"model,omitempty"`
}

// Run fetches sources, asks the LLM for a brief, writes brief.json + source texts.
func (r *Research) Run(ctx context.Context, in ResearchInput) (*Brief, error) {
	topic := strings.TrimSpace(in.Topic)
	if topic == "" {
		return nil, fmt.Errorf("research: topic is required")
	}
	if err := os.MkdirAll(filepath.Join(r.opts.OutDir, "sources"), 0o755); err != nil {
		return nil, fmt.Errorf("research: mkdir sources: %w", err)
	}

	var fetched []SourceRef
	var texts []string // parallel to fetched

	if v := strings.TrimSpace(in.VideoURL); v != "" {
		if !isVideoURL(v) {
			return nil, fmt.Errorf("research: not a video URL: %s", redactURL(v))
		}
		if r.opts.FetchTranscript == nil {
			return nil, fmt.Errorf("research: video URL requires FetchTranscript")
		}
		text, err := r.opts.FetchTranscript(ctx, v)
		if err != nil {
			return nil, fmt.Errorf("research: transcript %s: %w", redactURL(v), err)
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return nil, fmt.Errorf("research: empty transcript for %s", redactURL(v))
		}
		ref, err := r.storeSource(v, "transcript", text)
		if err != nil {
			return nil, err
		}
		fetched = append(fetched, ref)
		texts = append(texts, text)
	}

	for _, raw := range in.URLs {
		if len(fetched) >= r.opts.MaxSources {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		u := strings.TrimSpace(raw)
		if u == "" {
			continue
		}
		if isVideoURL(u) {
			r.log.Info("research: skipping video in URLs; use VideoURL", "url", redactURL(u))
			continue
		}
		allowed, err := r.robotsAllowed(ctx, u)
		if err != nil {
			r.log.Info("research: robots check failed, skipping", "url", redactURL(u), "err", err.Error())
			continue
		}
		if !allowed {
			r.log.Info("research: robots.txt disallows fetch", "url", redactURL(u))
			continue
		}
		body, err := r.getBody(ctx, u)
		if err != nil {
			r.log.Info("research: fetch failed, skipping", "url", redactURL(u), "err", err.Error())
			continue
		}
		text := extractReadableText(string(body))
		if strings.TrimSpace(text) == "" {
			r.log.Info("research: empty readable text, skipping", "url", redactURL(u))
			continue
		}
		ref, err := r.storeSource(u, "page", text)
		if err != nil {
			return nil, err
		}
		fetched = append(fetched, ref)
		texts = append(texts, text)
	}

	if len(fetched) == 0 {
		return nil, ErrResearchNoSources
	}

	brief, err := r.completeBrief(ctx, topic, fetched, texts)
	if err != nil {
		return nil, err
	}
	brief.Topic = topic
	brief.Sources = fetched

	if err := validateBrief(brief); err != nil {
		return nil, fmt.Errorf("research: brief validation: %w", err)
	}

	path := filepath.Join(r.opts.OutDir, "brief.json")
	data, err := json.MarshalIndent(brief, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("research: marshal brief: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return nil, fmt.Errorf("research: write brief.json: %w", err)
	}
	return brief, nil
}

func (r *Research) storeSource(rawURL, kind, text string) (SourceRef, error) {
	sum := sha256.Sum256([]byte(text))
	hexSum := hex.EncodeToString(sum[:])
	rel := filepath.ToSlash(filepath.Join("sources", hexSum[:16]+".txt"))
	abs := filepath.Join(r.opts.OutDir, filepath.FromSlash(rel))
	if err := os.WriteFile(abs, []byte(text), 0o644); err != nil {
		return SourceRef{}, fmt.Errorf("research: write source text: %w", err)
	}
	return SourceRef{
		URL:    rawURL,
		Path:   rel,
		Kind:   kind,
		Bytes:  len(text),
		SHA256: hexSum,
	}, nil
}

var briefSchema = json.RawMessage(`{
  "type": "object",
  "required": ["angle", "facts", "key_numbers", "open_questions"],
  "properties": {
    "angle": {"type": "string"},
    "facts": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["claim", "sources"],
        "properties": {
          "claim": {"type": "string"},
          "sources": {"type": "array", "items": {"type": "string"}}
        }
      }
    },
    "key_numbers": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["label", "value", "source"],
        "properties": {
          "label": {"type": "string"},
          "value": {"type": "string"},
          "source": {"type": "string"}
        }
      }
    },
    "open_questions": {"type": "array", "items": {"type": "string"}}
  }
}`)

const researchSystemPrompt = `You are a research assistant for faceless educational YouTube videos.
Given a topic and source texts, produce a JSON brief. Rules:
- Every fact.claim must be supported by the sources; each fact.sources must list ≥1 of the provided source URLs.
- key_numbers: important figures from the sources, each with its source URL.
- open_questions: gaps or things a script should not invent.
- angle: one sentence framing for an original script (do not copy source wording).
- Never invent URLs. Use only the URLs provided.
Reply with JSON only.`

func (r *Research) completeBrief(ctx context.Context, topic string, refs []SourceRef, texts []string) (*Brief, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "Topic: %s\n\n", topic)
	b.WriteString("Sources:\n")
	for i, ref := range refs {
		fmt.Fprintf(&b, "--- source %d url=%s kind=%s ---\n%s\n\n", i+1, ref.URL, ref.Kind, truncateRunes(texts[i], 12_000))
	}
	b.WriteString("Produce the brief JSON now.")

	resp, err := r.opts.Completer.Complete(ctx, llm.TaskResearch, llm.Request{
		System:     researchSystemPrompt,
		Messages:   []llm.Message{{Role: "user", Content: b.String()}},
		MaxTokens:  2048,
		JSONSchema: briefSchema,
	})
	if err != nil {
		return nil, fmt.Errorf("research: llm: %w", err)
	}
	text := strings.TrimSpace(resp.Text)
	if strings.HasPrefix(text, "```") {
		text = stripMarkdownFence(text)
	}
	var out Brief
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return nil, fmt.Errorf("research: parse brief json: %w", err)
	}
	out.Provider = resp.Provider
	out.Model = resp.Model
	return &out, nil
}

func validateBrief(b *Brief) error {
	if strings.TrimSpace(b.Angle) == "" {
		return fmt.Errorf("angle is empty")
	}
	if len(b.Facts) == 0 {
		return fmt.Errorf("facts is empty")
	}
	for i, f := range b.Facts {
		if strings.TrimSpace(f.Claim) == "" {
			return fmt.Errorf("facts[%d].claim empty", i)
		}
		if len(f.Sources) == 0 {
			return fmt.Errorf("facts[%d] missing source URL", i)
		}
		for j, s := range f.Sources {
			if strings.TrimSpace(s) == "" {
				return fmt.Errorf("facts[%d].sources[%d] empty", i, j)
			}
		}
	}
	return nil
}

func (r *Research) getBody(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", stripURLError(err))
	}
	req.Header.Set("User-Agent", researchUserAgent)
	res, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", redactURL(rawURL), stripURLError(err))
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxResearchBody))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", redactURL(rawURL), err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s: status %d", redactURL(rawURL), res.StatusCode)
	}
	return body, nil
}

// robotsAllowed fetches host/robots.txt and checks the path for User-agent: *.
// Missing or unreadable robots.txt → allow (common crawl convention).
func (r *Research) robotsAllowed(ctx context.Context, rawURL string) (bool, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false, err
	}
	if u.Scheme == "" || u.Host == "" {
		return false, fmt.Errorf("invalid URL")
	}
	robotsURL := u.Scheme + "://" + u.Host + "/robots.txt"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, robotsURL, nil)
	if err != nil {
		return false, stripURLError(err)
	}
	req.Header.Set("User-Agent", researchUserAgent)
	res, err := r.client.Do(req)
	if err != nil {
		// Unreachable robots → allow; document in logs via caller.
		return true, nil
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return true, nil
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return true, nil
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return true, nil
	}
	return robotsPathAllowed(string(body), u.EscapedPath()), nil
}

// robotsPathAllowed is a minimal robots.txt parser for User-agent: * rules.
func robotsPathAllowed(robotsBody, path string) bool {
	if path == "" {
		path = "/"
	}
	lines := strings.Split(robotsBody, "\n")
	inStar := false
	var disallows []string
	var allows []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "user-agent:"):
			ua := strings.TrimSpace(line[len("user-agent:"):])
			inStar = ua == "*"
		case inStar && strings.HasPrefix(lower, "disallow:"):
			disallows = append(disallows, strings.TrimSpace(line[len("disallow:"):]))
		case inStar && strings.HasPrefix(lower, "allow:"):
			allows = append(allows, strings.TrimSpace(line[len("allow:"):]))
		}
	}
	// Longest Allow/Disallow match wins (simplified RFC9309).
	bestAllow, bestDisallow := -1, -1
	for _, a := range allows {
		if a != "" && strings.HasPrefix(path, a) && len(a) > bestAllow {
			bestAllow = len(a)
		}
	}
	for _, d := range disallows {
		if d == "" {
			// Disallow: with empty path means allow all.
			continue
		}
		if strings.HasPrefix(path, d) && len(d) > bestDisallow {
			bestDisallow = len(d)
		}
	}
	if bestDisallow < 0 {
		return true
	}
	if bestAllow > bestDisallow {
		return true
	}
	return false
}

var (
	reScript   = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	reStyle    = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	reNoscript = regexp.MustCompile(`(?is)<noscript[^>]*>.*?</noscript>`)
	reTags     = regexp.MustCompile(`(?s)<[^>]+>`)
	reWS       = regexp.MustCompile(`\s+`)
)

// extractReadableText strips scripts/styles/tags and collapses whitespace.
func extractReadableText(html string) string {
	s := reScript.ReplaceAllString(html, " ")
	s = reStyle.ReplaceAllString(s, " ")
	s = reNoscript.ReplaceAllString(s, " ")
	s = reTags.ReplaceAllString(s, " ")
	s = htmlUnescape(s)
	s = reWS.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func htmlUnescape(s string) string {
	repl := strings.NewReplacer(
		"&nbsp;", " ",
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", `"`,
		"&#39;", "'",
		"&apos;", "'",
	)
	return repl.Replace(s)
}

func isVideoURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Host)
	host = strings.TrimPrefix(host, "www.")
	switch host {
	case "youtube.com", "m.youtube.com", "youtu.be", "youtube-nocookie.com":
		return true
	case "vimeo.com":
		return true
	}
	return false
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	n := 0
	for i := range s {
		if n == max {
			return s[:i] + "…"
		}
		n++
	}
	return s
}

func stripMarkdownFence(s string) string {
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

// --- research.brief queue handler (M2-117) ----------------------------------

// ResearchJobPayload is the research.brief job body. ChannelID + Language
// are required so the handler can create a content_items row (when
// ContentID is empty) and build the script.write payload it chains to.
type ResearchJobPayload struct {
	ContentID string   `json:"content_id,omitempty"` // reuse an existing row (e.g. redo); empty -> create one
	ChannelID string   `json:"channel_id"`
	Language  string   `json:"language"` // "en" | "hi"
	Topic     string   `json:"topic"`
	URLs      []string `json:"urls,omitempty"`
	VideoURL  string   `json:"video_url,omitempty"`
	// Allowed is forwarded to script.write's format picker. Empty -> the
	// handler falls back to every format in the catalog (formats.Catalog).
	Allowed []string `json:"allowed,omitempty"`
}

// RegisterHandler registers research.brief on the net resource class (LLM +
// HTTP source fetches — not a local model or render, so CLAUDE.md's "heavy"
// rule does not apply).
func (r *Research) RegisterHandler(q *queue.Queue) {
	if q == nil || r == nil {
		return
	}
	q.Register(JobResearchBrief, queue.ResourceNet, 3, r.handleJob)
}

func (r *Research) newID() string {
	if r.opts.NewID != nil {
		return r.opts.NewID()
	}
	return newRenderULID(time.Now)
}

func (r *Research) handleJob(ctx context.Context, job queue.Job) (json.RawMessage, error) {
	var p ResearchJobPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return nil, queue.Permanent(fmt.Errorf("content research.brief payload: %w", err))
	}
	contentID := firstNonEmpty(p.ContentID, deref(job.ContentID))
	channelID := strings.TrimSpace(p.ChannelID)
	if channelID == "" {
		return nil, queue.Permanent(fmt.Errorf("content research.brief: channel_id required"))
	}
	lang := strings.TrimSpace(p.Language)
	if lang == "" {
		return nil, queue.Permanent(fmt.Errorf("content research.brief: language required"))
	}
	if strings.TrimSpace(p.Topic) == "" {
		return nil, queue.Permanent(fmt.Errorf("content research.brief: topic required"))
	}
	if contentID != "" && (contentID != filepath.Base(contentID) || strings.Contains(contentID, "..")) {
		return nil, queue.Permanent(fmt.Errorf("content research.brief: unsafe content_id %q", contentID))
	}

	if contentID == "" {
		if r.opts.DB == nil {
			return nil, queue.Permanent(fmt.Errorf("content research.brief: content_id required (no DB configured to create one)"))
		}
		id := r.newID()
		if _, err := r.opts.DB.ExecContext(ctx, `
INSERT INTO content_items (id, channel_id, kind, format, language, stage, created_at)
VALUES (?, ?, 'short', '', ?, 'researching', ?)`,
			id, channelID, lang, time.Now().UTC().Format(time.RFC3339Nano),
		); err != nil {
			return nil, fmt.Errorf("content research.brief: create content_items %s: %w", id, err)
		}
		contentID = id
	}

	// Run against a per-content_id subdirectory of the shared OutDir, not
	// OutDir itself: r.opts.OutDir is one fixed path set at construction
	// (single Options.OutDir field), and concurrent research.brief jobs
	// (net resource class runs 2 workers, config.example.yaml) would
	// otherwise overwrite each other's brief.json/sources/.
	perJob := &Research{client: r.client, opts: r.opts, log: r.log}
	perJob.opts.OutDir = filepath.Join(r.opts.OutDir, contentID)
	brief, err := perJob.Run(ctx, ResearchInput{Topic: p.Topic, URLs: p.URLs, VideoURL: p.VideoURL})
	if err != nil {
		return nil, fmt.Errorf("content research.brief: %w", err)
	}

	if r.opts.Enqueue != nil {
		allowed := p.Allowed
		if len(allowed) == 0 {
			allowed = allCatalogFormatIDs()
		}
		_, err := r.opts.Enqueue.Enqueue(ctx, JobScriptWrite, map[string]any{
			"content_id": contentID,
			"channel_id": channelID,
			"language":   lang,
			"topic":      p.Topic,
			"brief":      brief,
			"allowed":    allowed,
		}, queue.ContentID(contentID))
		if err != nil {
			return nil, fmt.Errorf("content research.brief: enqueue script.write: %w", err)
		}
	}

	out := map[string]any{"content_id": contentID, "brief": brief}
	return json.Marshal(out)
}

// NormalizeForShingle is exported for future G1 use: lowercases and collapses space.
func NormalizeForShingle(s string) string {
	var b strings.Builder
	prevSpace := true
	for _, r := range strings.ToLower(s) {
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return strings.TrimSpace(b.String())
}
