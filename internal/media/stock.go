// Package media wraps the media tools (stock footage, ffmpeg, media-tools, remotion).
//
// stock.go finds and downloads licensed B-roll per script beat from the
// official Pexels and Pixabay APIs (D15). Every returned asset carries its
// license record (COMPLIANCE §2, gate F1). Sources are enabled only by the
// API keys that exist (D24).
package media

import (
	"context"
	"crypto/sha256"
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
	"sync"
	"time"
)

// Env keys that enable each stock source (D24).
const (
	PexelsKeyEnv  = "PEXELS_API_KEY"
	PixabayKeyEnv = "PIXABAY_API_KEY"
)

// Defaults for Stock. All can be overridden through StockOptions.
const (
	DefaultStockReuseWindow     = 30 * 24 * time.Hour
	DefaultStockMaxBytes        = 200 << 20 // 200 MiB per clip
	DefaultStockDownloadTimeout = 5 * time.Minute
	DefaultStockSearchTimeout   = 30 * time.Second
	DefaultStockMaxWidth        = 1920
	maxStockSearchBody          = 4 << 20 // cap on a search response body
	maxStockCount               = 20
)

// ErrStockNotConfigured is matched (errors.Is) by *NotConfiguredError.
var ErrStockNotConfigured = errors.New("stock visuals not configured")

// ErrNoStockMatch means no usable, licensed, not-recently-used clip was found.
var ErrNoStockMatch = errors.New("no stock clip matched")

// NotConfiguredError is returned when no stock source has an API key.
type NotConfiguredError struct {
	// MissingEnv lists the env key names that would enable a source. Names only, never values.
	MissingEnv []string
}

func (e *NotConfiguredError) Error() string {
	return fmt.Sprintf("stock visuals not configured: set at least one of %s in .env", strings.Join(e.MissingEnv, ", "))
}

// Is lets errors.Is(err, ErrStockNotConfigured) match.
func (e *NotConfiguredError) Is(target error) bool { return target == ErrStockNotConfigured }

// Orientation of the wanted clip.
type Orientation string

const (
	OrientationAny       Orientation = ""
	OrientationLandscape Orientation = "landscape"
	OrientationPortrait  Orientation = "portrait"
	OrientationSquare    Orientation = "square"
)

// Valid reports whether o is a known orientation.
func (o Orientation) Valid() bool {
	switch o {
	case OrientationAny, OrientationLandscape, OrientationPortrait, OrientationSquare:
		return true
	}
	return false
}

// matches reports whether a w×h frame has orientation o. Square allows 10% slack.
func (o Orientation) matches(w, h int) bool {
	if w <= 0 || h <= 0 {
		return o == OrientationAny
	}
	switch o {
	case OrientationLandscape:
		return w > h && !isSquarish(w, h)
	case OrientationPortrait:
		return h > w && !isSquarish(w, h)
	case OrientationSquare:
		return isSquarish(w, h)
	default:
		return true
	}
}

func isSquarish(w, h int) bool {
	big, small := max(w, h), min(w, h)
	return float64(big) <= float64(small)*1.1
}

// StockQuery is what a provider searches for.
type StockQuery struct {
	Keywords    []string
	Orientation Orientation
	MinSeconds  int
	PerPage     int
	MaxWidth    int // preferred max width of the chosen rendition
}

// Text is the search string sent to the provider.
func (q StockQuery) Text() string {
	var parts []string
	for _, k := range q.Keywords {
		if k = strings.TrimSpace(k); k != "" {
			parts = append(parts, k)
		}
	}
	return strings.Join(parts, " ")
}

// LicenseRecord is the proof that a clip may be used (COMPLIANCE §2, F1).
type LicenseRecord struct {
	Provider        string    `json:"provider"`
	ProviderAssetID string    `json:"provider_asset_id"`
	SourcePageURL   string    `json:"source_page_url"`
	LicenseName     string    `json:"license_name"`
	LicenseURL      string    `json:"license_url"`
	Author          string    `json:"author,omitempty"`
	AuthorURL       string    `json:"author_url,omitempty"`
	RetrievedAt     time.Time `json:"retrieved_at"`
}

// Validate reports whether the record is complete enough to use the clip.
func (l LicenseRecord) Validate() error {
	switch {
	case l.Provider == "":
		return errors.New("license record: provider is empty")
	case l.ProviderAssetID == "":
		return errors.New("license record: provider asset id is empty")
	case !isHTTPURL(l.SourcePageURL):
		return fmt.Errorf("license record %s/%s: source page url %q is not an http(s) url", l.Provider, l.ProviderAssetID, l.SourcePageURL)
	case !isHTTPURL(l.LicenseURL):
		return fmt.Errorf("license record %s/%s: license url %q is not an http(s) url", l.Provider, l.ProviderAssetID, l.LicenseURL)
	}
	return nil
}

