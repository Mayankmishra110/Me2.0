package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testPexelsKey  = "pexels-secret-key-123"
	testPixabayKey = "pixabay-secret-key-456"
)

// fakeStockAPI is one TLS server that plays both APIs and the file CDN,
// answering with the recorded-style fixtures in testdata/.
type fakeStockAPI struct {
	srv *httptest.Server

	mu            sync.Mutex
	pexelsStatus  int // 0 = 200 with fixture
	pixabayStatus int
	fileDelay     time.Duration
	fileBody      []byte
	pexelsQueries []string
	pixabayQuery  []string
	downloads     []string
}

func newFakeStockAPI(t *testing.T) *fakeStockAPI {
	t.Helper()
	f := &fakeStockAPI{fileBody: bytes.Repeat([]byte("mp4!"), 256)}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeStockAPI) fixture(name string) []byte {
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		panic(err)
	}
	return bytes.ReplaceAll(raw, []byte("{{BASE}}"), []byte(f.srv.URL))
}

func (f *fakeStockAPI) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	pexStatus, pixStatus, delay, body := f.pexelsStatus, f.pixabayStatus, f.fileDelay, f.fileBody
	f.mu.Unlock()
	switch {
	case r.URL.Path == "/videos/search":
		if r.Header.Get("Authorization") != testPexelsKey {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		f.pexelsQueries = append(f.pexelsQueries, r.URL.RawQuery)
		f.mu.Unlock()
		if pexStatus != 0 {
			http.Error(w, "boom", pexStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(f.fixture("pexels_videos_search.json"))
	case r.URL.Path == "/api/videos/":
		if r.URL.Query().Get("key") != testPixabayKey {
			http.Error(w, "[ERROR 400] Invalid or missing API key", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.pixabayQuery = append(f.pixabayQuery, r.URL.RawQuery)
		f.mu.Unlock()
		if pixStatus != 0 {
			http.Error(w, "boom", pixStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(f.fixture("pixabay_videos_search.json"))
	case strings.HasPrefix(r.URL.Path, "/files/"):
		f.mu.Lock()
		f.downloads = append(f.downloads, r.URL.Path)
		f.mu.Unlock()
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write(append([]byte(r.URL.Path+":"), body...))
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeStockAPI) pexels() *Pexels {
	return NewPexels(testPexelsKey, PexelsOptions{BaseURL: f.srv.URL, HTTPClient: f.srv.Client()})
}

func (f *fakeStockAPI) pixabay() *Pixabay {
	return NewPixabay(testPixabayKey, PixabayOptions{BaseURL: f.srv.URL, HTTPClient: f.srv.Client()})
}

// fixedClock is a settable clock for reuse-window tests.
type fixedClock struct{ t time.Time }

func (c *fixedClock) now() time.Time { return c.t }

func newTestStock(t *testing.T, f *fakeStockAPI, providers []StockProvider, mod func(*StockOptions)) (*Stock, *fixedClock, *bytes.Buffer) {
	t.Helper()
	clock := &fixedClock{t: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)}
	logs := &bytes.Buffer{}
	opts := StockOptions{
		HTTPClient: f.srv.Client(),
		Usage:      NewMemoryUsageStore(),
		Now:        clock.now,
		Logger:     slog.New(slog.NewTextHandler(logs, nil)),
	}
	if mod != nil {
		mod(&opts)
	}
	s, err := NewStock(providers, opts)
	if err != nil {
		t.Fatalf("NewStock: %v", err)
	}
	return s, clock, logs
}

func envMap(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestNewStockFromEnv(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		wantErr     bool
		wantMissing []string
		want        []string
	}{
		{name: "no keys", env: map[string]string{}, wantErr: true, wantMissing: []string{PexelsKeyEnv, PixabayKeyEnv}},
		{name: "empty keys from .env.example", env: map[string]string{PexelsKeyEnv: "", PixabayKeyEnv: "  "}, wantErr: true, wantMissing: []string{PexelsKeyEnv, PixabayKeyEnv}},
		{name: "pexels only", env: map[string]string{PexelsKeyEnv: "k1"}, want: []string{"pexels"}},
		{name: "pixabay only", env: map[string]string{PixabayKeyEnv: "k2", PexelsKeyEnv: ""}, want: []string{"pixabay"}},
		{name: "both", env: map[string]string{PexelsKeyEnv: "k1", PixabayKeyEnv: "k2"}, want: []string{"pexels", "pixabay"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := NewStockFromEnv(envMap(tt.env), StockOptions{})
			if tt.wantErr {
				if !errors.Is(err, ErrStockNotConfigured) {
					t.Fatalf("err = %v, want ErrStockNotConfigured", err)
				}
				var nc *NotConfiguredError
				if !errors.As(err, &nc) {
					t.Fatalf("err type = %T, want *NotConfiguredError", err)
				}
				if strings.Join(nc.MissingEnv, ",") != strings.Join(tt.wantMissing, ",") {
					t.Errorf("MissingEnv = %v, want %v", nc.MissingEnv, tt.wantMissing)
				}
				if s != nil {
					t.Errorf("Stock = %v, want nil", s)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewStockFromEnv: %v", err)
			}
			if got := strings.Join(s.Providers(), ","); got != strings.Join(tt.want, ",") {
				t.Errorf("Providers = %s, want %v", got, tt.want)
			}
		})
	}
}

func TestNewStockNoProviders(t *testing.T) {
	for _, ps := range [][]StockProvider{nil, {}, {nil}} {
		if _, err := NewStock(ps, StockOptions{}); !errors.Is(err, ErrStockNotConfigured) {
			t.Errorf("NewStock(%v) err = %v, want ErrStockNotConfigured", ps, err)
		}
	}
}

func TestFetchPexelsLandscapeWithLicense(t *testing.T) {
	f := newFakeStockAPI(t)
	s, _, _ := newTestStock(t, f, []StockProvider{f.pexels()}, nil)
	jobDir := filepath.Join(t.TempDir(), "job-1")

	assets, err := s.Fetch(context.Background(), FetchRequest{
		ChannelID: "en-tech", JobDir: jobDir, Keywords: []string{"city", " night "},
		Orientation: OrientationLandscape, Count: 5,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	// 1001 and 1002 are landscape; 1003 is portrait; 1004 has no page URL.
	if len(assets) != 2 {
		t.Fatalf("got %d assets, want 2: %+v", len(assets), assets)
	}
	if q := f.pexelsQueries[0]; !strings.Contains(q, "query=city+night") || !strings.Contains(q, "orientation=landscape") {
		t.Errorf("pexels query = %q", q)
	}
	a := assets[0]
	if a.License.ProviderAssetID != "1001" || a.License.Provider != "pexels" {
		t.Errorf("first asset = %s/%s", a.License.Provider, a.License.ProviderAssetID)
	}
	if a.Width != 1920 || a.Height != 1080 {
		t.Errorf("rendition %dx%d, want 1920x1080 (uhd skipped by MaxWidth)", a.Width, a.Height)
	}
	if f.downloads[0] != "/files/pexels-1001-hd.mp4" {
		t.Errorf("downloaded %s, want hd rendition", f.downloads[0])
	}
	for _, a := range assets {
		assertLicensedAsset(t, a, jobDir)
		if a.License.LicenseURL != pexelsLicenseURL {
			t.Errorf("license url = %q", a.License.LicenseURL)
		}
	}
}

// assertLicensedAsset checks the F1 contract: file present in the job dir,
// hash/bytes right, full license record, and a matching sidecar.
func assertLicensedAsset(t *testing.T, a StockAsset, jobDir string) {
	t.Helper()
	if err := a.License.Validate(); err != nil {
		t.Errorf("license: %v", err)
	}
	if a.License.RetrievedAt.IsZero() {
		t.Errorf("%s: RetrievedAt is zero", a.Path)
	}
	absJob, _ := filepath.Abs(jobDir)
	if filepath.Dir(a.Path) != absJob {
		t.Errorf("path %s not in job dir %s", a.Path, absJob)
	}
	data, err := os.ReadFile(a.Path)
	if err != nil {
		t.Fatalf("read clip: %v", err)
	}
	sum := sha256.Sum256(data)
	if a.SHA256 != hex.EncodeToString(sum[:]) || a.Bytes != int64(len(data)) {
		t.Errorf("sha/bytes mismatch for %s", a.Path)
	}
	raw, err := os.ReadFile(a.LicensePath)
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	var side StockAsset
	if err := json.Unmarshal(raw, &side); err != nil {
		t.Fatalf("decode sidecar: %v", err)
	}
	if side.License != a.License || side.Path != a.Path || side.SHA256 != a.SHA256 {
		t.Errorf("sidecar %+v does not match asset %+v", side, a)
	}
}

func TestFetchOrientationAndDuration(t *testing.T) {
	tests := []struct {
		name       string
		providers  func(*fakeStockAPI) []StockProvider
		orient     Orientation
		minSeconds int
		wantIDs    []string
	}{
		{"pexels portrait", func(f *fakeStockAPI) []StockProvider { return []StockProvider{f.pexels()} }, OrientationPortrait, 0, []string{"1003"}},
		{"pexels min 10s", func(f *fakeStockAPI) []StockProvider { return []StockProvider{f.pexels()} }, OrientationLandscape, 10, []string{"1001"}},
		{"pixabay portrait filtered client side", func(f *fakeStockAPI) []StockProvider { return []StockProvider{f.pixabay()} }, OrientationPortrait, 0, []string{"2002"}},
		{"pixabay landscape", func(f *fakeStockAPI) []StockProvider { return []StockProvider{f.pixabay()} }, OrientationLandscape, 0, []string{"2001"}},
		{"pixabay any", func(f *fakeStockAPI) []StockProvider { return []StockProvider{f.pixabay()} }, OrientationAny, 0, []string{"2001", "2002"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeStockAPI(t)
			s, _, _ := newTestStock(t, f, tt.providers(f), nil)
			assets, err := s.Fetch(context.Background(), FetchRequest{
				ChannelID: "c", JobDir: t.TempDir(), Keywords: []string{"x"},
				Orientation: tt.orient, MinSeconds: tt.minSeconds, Count: 5,
			})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			var ids []string
			for _, a := range assets {
				ids = append(ids, a.License.ProviderAssetID)
			}
			if strings.Join(ids, ",") != strings.Join(tt.wantIDs, ",") {
				t.Errorf("ids = %v, want %v", ids, tt.wantIDs)
			}
		})
	}
}

func TestFetchPixabayKeyNeverLeaks(t *testing.T) {
	f := newFakeStockAPI(t)
	f.pixabayStatus = http.StatusInternalServerError
	s, _, logs := newTestStock(t, f, []StockProvider{f.pixabay()}, nil)

	_, err := s.Fetch(context.Background(), FetchRequest{ChannelID: "c", JobDir: t.TempDir(), Keywords: []string{"ocean"}})
	if err == nil {
		t.Fatal("want error")
	}
	if !errors.Is(err, ErrNoStockMatch) {
		t.Errorf("err = %v, want ErrNoStockMatch", err)
	}
	if !strings.Contains(f.pixabayQuery[0], "key="+testPixabayKey) {
		t.Errorf("pixabay query %q should carry the key", f.pixabayQuery[0])
	}
	for _, s := range []string{err.Error(), logs.String()} {
		if strings.Contains(s, testPixabayKey) {
			t.Errorf("pixabay key leaked: %s", s)
		}
	}

	// Transport error (server gone): *url.Error would carry the URL with the key.
	f.srv.Close()
	_, err = f.pixabay().Search(context.Background(), StockQuery{Keywords: []string{"ocean"}})
	if err == nil || strings.Contains(err.Error(), testPixabayKey) {
		t.Errorf("transport err = %v; must be non-nil and key-free", err)
	}
}

func TestFetchFallsBackToSecondProvider(t *testing.T) {
	f := newFakeStockAPI(t)
	f.pexelsStatus = http.StatusTooManyRequests
	s, _, logs := newTestStock(t, f, []StockProvider{f.pexels(), f.pixabay()}, nil)

	assets, err := s.Fetch(context.Background(), FetchRequest{ChannelID: "c", JobDir: t.TempDir(), Keywords: []string{"ocean"}, Orientation: OrientationLandscape})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(assets) != 1 || assets[0].License.Provider != "pixabay" || assets[0].License.LicenseURL != pixabayLicenseURL {
		t.Fatalf("assets = %+v, want one pixabay clip", assets)
	}
	if assets[0].License.AuthorURL != "https://pixabay.com/users/SeaShots-777/" {
		t.Errorf("author url = %q", assets[0].License.AuthorURL)
	}
	if !strings.Contains(logs.String(), "http status 429") || strings.Contains(logs.String(), testPexelsKey) {
		t.Errorf("logs = %s", logs.String())
	}
}

func TestFetchReuseWindow(t *testing.T) {
	f := newFakeStockAPI(t)
	s, clock, _ := newTestStock(t, f, []StockProvider{f.pexels()}, nil)
	ctx := context.Background()
	fetch := func(channel string) string {
		t.Helper()
		assets, err := s.Fetch(ctx, FetchRequest{ChannelID: channel, JobDir: t.TempDir(), Keywords: []string{"city"}, Orientation: OrientationLandscape})
		if errors.Is(err, ErrNoStockMatch) {
			return "none"
		}
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		return assets[0].License.ProviderAssetID
	}

	steps := []struct {
		advance time.Duration
		channel string
		want    string
	}{
		{0, "en-tech", "1001"},
		{24 * time.Hour, "en-tech", "1002"},                // 1001 used yesterday
		{0, "hi-finance", "1001"},                          // other channel may use it
		{24 * time.Hour, "en-tech", "none"},                // both landscape clips used within 30 days
		{28*24*time.Hour - time.Minute, "en-tech", "none"}, // 29d23h59m after first use
		{time.Minute, "en-tech", "1001"},                   // exactly 30 days after first use
		{0, "en-tech", "none"},                             // 1002 used 29 days ago, 1001 just now
	}
	for i, st := range steps {
		clock.t = clock.t.Add(st.advance)
		if got := fetch(st.channel); got != st.want {
			t.Fatalf("step %d (%s at %s): got %s, want %s", i, st.channel, clock.t.Format(time.RFC3339), got, st.want)
		}
	}
}

func TestFetchNoDuplicatesInOneBatch(t *testing.T) {
	f := newFakeStockAPI(t)
	// Same provider listed twice returns the same hits twice.
	s, _, _ := newTestStock(t, f, []StockProvider{f.pexels(), f.pexels()}, nil)
	assets, err := s.Fetch(context.Background(), FetchRequest{ChannelID: "c", JobDir: t.TempDir(), Keywords: []string{"x"}, Count: 10})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	seen := map[string]bool{}
	for _, a := range assets {
		if seen[a.License.ProviderAssetID] {
			t.Errorf("duplicate %s", a.License.ProviderAssetID)
		}
		seen[a.License.ProviderAssetID] = true
	}
	if len(assets) != 3 {
		t.Errorf("got %d assets, want 3 (1001, 1002, 1003)", len(assets))
	}
}

func TestFetchDownloadBounds(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*fakeStockAPI, *StockOptions)
	}{
		{"too large", func(f *fakeStockAPI, o *StockOptions) { o.MaxBytes = 100 }},
		{"timeout", func(f *fakeStockAPI, o *StockOptions) {
			f.fileDelay = 2 * time.Second
			o.DownloadTimeout = 50 * time.Millisecond
		}},
		{"empty body", func(f *fakeStockAPI, o *StockOptions) { f.fileBody = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeStockAPI(t)
			if tt.name == "empty body" {
				// Serve a truly empty body for files.
				f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasPrefix(r.URL.Path, "/files/") {
						return
					}
					f.serve(w, r)
				})
			}
			s, _, _ := newTestStock(t, f, []StockProvider{f.pexels()}, func(o *StockOptions) { tt.setup(f, o) })
			jobDir := t.TempDir()
			_, err := s.Fetch(context.Background(), FetchRequest{ChannelID: "c", JobDir: jobDir, Keywords: []string{"x"}, Count: 3})
			if !errors.Is(err, ErrNoStockMatch) {
				t.Fatalf("err = %v, want ErrNoStockMatch", err)
			}
			left, _ := os.ReadDir(jobDir)
			if len(left) != 0 {
				t.Errorf("job dir not clean: %v", left)
			}
			// A failed download must not burn the clip for the channel.
			if _, used, _ := s.usage.LastUsed(context.Background(), "c", "pexels", "1001"); used {
				t.Error("failed download recorded as used")
			}
		})
	}
}

func TestFetchSkipsPixabayOversizeBeforeDownload(t *testing.T) {
	f := newFakeStockAPI(t)
	s, _, _ := newTestStock(t, f, []StockProvider{f.pixabay()}, func(o *StockOptions) { o.MaxBytes = 5_000_000 })
	_, err := s.Fetch(context.Background(), FetchRequest{ChannelID: "c", JobDir: t.TempDir(), Keywords: []string{"x"}, Count: 2})
	if !errors.Is(err, ErrNoStockMatch) {
		t.Fatalf("err = %v, want ErrNoStockMatch", err)
	}
	if len(f.downloads) != 0 {
		t.Errorf("downloads = %v, want none (declared size over limit)", f.downloads)
	}
}

// stubProvider returns fixed candidates.
type stubProvider struct {
	cands []StockCandidate
	err   error
}

func (p stubProvider) Name() string { return "stub" }
func (p stubProvider) Search(context.Context, StockQuery) ([]StockCandidate, error) {
	return p.cands, p.err
}

func TestFetchRejectsUnlicensedOrUnsafe(t *testing.T) {
	f := newFakeStockAPI(t)
	good := LicenseRecord{Provider: "stub", ProviderAssetID: "9", SourcePageURL: "https://example.com/v/9", LicenseURL: "https://example.com/license"}
	tests := []struct {
		name string
		cand StockCandidate
	}{
		{"no page url", StockCandidate{License: LicenseRecord{Provider: "stub", ProviderAssetID: "1", LicenseURL: good.LicenseURL}, DownloadURL: f.srv.URL + "/files/a.mp4"}},
		{"no license url", StockCandidate{License: LicenseRecord{Provider: "stub", ProviderAssetID: "2", SourcePageURL: good.SourcePageURL}, DownloadURL: f.srv.URL + "/files/a.mp4"}},
		{"no asset id", StockCandidate{License: LicenseRecord{Provider: "stub", SourcePageURL: good.SourcePageURL, LicenseURL: good.LicenseURL}, DownloadURL: f.srv.URL + "/files/a.mp4"}},
		{"plain http download", StockCandidate{License: good, DownloadURL: "http://example.com/a.mp4"}},
		{"file url", StockCandidate{License: good, DownloadURL: "file:///C:/Windows/win.ini"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _, _ := newTestStock(t, f, []StockProvider{stubProvider{cands: []StockCandidate{tt.cand}}}, nil)
			jobDir := t.TempDir()
			assets, err := s.Fetch(context.Background(), FetchRequest{ChannelID: "c", JobDir: jobDir, Keywords: []string{"x"}})
			if !errors.Is(err, ErrNoStockMatch) || assets != nil {
				t.Fatalf("assets=%v err=%v, want none + ErrNoStockMatch", assets, err)
			}
			if left, _ := os.ReadDir(jobDir); len(left) != 0 {
				t.Errorf("job dir not clean: %v", left)
			}
			if len(f.downloads) != 0 {
				t.Errorf("downloaded %v", f.downloads)
			}
		})
	}
}

func TestFetchValidation(t *testing.T) {
	f := newFakeStockAPI(t)
	s, _, _ := newTestStock(t, f, []StockProvider{f.pexels()}, nil)
	dir := t.TempDir()
	tests := []struct {
		name string
		req  FetchRequest
		want string
	}{
		{"no channel", FetchRequest{JobDir: dir, Keywords: []string{"x"}}, "channel id"},
		{"no job dir", FetchRequest{ChannelID: "c", Keywords: []string{"x"}}, "job dir"},
		{"no keywords", FetchRequest{ChannelID: "c", JobDir: dir}, "keyword"},
		{"blank keywords", FetchRequest{ChannelID: "c", JobDir: dir, Keywords: []string{" ", ""}}, "keyword"},
		{"bad orientation", FetchRequest{ChannelID: "c", JobDir: dir, Keywords: []string{"x"}, Orientation: "diagonal"}, "orientation"},
		{"count too high", FetchRequest{ChannelID: "c", JobDir: dir, Keywords: []string{"x"}, Count: 99}, "count"},
		{"negative count", FetchRequest{ChannelID: "c", JobDir: dir, Keywords: []string{"x"}, Count: -1}, "count"},
		{"negative min seconds", FetchRequest{ChannelID: "c", JobDir: dir, Keywords: []string{"x"}, MinSeconds: -1}, "min seconds"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.Fetch(context.Background(), tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want mention of %q", err, tt.want)
			}
		})
	}
	if len(f.pexelsQueries) != 0 {
		t.Errorf("invalid requests reached the API: %v", f.pexelsQueries)
	}
}

func TestFetchContextCancelled(t *testing.T) {
	f := newFakeStockAPI(t)
	s, _, _ := newTestStock(t, f, []StockProvider{f.pexels()}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.Fetch(ctx, FetchRequest{ChannelID: "c", JobDir: t.TempDir(), Keywords: []string{"x"}})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestFetchUsageStoreError(t *testing.T) {
	f := newFakeStockAPI(t)
	s, _, _ := newTestStock(t, f, []StockProvider{f.pexels()}, func(o *StockOptions) { o.Usage = failingUsage{} })
	jobDir := t.TempDir()
	_, err := s.Fetch(context.Background(), FetchRequest{ChannelID: "c", JobDir: jobDir, Keywords: []string{"x"}})
	if !errors.Is(err, ErrNoStockMatch) || !strings.Contains(err.Error(), "usage lookup") {
		t.Fatalf("err = %v", err)
	}
	if len(f.downloads) != 0 {
		t.Errorf("downloaded without a usage check: %v", f.downloads)
	}
}

type failingUsage struct{}

func (failingUsage) LastUsed(context.Context, string, string, string) (time.Time, bool, error) {
	return time.Time{}, false, errors.New("db locked")
}
func (failingUsage) RecordUse(context.Context, string, string, string, time.Time) error {
	return errors.New("db locked")
}

func TestOrientationMatches(t *testing.T) {
	tests := []struct {
		o    Orientation
		w, h int
		want bool
	}{
		{OrientationLandscape, 1920, 1080, true},
		{OrientationLandscape, 1080, 1920, false},
		{OrientationLandscape, 1000, 950, false},
		{OrientationPortrait, 1080, 1920, true},
		{OrientationPortrait, 1920, 1080, false},
		{OrientationSquare, 1080, 1080, true},
		{OrientationSquare, 1080, 1150, true},
		{OrientationSquare, 1920, 1080, false},
		{OrientationAny, 1920, 1080, true},
		{OrientationAny, 0, 0, true},
		{OrientationLandscape, 0, 0, false},
	}
	for _, tt := range tests {
		if got := tt.o.matches(tt.w, tt.h); got != tt.want {
			t.Errorf("%q.matches(%d,%d) = %v, want %v", tt.o, tt.w, tt.h, got, tt.want)
		}
	}
}

func TestPickPexelsFile(t *testing.T) {
	uhd := pexelsFile{FileType: "video/mp4", Width: 3840, Height: 2160, Link: "uhd"}
	hd := pexelsFile{FileType: "video/mp4", Width: 1920, Height: 1080, Link: "hd"}
	sd := pexelsFile{FileType: "video/mp4", Width: 640, Height: 360, Link: "sd"}
	vert := pexelsFile{FileType: "video/mp4", Width: 1080, Height: 1920, Link: "vert"}
	webm := pexelsFile{FileType: "video/webm", Width: 1280, Height: 720, Link: "webm"}
	tests := []struct {
		name  string
		files []pexelsFile
		max   int
		want  string
	}{
		{"widest under limit", []pexelsFile{uhd, sd, hd}, 1920, "hd"},
		{"all too wide → smallest", []pexelsFile{uhd, hd}, 1280, "hd"},
		{"no limit → largest", []pexelsFile{sd, uhd, hd}, 0, "uhd"},
		{"portrait long edge", []pexelsFile{vert, sd}, 1920, "vert"},
		{"skips non-mp4", []pexelsFile{webm, sd}, 1920, "sd"},
		{"none", []pexelsFile{webm}, 1920, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := pickPexelsFile(tt.files, tt.max)
			if got.Link != tt.want || ok != (tt.want != "") {
				t.Errorf("got %q ok=%v, want %q", got.Link, ok, tt.want)
			}
		})
	}
}

func TestPickPixabayRendition(t *testing.T) {
	large := pixabayRendition{URL: "large", Width: 3840, Height: 2160}
	medium := pixabayRendition{URL: "medium", Width: 1920, Height: 1080}
	tiny := pixabayRendition{URL: "tiny", Width: 640, Height: 360}
	tests := []struct {
		name string
		rs   []pixabayRendition
		max  int
		want string
	}{
		{"first that fits", []pixabayRendition{large, medium, tiny}, 1920, "medium"},
		{"empty large skipped", []pixabayRendition{{}, medium, tiny}, 1920, "medium"},
		{"none fit → last", []pixabayRendition{large, medium}, 100, "medium"},
		{"no limit → largest", []pixabayRendition{large, medium}, 0, "large"},
		{"all empty", []pixabayRendition{{}, {}}, 1920, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := pickPixabayRendition(tt.max, tt.rs...)
			if got.URL != tt.want || ok != (tt.want != "") {
				t.Errorf("got %q ok=%v, want %q", got.URL, ok, tt.want)
			}
		})
	}
}

func TestSearchJSONErrors(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad-json" {
			_, _ = io.WriteString(w, "{not json")
			return
		}
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()
	var out map[string]any
	if err := searchJSON(context.Background(), srv.Client(), "p", srv.URL+"/bad-json", nil, &out); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Errorf("bad json err = %v", err)
	}
	if err := searchJSON(context.Background(), srv.Client(), "p", srv.URL+"/x", nil, &out); err == nil || !strings.Contains(err.Error(), "http status 403") {
		t.Errorf("403 err = %v", err)
	}
}
