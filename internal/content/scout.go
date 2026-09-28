// Niche Scout stage (M2-202): daily scored topic suggestions per channel.
// Signals come from the YouTube Data API (quota-aware), configured RSS feeds,
// and an optional Trends endpoint when access is granted (D24: skip clean).
package content

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Env key that enables the YouTube Data API signal source (D24).
const YouTubeAPIKeyEnv = "YOUTUBE_API_KEY"

// Defaults for Scout. Override through ScoutOptions.
const (
	DefaultScoutLimit           = 10
	DefaultScoutManualBoost     = 1.5
	DefaultScoutExplorationRate = 0.20
	DefaultScoutDedupWindow     = 14 * 24 * time.Hour
	DefaultScoutYouTubeQuota    = 1000 // units per Run; search.list=100, videos.list=1
	DefaultScoutSearchTimeout   = 30 * time.Second
	DefaultScoutHTTPTimeout     = 30 * time.Second
	DefaultYouTubeBaseURL       = "https://www.googleapis.com/youtube/v3"
	maxScoutBody                = 4 << 20
	ytSearchCost                = 100
	ytVideosCost                = 1
	analyticsBlendWeight        = 0.30
	week4Days                   = 28
)

// ErrScoutNotConfigured is matched (errors.Is) by *ScoutNotConfiguredError.
var ErrScoutNotConfigured = errors.New("niche scout not configured")

// ScoutNotConfiguredError is returned when no signal source is available.
type ScoutNotConfiguredError struct {
	// MissingEnv lists env key names that would enable YouTube. Names only.
	MissingEnv []string
}

func (e *ScoutNotConfiguredError) Error() string {
	return fmt.Sprintf("niche scout not configured: set %s or pass RSSFeeds in ScoutOptions", strings.Join(e.MissingEnv, ", "))
}

// Is lets errors.Is(err, ErrScoutNotConfigured) match.
func (e *ScoutNotConfiguredError) Is(target error) bool { return target == ErrScoutNotConfigured }

// Topic source / status values match the topics table CHECK constraints
// (ARCHITECTURE §4).
const (
	TopicSourceScout  = "scout"
	TopicSourceManual = "manual"

	TopicStatusNew      = "new"
	TopicStatusPicked   = "picked"
	TopicStatusUsed     = "used"
	TopicStatusRejected = "rejected"
)

// TopicSignals is stored as JSON in topics.signals.
type TopicSignals struct {
	Demand      float64  `json:"demand"`
	Freshness   float64  `json:"freshness"`
	Fit         float64  `json:"fit"`
	Competition float64  `json:"competition"`
	Analytics   float64  `json:"analytics,omitempty"`
	BaseScore   float64  `json:"base_score"`
	Exploration bool     `json:"exploration,omitempty"`
	ManualBoost float64  `json:"manual_boost,omitempty"`
	Keyword     string   `json:"keyword,omitempty"`
	Sources     []string `json:"sources,omitempty"`
}

// Topic is a topics-table row (ARCHITECTURE §4).
type Topic struct {
	ID        string
	ChannelID string
	Title     string
	Source    string
	SourceURL string
	Score     float64
	Signals   TopicSignals
	Status    string
	CreatedAt time.Time
}

// TopicsDB is the subset of *sql.DB the scout needs.
type TopicsDB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ScoutOptions configures a Scout. Zero values mean defaults.
type ScoutOptions struct {
	HTTPClient *http.Client
	// YouTubeAPIKey enables the YouTube Data API. Empty disables YT signals.
	YouTubeAPIKey string
	// YouTubeBaseURL overrides the API host (tests).
	YouTubeBaseURL string
	// RSSFeeds are absolute feed URLs to poll. Empty disables RSS.
	RSSFeeds []string
	// TrendsBaseURL enables Trends when non-empty (D24). Empty skips Trends.
	// Expected GET ?q=<query> → {"interest":0-100}.
	TrendsBaseURL string
	// TrendsAPIKey is sent as Authorization: Bearer <key> when set.
	TrendsAPIKey string

	Limit           int
	ManualBoost     float64
	ExplorationRate float64
	DedupWindow     time.Duration
	YouTubeQuota    int      // max units spent per Run
	Keywords        []string // override niche keyword list

	Now    func() time.Time
	NewID  func() string
	Logger *slog.Logger
	// RandFloat returns [0,1). Injected for deterministic exploration tests.
	RandFloat func() float64
}

