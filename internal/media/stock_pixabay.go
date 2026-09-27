package media

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	pixabayDefaultBaseURL = "https://pixabay.com"
	pixabayLicenseURL     = "https://pixabay.com/service/license-summary/"
	pixabayLicenseName    = "Pixabay Content License"
	pixabayMinPerPage     = 3
	pixabayMaxPerPage     = 200
)

// PixabayOptions overrides defaults (tests point BaseURL at httptest).
type PixabayOptions struct {
	BaseURL    string
	HTTPClient *http.Client
}

// Pixabay searches the official Pixabay Videos API (GET /api/videos/).
// Pixabay requires the key as a query parameter, so request URLs are never
// logged or put in errors (see stripURLError).
type Pixabay struct {
	key     string
	baseURL string
	client  *http.Client
}

// NewPixabay returns a Pixabay provider.
func NewPixabay(apiKey string, opts PixabayOptions) *Pixabay {
	p := &Pixabay{key: apiKey, baseURL: strings.TrimRight(opts.BaseURL, "/"), client: opts.HTTPClient}
	if p.baseURL == "" {
		p.baseURL = pixabayDefaultBaseURL
	}
	if p.client == nil {
		p.client = defaultStockClient()
	}
	return p
}

// Name implements StockProvider.
func (p *Pixabay) Name() string { return "pixabay" }

type pixabayRendition struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Size   int64  `json:"size"`
}

type pixabaySearchResponse struct {
	Hits []struct {
		ID       int64  `json:"id"`
		PageURL  string `json:"pageURL"`
		Duration int    `json:"duration"`
		User     string `json:"user"`
		UserID   int64  `json:"user_id"`
		Videos   struct {
			Large  pixabayRendition `json:"large"`
			Medium pixabayRendition `json:"medium"`
			Small  pixabayRendition `json:"small"`
			Tiny   pixabayRendition `json:"tiny"`
		} `json:"videos"`
	} `json:"hits"`
}

// Search implements StockProvider. The Pixabay video API has no orientation
// parameter; Stock.Fetch filters by the rendition's width and height.
func (p *Pixabay) Search(ctx context.Context, q StockQuery) ([]StockCandidate, error) {
	v := url.Values{}
	v.Set("key", p.key)
	v.Set("q", q.Text())
	v.Set("per_page", strconv.Itoa(clamp(q.PerPage, pixabayMinPerPage, pixabayMaxPerPage)))
	v.Set("safesearch", "true")
	v.Set("video_type", "film")

	var resp pixabaySearchResponse
	if err := searchJSON(ctx, p.client, p.Name(), p.baseURL+"/api/videos/?"+v.Encode(), nil, &resp); err != nil {
		return nil, err
	}
	var out []StockCandidate
	for _, hit := range resp.Hits {
		r, ok := pickPixabayRendition(q.MaxWidth, hit.Videos.Large, hit.Videos.Medium, hit.Videos.Small, hit.Videos.Tiny)
		if !ok || hit.ID == 0 {
			continue
		}
		author := ""
		authorURL := ""
		if hit.User != "" && hit.UserID != 0 {
			author = hit.User
			authorURL = "https://pixabay.com/users/" + url.PathEscape(hit.User) + "-" + strconv.FormatInt(hit.UserID, 10) + "/"
		}
		out = append(out, StockCandidate{
			License: LicenseRecord{
				Provider:        p.Name(),
				ProviderAssetID: strconv.FormatInt(hit.ID, 10),
				SourcePageURL:   hit.PageURL,
				LicenseName:     pixabayLicenseName,
				LicenseURL:      pixabayLicenseURL,
				Author:          author,
				AuthorURL:       authorURL,
			},
			DownloadURL: r.URL,
			Width:       r.Width,
			Height:      r.Height,
			Seconds:     hit.Duration,
			SizeBytes:   r.Size,
		})
	}
	return out, nil
}

// pickPixabayRendition returns the first rendition (largest first) whose long
// edge fits maxWidth, else the last non-empty one.
func pickPixabayRendition(maxWidth int, rs ...pixabayRendition) (pixabayRendition, bool) {
	var last pixabayRendition
	var have bool
	for _, r := range rs {
		if r.URL == "" {
			continue
		}
		last, have = r, true
		if maxWidth <= 0 || max(r.Width, r.Height) <= maxWidth {
			return r, true
		}
	}
	return last, have
}