// StockCandidate is one search hit, before download.
type StockCandidate struct {
	License     LicenseRecord
	DownloadURL string
	Width       int
	Height      int
	Seconds     int
	SizeBytes   int64 // 0 when the provider does not say
}

// StockAsset is a downloaded, licensed clip in the job folder.
type StockAsset struct {
	License     LicenseRecord `json:"license"`
	Path        string        `json:"path"`         // absolute path in the job folder
	LicensePath string        `json:"license_path"` // sidecar JSON next to the clip
	Bytes       int64         `json:"bytes"`
	SHA256      string        `json:"sha256"`
	Width       int           `json:"width"`
	Height      int           `json:"height"`
	Seconds     int           `json:"seconds"`
}

// StockProvider searches one official stock API.
type StockProvider interface {
	Name() string
	Search(ctx context.Context, q StockQuery) ([]StockCandidate, error)
}

// StockOptions configures a Stock fetcher. Zero values use the defaults.
type StockOptions struct {
	HTTPClient      *http.Client // used for downloads; nil = safe default client
	Usage           UsageStore   // nil = in-memory store (tests / not persisted)
	ReuseWindow     time.Duration
	MaxBytes        int64
	DownloadTimeout time.Duration
	MaxWidth        int
	Now             func() time.Time
	Logger          *slog.Logger
}

// Stock finds and downloads licensed clips from the enabled providers.
type Stock struct {
	providers []StockProvider
	client    *http.Client
	usage     UsageStore
	window    time.Duration
	maxBytes  int64
	dlTimeout time.Duration
	maxWidth  int
	now       func() time.Time
	log       *slog.Logger

	mu sync.Mutex // serialises check-then-record of clip usage within this process
}