// Scout finds and scores topic suggestions for one channel at a time.
type Scout struct {
	db     TopicsDB
	opts   ScoutOptions
	client *http.Client
	log    *slog.Logger

	mu        sync.Mutex
	quotaUsed int
}

// NewScout builds a Scout. At least one of YouTubeAPIKey or RSSFeeds must be set.
func NewScout(db TopicsDB, opts ScoutOptions) (*Scout, error) {
	if db == nil {
		return nil, errors.New("scout: db is required")
	}
	opts = withScoutDefaults(opts)
	if opts.YouTubeAPIKey == "" && len(opts.RSSFeeds) == 0 {
		return nil, &ScoutNotConfiguredError{MissingEnv: []string{YouTubeAPIKeyEnv}}
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: DefaultScoutHTTPTimeout}
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Scout{db: db, opts: opts, client: client, log: log}, nil
}

// NewScoutFromEnv builds a Scout using YOUTUBE_API_KEY from getenv (and any
// RSSFeeds / Trends settings already on opts). Missing YouTube key is fine if
// RSSFeeds is non-empty.
func NewScoutFromEnv(db TopicsDB, opts ScoutOptions, getenv func(string) (string, bool)) (*Scout, error) {
	if getenv == nil {
		getenv = os.LookupEnv
	}
	if opts.YouTubeAPIKey == "" {
		if v, ok := getenv(YouTubeAPIKeyEnv); ok && strings.TrimSpace(v) != "" {
			opts.YouTubeAPIKey = strings.TrimSpace(v)
		}
	}
	return NewScout(db, opts)
}

