package media

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	pexelsDefaultBaseURL = "https://api.pexels.com"
	pexelsLicenseURL     = "https://www.pexels.com/license/"
	pexelsLicenseName    = "Pexels License"
	pexelsMaxPerPage     = 80
)

// PexelsOptions overrides defaults (tests point BaseURL at httptest).
type PexelsOptions struct {
	BaseURL    string
	HTTPClient *http.Client
}

// Pexels searches the official Pexels Videos API (GET /videos/search).
type Pexels struct {
	key     string
	baseURL string
	client  *http.Client
}

// NewPexels returns a Pexels provider. The key is sent in the Authorization
// header and never logged.
func NewPexels(apiKey string, opts PexelsOptions) *Pexels {
	p := &Pexels{key: apiKey, baseURL: strings.TrimRight(opts.BaseURL, "/"), client: opts.HTTPClient}
	if p.baseURL == "" {
		p.baseURL = pexelsDefaultBaseURL
	}
	if p.client == nil {
		p.client = defaultStockClient()
	}
	return p
}

// Name implements StockProvider.
func (p *Pexels) Name() string { return "pexels" }

type pexelsSearchResponse struct {
	Videos []struct {
		ID       int64  `json:"id"`
		Width    int    `json:"width"`
		Height   int    `json:"height"`
		URL      string `json:"url"`
		Duration int    `json:"duration"`
		User     struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"user"`
		VideoFiles []pexelsFile `json:"video_files"`
	} `json:"videos"`
}

type pexelsFile struct {
	Quality  string `json:"quality"`
	FileType string `json:"file_type"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Link     string `json:"link"`
}

// Search implements StockProvider.
func (p *Pexels) Search(ctx context.Context, q StockQuery) ([]StockCandidate, error) {
	v := url.Values{}
	v.Set("query", q.Text())
	v.Set("per_page", strconv.Itoa(clamp(q.PerPage, 1, pexelsMaxPerPage)))
	if q.Orientation != OrientationAny {
		v.Set("orientation", string(q.Orientation))
	}
	h := http.Header{}
	h.Set("Authorization", p.key)

	var resp pexelsSearchResponse
	if err := searchJSON(ctx, p.client, p.Name(), p.baseURL+"/videos/search?"+v.Encode(), h, &resp); err != nil {
		return nil, err
	}
	var out []StockCandidate
	for _, vid := range resp.Videos {
		f, ok := pickPexelsFile(vid.VideoFiles, q.MaxWidth)
		if !ok || vid.ID == 0 {
			continue
		}
		w, hgt := f.Width, f.Height
		if w == 0 || hgt == 0 {
			w, hgt = vid.Width, vid.Height
		}
		out = append(out, StockCandidate{
			License: LicenseRecord{
				Provider:        p.Name(),
				ProviderAssetID: strconv.FormatInt(vid.ID, 10),
				SourcePageURL:   vid.URL,
				LicenseName:     pexelsLicenseName,
				LicenseURL:      pexelsLicenseURL,
				Author:          vid.User.Name,
				AuthorURL:       vid.User.URL,
			},
			DownloadURL: f.Link,
			Width:       w,
			Height:      hgt,
			Seconds:     vid.Duration,
		})
	}
	return out, nil
}

// pickPexelsFile picks the widest mp4 rendition not wider than maxWidth (on
// the long edge), falling back to the narrowest mp4 if all are wider.
func pickPexelsFile(files []pexelsFile, maxWidth int) (pexelsFile, bool) {
	var best, smallest pexelsFile
	var haveBest, haveSmallest bool
	for _, f := range files {
		if f.FileType != "video/mp4" || f.Link == "" {
			continue
		}
		edge := max(f.Width, f.Height)
		if !haveSmallest || edge < max(smallest.Width, smallest.Height) {
			smallest, haveSmallest = f, true
		}
		if maxWidth > 0 && edge > maxWidth {
			continue
		}
		if !haveBest || edge > max(best.Width, best.Height) {
			best, haveBest = f, true
		}
	}
	if haveBest {
		return best, true
	}
	return smallest, haveSmallest
}

func clamp(n, lo, hi int) int {
	return min(max(n, lo), hi)
}