// NewStock builds a fetcher over the given providers. With no providers it
// returns a *NotConfiguredError.
func NewStock(providers []StockProvider, opts StockOptions) (*Stock, error) {
	var ps []StockProvider
	for _, p := range providers {
		if p != nil {
			ps = append(ps, p)
		}
	}
	if len(ps) == 0 {
		return nil, &NotConfiguredError{MissingEnv: []string{PexelsKeyEnv, PixabayKeyEnv}}
	}
	s := &Stock{
		providers: ps,
		client:    opts.HTTPClient,
		usage:     opts.Usage,
		window:    opts.ReuseWindow,
		maxBytes:  opts.MaxBytes,
		dlTimeout: opts.DownloadTimeout,
		maxWidth:  opts.MaxWidth,
		now:       opts.Now,
		log:       opts.Logger,
	}
	if s.client == nil {
		s.client = defaultStockClient()
	}
	if s.usage == nil {
		s.usage = NewMemoryUsageStore()
	}
	if s.window <= 0 {
		s.window = DefaultStockReuseWindow
	}
	if s.maxBytes <= 0 {
		s.maxBytes = DefaultStockMaxBytes
	}
	if s.dlTimeout <= 0 {
		s.dlTimeout = DefaultStockDownloadTimeout
	}
	if s.maxWidth <= 0 {
		s.maxWidth = DefaultStockMaxWidth
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	return s, nil
}

// NewStockFromEnv enables Pexels and/or Pixabay from their API keys (D24).
// lookup is usually os.LookupEnv (after config.LoadEnvFile); empty values count
// as missing. Neither key → *NotConfiguredError. Keys are never logged.
func NewStockFromEnv(lookup func(string) (string, bool), opts StockOptions) (*Stock, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	var ps []StockProvider
	var missing []string
	if key := envValue(lookup, PexelsKeyEnv); key != "" {
		ps = append(ps, NewPexels(key, PexelsOptions{}))
	} else {
		missing = append(missing, PexelsKeyEnv)
	}
	if key := envValue(lookup, PixabayKeyEnv); key != "" {
		ps = append(ps, NewPixabay(key, PixabayOptions{}))
	} else {
		missing = append(missing, PixabayKeyEnv)
	}
	if len(ps) == 0 {
		return nil, &NotConfiguredError{MissingEnv: missing}
	}
	return NewStock(ps, opts)
}

func envValue(lookup func(string) (string, bool), key string) string {
	v, ok := lookup(key)
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}

// Providers returns the names of the enabled providers, in search order.
func (s *Stock) Providers() []string {
	names := make([]string, len(s.providers))
	for i, p := range s.providers {
		names[i] = p.Name()
	}
	return names
}

// FetchRequest asks for clips for one script beat.
type FetchRequest struct {
	ChannelID   string
	JobDir      string // job working folder; clips are written here
	Keywords    []string
	Orientation Orientation
	Count       int // clips wanted; default 1
	MinSeconds  int // optional minimum clip length
}

func (r FetchRequest) validate() error {
	if strings.TrimSpace(r.ChannelID) == "" {
		return errors.New("channel id is required")
	}
	if strings.TrimSpace(r.JobDir) == "" {
		return errors.New("job dir is required")
	}
	if (StockQuery{Keywords: r.Keywords}).Text() == "" {
		return errors.New("at least one non-empty keyword is required")
	}
	if !r.Orientation.Valid() {
		return fmt.Errorf("orientation %q: want landscape|portrait|square or empty", r.Orientation)
	}
	if r.Count < 0 || r.Count > maxStockCount {
		return fmt.Errorf("count %d: want 1..%d", r.Count, maxStockCount)
	}
	if r.MinSeconds < 0 {
		return fmt.Errorf("min seconds %d: want >= 0", r.MinSeconds)
	}
	return nil
}

// Fetch searches the enabled providers in order and downloads up to Count
// licensed clips not used on req.ChannelID within the reuse window. It returns
// the clips it got; with none it returns an error wrapping ErrNoStockMatch (or
// the provider errors). Fewer than Count clips is not an error.
func (s *Stock) Fetch(ctx context.Context, req FetchRequest) ([]StockAsset, error) {
	if err := req.validate(); err != nil {
		return nil, fmt.Errorf("stock fetch: %w", err)
	}
	if req.Count == 0 {
		req.Count = 1
	}
	jobDir, err := filepath.Abs(req.JobDir)
	if err != nil {
		return nil, fmt.Errorf("stock fetch: job dir %q: %w", req.JobDir, err)
	}
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		return nil, fmt.Errorf("stock fetch: create job dir %q: %w", jobDir, err)
	}

	q := StockQuery{
		Keywords:    req.Keywords,
		Orientation: req.Orientation,
		MinSeconds:  req.MinSeconds,
		PerPage:     max(req.Count*5, 10),
		MaxWidth:    s.maxWidth,
	}
	var (
		assets  []StockAsset
		errs    []error
		inBatch = map[string]bool{}
	)
	for _, p := range s.providers {
		if len(assets) >= req.Count {
			break
		}
		cands, err := p.Search(ctx, q)
		if err != nil {
			if ctx.Err() != nil {
				return assets, fmt.Errorf("stock fetch %q: %w", q.Text(), ctx.Err())
			}
			s.log.Warn("stock search failed", "provider", p.Name(), "query", q.Text(), "err", err)
			errs = append(errs, err)
			continue
		}
		for _, c := range cands {
			if len(assets) >= req.Count {
				break
			}
			a, ok, err := s.take(ctx, req, jobDir, c, inBatch)
			if err != nil {
				if ctx.Err() != nil {
					return assets, fmt.Errorf("stock fetch %q: %w", q.Text(), ctx.Err())
				}
				s.log.Warn("stock clip skipped", "provider", c.License.Provider, "asset_id", c.License.ProviderAssetID, "err", err)
				errs = append(errs, err)
				continue
			}
			if ok {
				assets = append(assets, a)
			}
		}
	}
	if len(assets) == 0 {
		errs = append([]error{ErrNoStockMatch}, errs...)
		return nil, fmt.Errorf("stock fetch %q for channel %s: %w", q.Text(), req.ChannelID, errors.Join(errs...))
	}
	return assets, nil
}

// take filters, reserves, and downloads one candidate. ok=false means skipped
// without error (filtered out or recently used).
func (s *Stock) take(ctx context.Context, req FetchRequest, jobDir string, c StockCandidate, inBatch map[string]bool) (StockAsset, bool, error) {
	lic := c.License
	if err := lic.Validate(); err != nil {
		return StockAsset{}, false, err
	}
	if !req.Orientation.matches(c.Width, c.Height) {
		return StockAsset{}, false, nil
	}
	if req.MinSeconds > 0 && c.Seconds < req.MinSeconds {
		return StockAsset{}, false, nil
	}
	if c.SizeBytes > s.maxBytes {
		return StockAsset{}, false, nil
	}
	key := lic.Provider + "/" + lic.ProviderAssetID
	if inBatch[key] {
		return StockAsset{}, false, nil
	}

	// Check-download-record under one lock so two beats of the same channel
	// in this process cannot pick the same clip.
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now().UTC()
	last, used, err := s.usage.LastUsed(ctx, req.ChannelID, lic.Provider, lic.ProviderAssetID)
	if err != nil {
		return StockAsset{}, false, fmt.Errorf("usage lookup %s on channel %s: %w", key, req.ChannelID, err)
	}
	if used && now.Sub(last) < s.window {
		return StockAsset{}, false, nil
	}

	lic.RetrievedAt = now
	a, err := s.download(ctx, jobDir, c, lic)
	if err != nil {
		return StockAsset{}, false, err
	}
	if err := s.usage.RecordUse(ctx, req.ChannelID, lic.Provider, lic.ProviderAssetID, now); err != nil {
		removeQuiet(a.Path, a.LicensePath)
		return StockAsset{}, false, fmt.Errorf("record usage %s on channel %s: %w", key, req.ChannelID, err)
	}
	inBatch[key] = true
	return a, true, nil
}