func withScoutDefaults(o ScoutOptions) ScoutOptions {
	if o.Limit <= 0 {
		o.Limit = DefaultScoutLimit
	}
	if o.ManualBoost <= 0 {
		o.ManualBoost = DefaultScoutManualBoost
	}
	if o.ExplorationRate < 0 {
		o.ExplorationRate = 0
	}
	if o.ExplorationRate == 0 {
		o.ExplorationRate = DefaultScoutExplorationRate
	}
	if o.ExplorationRate > 1 {
		o.ExplorationRate = 1
	}
	if o.DedupWindow <= 0 {
		o.DedupWindow = DefaultScoutDedupWindow
	}
	if o.YouTubeQuota <= 0 {
		o.YouTubeQuota = DefaultScoutYouTubeQuota
	}
	if o.YouTubeBaseURL == "" {
		o.YouTubeBaseURL = DefaultYouTubeBaseURL
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.NewID == nil {
		o.NewID = newScoutULID
	}
	if o.RandFloat == nil {
		o.RandFloat = cryptoFloat
	}
	return o
}

// Run gathers signals for channel, scores candidates, dedupes against recent
// topics, and writes up to Limit new topics (source=scout). channel must
// already exist in the channels table (FK).
func (s *Scout) Run(ctx context.Context, channel Channel, warmupStartedAt *time.Time) ([]Topic, error) {
	s.mu.Lock()
	s.quotaUsed = 0
	s.mu.Unlock()

	now := s.opts.Now().UTC()
	keywords := s.opts.Keywords
	if len(keywords) == 0 {
		keywords = KeywordsForNiche(channel.Niche, channel.Language)
	}
	if len(keywords) == 0 {
		return nil, fmt.Errorf("scout: no keywords for niche %q", channel.Niche)
	}

	cands := map[string]*candidate{} // key = normalizeTitle
	add := func(c candidate) {
		key := normalizeTitle(c.title)
		if key == "" {
			return
		}
		if prev, ok := cands[key]; ok {
			mergeCandidate(prev, c)
			return
		}
		cp := c
		cands[key] = &cp
	}

	if s.opts.YouTubeAPIKey != "" {
		for _, kw := range keywords {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			yc, err := s.youtubeCandidates(ctx, kw, channel.Language)
			if err != nil {
				if errors.Is(err, errQuotaExhausted) {
					s.log.Info("scout youtube quota budget exhausted", "channel", channel.ID, "used", s.quotaUsed)
					break
				}
				return nil, fmt.Errorf("scout youtube keyword %q: %w", kw, err)
			}
			for _, c := range yc {
				add(c)
			}
		}
	}

	for _, feedURL := range s.opts.RSSFeeds {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rc, err := s.rssCandidates(ctx, feedURL, keywords)
		if err != nil {
			return nil, fmt.Errorf("scout rss %s: %w", feedURL, err)
		}
		for _, c := range rc {
			add(c)
		}
	}

	if s.opts.TrendsBaseURL != "" {
		for _, kw := range keywords {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			interest, err := s.trendsInterest(ctx, kw)
			if err != nil {
				return nil, fmt.Errorf("scout trends %q: %w", kw, err)
			}
			key := normalizeTitle(kw)
			if c, ok := cands[key]; ok {
				c.demand = clamp01(math.Max(c.demand, interest))
				c.sources = appendUnique(c.sources, "trends")
			} else {
				add(candidate{
					title:       kw,
					keyword:     kw,
					demand:      interest,
					freshness:   0.5,
					fit:         nicheFit(kw, keywords),
					competition: 0.4,
					sources:     []string{"trends"},
				})
			}
		}
	}

	analyticsOn := warmupStartedAt != nil && now.Sub(warmupStartedAt.UTC()) >= week4Days*24*time.Hour
	analyticsScores, err := s.loadTopicScores(ctx, channel.ID)
	if err != nil {
		return nil, err
	}

	var ranked []scoredRow
	for _, c := range cands {
		sig := TopicSignals{
			Demand:      clamp01(c.demand),
			Freshness:   clamp01(c.freshness),
			Fit:         clamp01(c.fit),
			Competition: clamp01(c.competition),
			Keyword:     c.keyword,
			Sources:     append([]string(nil), c.sources...),
		}
		base := sig.Demand * sig.Freshness * sig.Fit * (1 - sig.Competition)
		sig.BaseScore = base
		score := base
		if analyticsOn {
			if a, ok := analyticsScores[normalizeTitle(c.title)]; ok {
				sig.Analytics = a
				score = (1-analyticsBlendWeight)*base + analyticsBlendWeight*a
			} else if a, ok := analyticsScores[normalizeTitle(c.keyword)]; ok {
				sig.Analytics = a
				score = (1-analyticsBlendWeight)*base + analyticsBlendWeight*a
			}
		}
		ranked = append(ranked, scoredRow{cand: *c, score: score, sig: sig})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].cand.title < ranked[j].cand.title
		}
		return ranked[i].score > ranked[j].score
	})

	recent, err := s.recentTitles(ctx, channel.ID, now.Add(-s.opts.DedupWindow))
	if err != nil {
		return nil, err
	}

	var fresh []scoredRow
	for _, r := range ranked {
		if recent[normalizeTitle(r.cand.title)] {
			continue
		}
		fresh = append(fresh, r)
	}

	picked := pickWithExploration(fresh, s.opts.Limit, s.opts.ExplorationRate, s.opts.RandFloat)

	out := make([]Topic, 0, len(picked))
	for _, p := range picked {
		sig := p.sig
		sig.Exploration = p.exploration
		topic := Topic{
			ID:        s.opts.NewID(),
			ChannelID: channel.ID,
			Title:     p.cand.title,
			Source:    TopicSourceScout,
			SourceURL: p.cand.sourceURL,
			Score:     p.score,
			Signals:   sig,
			Status:    TopicStatusNew,
			CreatedAt: now,
		}
		if err := s.insertTopic(ctx, topic); err != nil {
			return out, fmt.Errorf("scout write topic %q: %w", topic.Title, err)
		}
		out = append(out, topic)
	}
	return out, nil
}

// AddManual inserts a Mayank-supplied topic with a priority score boost
// (Telegram /topic and dashboard POST /api/topics).
func (s *Scout) AddManual(ctx context.Context, channelID, title, sourceURL string) (Topic, error) {
	title = strings.TrimSpace(title)
	if channelID == "" {
		return Topic{}, errors.New("scout manual: channel_id is required")
	}
	if title == "" {
		return Topic{}, errors.New("scout manual: title is required")
	}
	now := s.opts.Now().UTC()
	if recent, err := s.recentTitles(ctx, channelID, now.Add(-s.opts.DedupWindow)); err != nil {
		return Topic{}, err
	} else if recent[normalizeTitle(title)] {
		return Topic{}, fmt.Errorf("scout manual: topic %q already suggested recently", title)
	}

	sig := TopicSignals{
		Demand:      0.8,
		Freshness:   1.0,
		Fit:         1.0,
		Competition: 0.2,
		ManualBoost: s.opts.ManualBoost,
		Sources:     []string{"manual"},
	}
	base := sig.Demand * sig.Freshness * sig.Fit * (1 - sig.Competition)
	sig.BaseScore = base
	topic := Topic{
		ID:        s.opts.NewID(),
		ChannelID: channelID,
		Title:     title,
		Source:    TopicSourceManual,
		SourceURL: strings.TrimSpace(sourceURL),
		Score:     base * s.opts.ManualBoost,
		Signals:   sig,
		Status:    TopicStatusNew,
		CreatedAt: now,
	}
	if err := s.insertTopic(ctx, topic); err != nil {
		return Topic{}, fmt.Errorf("scout manual write %q: %w", title, err)
	}
	return topic, nil
}

// QuotaUsed returns YouTube API units spent in the last Run.
func (s *Scout) QuotaUsed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.quotaUsed
}

// --- candidates & scoring ---------------------------------------------------

type candidate struct {
	title       string
	keyword     string
	sourceURL   string
	demand      float64
	freshness   float64
	fit         float64
	competition float64
	sources     []string
}

func mergeCandidate(dst *candidate, src candidate) {
	dst.demand = math.Max(dst.demand, src.demand)
	dst.freshness = math.Max(dst.freshness, src.freshness)
	dst.fit = math.Max(dst.fit, src.fit)
	// competition: keep the more competitive (higher) reading
	dst.competition = math.Max(dst.competition, src.competition)
	if dst.sourceURL == "" {
		dst.sourceURL = src.sourceURL
	}
	if dst.keyword == "" {
		dst.keyword = src.keyword
	}
	for _, s := range src.sources {
		dst.sources = appendUnique(dst.sources, s)
	}
}

type pickedCand struct {
	cand        candidate
	score       float64
	sig         TopicSignals
	exploration bool
}

// scoredRow is one scored candidate before exploration pick.
type scoredRow struct {
	cand  candidate
	score float64
	sig   TopicSignals
}