var unsafeNameRe = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// download streams the clip into jobDir, bounded by size and timeout, then
// writes the license sidecar. On any failure nothing is left behind.
func (s *Stock) download(ctx context.Context, jobDir string, c StockCandidate, lic LicenseRecord) (StockAsset, error) {
	key := lic.Provider + "/" + lic.ProviderAssetID
	u, err := url.Parse(c.DownloadURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return StockAsset{}, fmt.Errorf("download %s: url must be https", key)
	}

	ctx, cancel := context.WithTimeout(ctx, s.dlTimeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.DownloadURL, nil)
	if err != nil {
		return StockAsset{}, fmt.Errorf("download %s: %w", key, err)
	}
	resp, err := s.client.Do(httpReq)
	if err != nil {
		return StockAsset{}, fmt.Errorf("download %s: %w", key, stripURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return StockAsset{}, fmt.Errorf("download %s: http status %d", key, resp.StatusCode)
	}
	if resp.ContentLength > s.maxBytes {
		return StockAsset{}, fmt.Errorf("download %s: %d bytes exceeds limit %d", key, resp.ContentLength, s.maxBytes)
	}

	base := fmt.Sprintf("stock-%s-%s", unsafeNameRe.ReplaceAllString(lic.Provider, "_"), unsafeNameRe.ReplaceAllString(lic.ProviderAssetID, "_"))
	final := filepath.Join(jobDir, base+".mp4")
	sidecar := filepath.Join(jobDir, base+".license.json")

	tmp, err := os.CreateTemp(jobDir, base+"-*.part")
	if err != nil {
		return StockAsset{}, fmt.Errorf("download %s: create temp file: %w", key, err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = tmp.Close()
			removeQuiet(tmpName)
		}
	}()

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, s.maxBytes+1))
	if err != nil {
		return StockAsset{}, fmt.Errorf("download %s: %w", key, stripURLError(err))
	}
	if n > s.maxBytes {
		return StockAsset{}, fmt.Errorf("download %s: exceeds limit %d bytes", key, s.maxBytes)
	}
	if n == 0 {
		return StockAsset{}, fmt.Errorf("download %s: empty body", key)
	}
	if err := tmp.Close(); err != nil {
		return StockAsset{}, fmt.Errorf("download %s: close temp file: %w", key, err)
	}

	a := StockAsset{
		License:     lic,
		Path:        final,
		LicensePath: sidecar,
		Bytes:       n,
		SHA256:      hex.EncodeToString(h.Sum(nil)),
		Width:       c.Width,
		Height:      c.Height,
		Seconds:     c.Seconds,
	}
	side, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return StockAsset{}, fmt.Errorf("download %s: encode license sidecar: %w", key, err)
	}
	if err := os.WriteFile(sidecar, side, 0o644); err != nil {
		return StockAsset{}, fmt.Errorf("download %s: write license sidecar: %w", key, err)
	}
	if err := os.Rename(tmpName, final); err != nil {
		removeQuiet(sidecar)
		return StockAsset{}, fmt.Errorf("download %s: move into place: %w", key, err)
	}
	ok = true
	return a, nil
}

// defaultStockClient refuses redirects away from https and caps redirects.
func defaultStockClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "https" {
				return errors.New("redirect to non-https url refused")
			}
			return nil
		},
	}
}

// stripURLError drops the request URL from *url.Error so query-string API keys
// (Pixabay) never end up in errors or logs.
func stripURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}

func isHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

func removeQuiet(paths ...string) {
	for _, p := range paths {
		_ = os.Remove(p)
	}
}

// searchJSON GETs a provider search URL and decodes the JSON body into out.
// provider names the source in errors; the URL is never included.
func searchJSON(ctx context.Context, client *http.Client, provider string, reqURL string, header http.Header, out any) error {
	ctx, cancel := context.WithTimeout(ctx, DefaultStockSearchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return fmt.Errorf("%s search: build request: %w", provider, stripURLError(err))
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s search: %w", provider, stripURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return fmt.Errorf("%s search: http status %d", provider, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxStockSearchBody)).Decode(out); err != nil {
		return fmt.Errorf("%s search: decode response: %w", provider, err)
	}
	return nil
}