func pickWithExploration(ranked []scoredRow, limit int, rate float64, randFloat func() float64) []pickedCand {
	if limit <= 0 || len(ranked) == 0 {
		return nil
	}
	if len(ranked) > limit*4 {
		ranked = ranked[:min(len(ranked), limit*4)]
	}
	exploitN := int(math.Round(float64(limit) * (1 - rate)))
	if exploitN < 0 {
		exploitN = 0
	}
	if exploitN > limit {
		exploitN = limit
	}
	if exploitN > len(ranked) {
		exploitN = len(ranked)
	}
	out := make([]pickedCand, 0, limit)
	used := map[string]bool{}
	for i := 0; i < exploitN; i++ {
		r := ranked[i]
		out = append(out, pickedCand{cand: r.cand, score: r.score, sig: r.sig, exploration: false})
		used[normalizeTitle(r.cand.title)] = true
	}
	exploreN := limit - len(out)
	var pool []scoredRow
	for i := exploitN; i < len(ranked); i++ {
		if used[normalizeTitle(ranked[i].cand.title)] {
			continue
		}
		pool = append(pool, ranked[i])
	}
	for exploreN > 0 && len(pool) > 0 {
		idx := int(randFloat() * float64(len(pool)))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(pool) {
			idx = len(pool) - 1
		}
		r := pool[idx]
		out = append(out, pickedCand{cand: r.cand, score: r.score, sig: r.sig, exploration: true})
		used[normalizeTitle(r.cand.title)] = true
		pool = append(pool[:idx], pool[idx+1:]...)
		exploreN--
	}
	for len(out) < limit {
		added := false
		for _, r := range ranked {
			if used[normalizeTitle(r.cand.title)] {
				continue
			}
			out = append(out, pickedCand{cand: r.cand, score: r.score, sig: r.sig, exploration: false})
			used[normalizeTitle(r.cand.title)] = true
			added = true
			break
		}
		if !added {
			break
		}
	}
	return out
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func appendUnique(ss []string, s string) []string {
	for _, x := range ss {
		if x == s {
			return ss
		}
	}
	return append(ss, s)
}

var nonAlpha = regexp.MustCompile(`[^a-z0-9\s]+`)
var spaceRun = regexp.MustCompile(`\s+`)

func normalizeTitle(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonAlpha.ReplaceAllString(s, " ")
	s = spaceRun.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// KeywordsForNiche returns scout search keywords for a channel niche
// (CONTENT_STRATEGY §2). Hindi channels get the same topical keywords;
// native HI scripting is a later stage.
func KeywordsForNiche(niche string, lang Language) []string {
	switch strings.ToLower(strings.TrimSpace(niche)) {
	case "money_side_hustles", "money", "side_hustles":
		base := []string{
			"budgeting system for beginners",
			"credit score basics explained",
			"index funds compounding explained",
			"side hustle with real numbers",
			"online business case study",
			"money psychology habits",
			"financial scams to avoid",
			"taxes explained simply",
		}
		if lang == LanguageHI {
			return append([]string{
				"बजट कैसे बनाएं",
				"साइड हसल आइडिया",
			}, base...)
		}
		return base
	case "ai_tools_tech", "ai_tools", "tech":
		base := []string{
			"new AI tool breakdown",
			"how large language models work",
			"AI productivity workflow",
			"ChatGPT vs Claude comparison",
			"tech news explained 60 seconds",
			"beginner automation tutorial",
			"AI myths vs facts",
			"best AI tools for work",
		}
		if lang == LanguageHI {
			return append([]string{
				"AI टूल कैसे काम करता है",
				"ऑटोमेशन ट्यूटोरियल",
			}, base...)
		}
		return base
	default:
		if niche == "" {
			return nil
		}
		return []string{strings.ReplaceAll(niche, "_", " ")}
	}
}

func nicheFit(title string, keywords []string) float64 {
	n := normalizeTitle(title)
	if n == "" {
		return 0
	}
	best := 0.0
	for _, kw := range keywords {
		k := normalizeTitle(kw)
		if k == "" {
			continue
		}
		if n == k {
			return 1
		}
		if strings.Contains(n, k) || strings.Contains(k, n) {
			best = math.Max(best, 0.85)
			continue
		}
		// token overlap
		nt := strings.Fields(n)
		kt := strings.Fields(k)
		if len(nt) == 0 || len(kt) == 0 {
			continue
		}
		set := map[string]bool{}
		for _, t := range kt {
			set[t] = true
		}
		hit := 0
		for _, t := range nt {
			if set[t] {
				hit++
			}
		}
		overlap := float64(hit) / float64(max(len(nt), len(kt)))
		best = math.Max(best, 0.4+0.5*overlap)
	}
	if best == 0 {
		return 0.35 // weak default so unknown titles aren't zeroed out
	}
	return clamp01(best)
}

func scoreViewsDemand(views []uint64) float64 {
	if len(views) == 0 {
		return 0.2
	}
	var sum float64
	for _, v := range views {
		sum += math.Log10(float64(v) + 10)
	}
	avg := sum / float64(len(views))
	// log10(10)=1 … log10(1e7+10)≈7 → map roughly into 0..1
	return clamp01(avg / 7.0)
}

func scoreCompetition(views []uint64, published []time.Time, now time.Time) float64 {
	if len(views) == 0 {
		return 0.5
	}
	high := 0
	for i, v := range views {
		recent := true
		if i < len(published) && !published[i].IsZero() {
			recent = now.Sub(published[i]) < 90*24*time.Hour
		}
		if v >= 100_000 && recent {
			high++
		}
	}
	return clamp01(float64(high) / float64(len(views)))
}

func scoreFreshness(published []time.Time, now time.Time) float64 {
	if len(published) == 0 {
		return 0.4
	}
	var sum float64
	n := 0
	for _, p := range published {
		if p.IsZero() {
			continue
		}
		days := now.Sub(p).Hours() / 24
		switch {
		case days < 0:
			sum += 1
		case days <= 7:
			sum += 1
		case days <= 30:
			sum += 0.7
		case days <= 90:
			sum += 0.4
		default:
			sum += 0.15
		}
		n++
	}
	if n == 0 {
		return 0.4
	}
	return clamp01(sum / float64(n))
}

// --- YouTube Data API -------------------------------------------------------

var errQuotaExhausted = errors.New("youtube scout quota exhausted")

func (s *Scout) spendQuota(units int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.quotaUsed+units > s.opts.YouTubeQuota {
		return errQuotaExhausted
	}
	s.quotaUsed += units
	return nil
}

func (s *Scout) youtubeCandidates(ctx context.Context, keyword string, lang Language) ([]candidate, error) {
	if err := s.spendQuota(ytSearchCost); err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("part", "snippet")
	q.Set("type", "video")
	q.Set("maxResults", "8")
	q.Set("q", keyword)
	q.Set("order", "relevance")
	q.Set("key", s.opts.YouTubeAPIKey)
	switch lang {
	case LanguageHI:
		q.Set("relevanceLanguage", "hi")
		q.Set("regionCode", "IN")
	default:
		q.Set("relevanceLanguage", "en")
		q.Set("regionCode", "US")
	}

	searchURL := strings.TrimRight(s.opts.YouTubeBaseURL, "/") + "/search?" + q.Encode()
	body, err := s.getJSON(ctx, searchURL, nil)
	if err != nil {
		return nil, err
	}
	var search ytSearchResponse
	if err := json.Unmarshal(body, &search); err != nil {
		return nil, fmt.Errorf("decode search: %w", err)
	}
	ids := make([]string, 0, len(search.Items))
	for _, it := range search.Items {
		if it.ID.VideoID != "" {
			ids = append(ids, it.ID.VideoID)
		}
	}
	if len(ids) == 0 {
		// still emit the keyword as a weak candidate from search intent
		return []candidate{{
			title:       keyword,
			keyword:     keyword,
			demand:      0.25,
			freshness:   0.5,
			fit:         1,
			competition: 0.3,
			sources:     []string{"youtube"},
		}}, nil
	}

	if err := s.spendQuota(ytVideosCost); err != nil {
		return nil, err
	}
	vq := url.Values{}
	vq.Set("part", "statistics,snippet")
	vq.Set("id", strings.Join(ids, ","))
	vq.Set("key", s.opts.YouTubeAPIKey)
	videosURL := strings.TrimRight(s.opts.YouTubeBaseURL, "/") + "/videos?" + vq.Encode()
	vbody, err := s.getJSON(ctx, videosURL, nil)
	if err != nil {
		return nil, err
	}
	var videos ytVideosResponse
	if err := json.Unmarshal(vbody, &videos); err != nil {
		return nil, fmt.Errorf("decode videos: %w", err)
	}

	now := s.opts.Now().UTC()
	views := make([]uint64, 0, len(videos.Items))
	published := make([]time.Time, 0, len(videos.Items))
	for _, it := range videos.Items {
		views = append(views, parseUint(it.Statistics.ViewCount))
		if t, err := time.Parse(time.RFC3339, it.Snippet.PublishedAt); err == nil {
			published = append(published, t.UTC())
		} else {
			published = append(published, time.Time{})
		}
	}

	demand := scoreViewsDemand(views)
	comp := scoreCompetition(views, published, now)
	fresh := scoreFreshness(published, now)

	out := []candidate{{
		title:       keyword,
		keyword:     keyword,
		demand:      demand,
		freshness:   fresh,
		fit:         1,
		competition: comp,
		sources:     []string{"youtube"},
	}}
	// Also surface a couple of concrete video titles as exploratory seeds
	// (research uses them as notes only — D17; we never reuse footage).
	for i, it := range videos.Items {
		if i >= 2 {
			break
		}
		title := strings.TrimSpace(it.Snippet.Title)
		if title == "" {
			continue
		}
		out = append(out, candidate{
			title:       title,
			keyword:     keyword,
			sourceURL:   "https://www.youtube.com/watch?v=" + it.ID,
			demand:      demand * 0.9,
			freshness:   fresh,
			fit:         nicheFit(title, []string{keyword}),
			competition: comp,
			sources:     []string{"youtube"},
		})
	}
	return out, nil
}

type ytSearchResponse struct {
	Items []struct {
		ID struct {
			VideoID string `json:"videoId"`
		} `json:"id"`
	} `json:"items"`
}

type ytVideosResponse struct {
	Items []struct {
		ID      string `json:"id"`
		Snippet struct {
			Title       string `json:"title"`
			PublishedAt string `json:"publishedAt"`
		} `json:"snippet"`
		Statistics struct {
			ViewCount string `json:"viewCount"`
		} `json:"statistics"`
	} `json:"items"`
}

func parseUint(s string) uint64 {
	var n uint64
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + uint64(c-'0')
	}
	return n
}

// --- RSS --------------------------------------------------------------------

func (s *Scout) rssCandidates(ctx context.Context, feedURL string, keywords []string) ([]candidate, error) {
	body, err := s.getJSON(ctx, feedURL, nil) // getJSON is just GET+read; content-type free
	if err != nil {
		return nil, err
	}
	items, err := parseFeed(body)
	if err != nil {
		return nil, err
	}
	now := s.opts.Now().UTC()
	out := make([]candidate, 0, len(items))
	for _, it := range items {
		title := strings.TrimSpace(it.Title)
		if title == "" {
			continue
		}
		fresh := 0.5
		if !it.Published.IsZero() {
			fresh = scoreFreshness([]time.Time{it.Published}, now)
		}
		out = append(out, candidate{
			title:       title,
			keyword:     title,
			sourceURL:   it.Link,
			demand:      0.55,
			freshness:   fresh,
			fit:         nicheFit(title, keywords),
			competition: 0.35,
			sources:     []string{"rss"},
		})
	}
	return out, nil
}

type feedItem struct {
	Title     string
	Link      string
	Published time.Time
}

func parseFeed(body []byte) ([]feedItem, error) {
	// Try RSS 2.0 first, then Atom.
	var rss struct {
		Channel struct {
			Items []struct {
				Title     string `xml:"title"`
				Link      string `xml:"link"`
				PubDate   string `xml:"pubDate"`
				Published string `xml:"published"` // some feeds
				Date      string `xml:"date"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(body, &rss); err == nil && len(rss.Channel.Items) > 0 {
		out := make([]feedItem, 0, len(rss.Channel.Items))
		for _, it := range rss.Channel.Items {
			out = append(out, feedItem{
				Title:     it.Title,
				Link:      it.Link,
				Published: parseFeedTime(it.PubDate, it.Published, it.Date),
			})
		}
		return out, nil
	}
	var atom struct {
		Entries []struct {
			Title string `xml:"title"`
			Link  []struct {
				Href string `xml:"href,attr"`
				Rel  string `xml:"rel,attr"`
			} `xml:"link"`
			Published string `xml:"published"`
			Updated   string `xml:"updated"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(body, &atom); err != nil {
		return nil, fmt.Errorf("parse feed: %w", err)
	}
	out := make([]feedItem, 0, len(atom.Entries))
	for _, e := range atom.Entries {
		link := ""
		for _, l := range e.Link {
			if l.Rel == "" || l.Rel == "alternate" {
				link = l.Href
				break
			}
		}
		out = append(out, feedItem{
			Title:     e.Title,
			Link:      link,
			Published: parseFeedTime(e.Published, e.Updated),
		})
	}
	return out, nil
}

func parseFeedTime(vals ...string) time.Time {
	layouts := []string{
		time.RFC1123Z, time.RFC1123, time.RFC3339, time.RFC3339Nano,
		"Mon, 02 Jan 2006 15:04:05 MST",
		"2006-01-02T15:04:05Z",
		"2006-01-02",
	}
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		for _, layout := range layouts {
			if t, err := time.Parse(layout, v); err == nil {
				return t.UTC()
			}
		}
	}
	return time.Time{}
}

// --- Trends (optional) ------------------------------------------------------

func (s *Scout) trendsInterest(ctx context.Context, query string) (float64, error) {
	u, err := url.Parse(s.opts.TrendsBaseURL)
	if err != nil {
		return 0, fmt.Errorf("trends base url: %w", err)
	}
	q := u.Query()
	q.Set("q", query)
	u.RawQuery = q.Encode()
	headers := map[string]string{}
	if s.opts.TrendsAPIKey != "" {
		headers["Authorization"] = "Bearer " + s.opts.TrendsAPIKey
	}
	body, err := s.getJSON(ctx, u.String(), headers)
	if err != nil {
		return 0, err
	}
	var resp struct {
		Interest float64 `json:"interest"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, fmt.Errorf("decode trends: %w", err)
	}
	return clamp01(resp.Interest / 100.0), nil
}

// --- HTTP / DB --------------------------------------------------------------

func (s *Scout) getJSON(ctx context.Context, rawURL string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", stripURLError(err))
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := s.client.Do(req)
	if err != nil {
		// Do not %w-wrap the raw client.Do error: *url.Error embeds the full
		// request URL (including ?key=…) which would leak YouTube API keys.
		return nil, fmt.Errorf("GET %s: %w", redactURL(rawURL), stripURLError(err))
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxScoutBody))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", redactURL(rawURL), err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s: status %d", redactURL(rawURL), res.StatusCode)
	}
	return body, nil
}

// stripURLError drops the request URL from *url.Error so query-string API keys
// (YouTube Data API) never end up in errors or logs. Mirrors media.stripURLError.
func stripURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}

func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<url>"
	}
	q := u.Query()
	if q.Has("key") {
		q.Set("key", "REDACTED")
		u.RawQuery = q.Encode()
	}
	return u.String()
}

func (s *Scout) insertTopic(ctx context.Context, t Topic) error {
	sig, err := json.Marshal(t.Signals)
	if err != nil {
		return fmt.Errorf("marshal signals: %w", err)
	}
	var sourceURL any
	if t.SourceURL != "" {
		sourceURL = t.SourceURL
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO topics (id, channel_id, title, source, source_url, score, signals, status, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.ChannelID, t.Title, t.Source, sourceURL, t.Score, string(sig), t.Status,
		t.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return err
	}
	return nil
}

func (s *Scout) recentTitles(ctx context.Context, channelID string, since time.Time) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT title FROM topics
WHERE channel_id = ? AND created_at >= ?`, channelID, since.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("recent topics for %s: %w", channelID, err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			return nil, err
		}
		out[normalizeTitle(title)] = true
	}
	return out, rows.Err()
}

func (s *Scout) loadTopicScores(ctx context.Context, channelID string) (map[string]float64, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT key, score FROM scores
WHERE scope = 'topic' AND channel_id = ?`, channelID)
	if err != nil {
		return nil, fmt.Errorf("topic scores for %s: %w", channelID, err)
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var key string
		var score float64
		if err := rows.Scan(&key, &score); err != nil {
			return nil, err
		}
		out[normalizeTitle(key)] = clamp01(score)
	}
	return out, rows.Err()
}

// --- ids / rand / env -------------------------------------------------------

func cryptoFloat() float64 {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return 0.5
	}
	return float64(n.Int64()) / 1_000_000
}

// newScoutULID is a local Crockford ULID so scout does not import internal/queue.
func newScoutULID() string {
	const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	ms := time.Now().UTC().UnixMilli()
	var buf [16]byte
	buf[0] = byte(ms >> 40)
	buf[1] = byte(ms >> 32)
	buf[2] = byte(ms >> 24)
	buf[3] = byte(ms >> 16)
	buf[4] = byte(ms >> 8)
	buf[5] = byte(ms)
	_, _ = rand.Read(buf[6:])
	n := new(big.Int).SetBytes(buf[:])
	digits := make([]byte, 26)
	base := big.NewInt(32)
	mod := new(big.Int)
	for i := 25; i >= 0; i-- {
		n.DivMod(n, base, mod)
		digits[i] = crockford[mod.Int64()]
	}
	return string(digits)
}
