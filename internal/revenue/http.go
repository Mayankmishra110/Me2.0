package revenue

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/oauth2"
)

// HTTPAdsRevenueAPI calls the YouTube Analytics API v2 reports endpoint for
// the `estimatedRevenue` metric, mirroring internal/analytics.HTTPAnalyticsAPI's
// shape. See pull.go's AdsRevenueAPI doc comment: the exact metric name and
// OAuth scope (`yt-analytics-monetary.readonly`) need verification against
// current YouTube Analytics API docs before this is relied on for real
// numbers — flagged in docs/CONTEXT.md §5, not silently assumed correct.
type HTTPAdsRevenueAPI struct {
	HTTP    *http.Client
	BaseURL string // tests override
}

func (a *HTTPAdsRevenueAPI) http() *http.Client {
	if a != nil && a.HTTP != nil {
		return a.HTTP
	}
	return http.DefaultClient
}

func (a *HTTPAdsRevenueAPI) base() string {
	if a != nil && strings.TrimSpace(a.BaseURL) != "" {
		return strings.TrimRight(a.BaseURL, "/")
	}
	return "https://youtubeanalytics.googleapis.com/v2"
}

// PullChannelRevenue fetches estimated ad revenue for one channel/period.
func (a *HTTPAdsRevenueAPI) PullChannelRevenue(ctx context.Context, tok *oauth2.Token, channelID, period string) (float64, string, error) {
	if tok == nil || tok.AccessToken == "" {
		return 0, "", fmt.Errorf("missing access token")
	}
	if strings.TrimSpace(period) == "" {
		return 0, "", fmt.Errorf("period required")
	}

	q := url.Values{}
	q.Set("ids", "channel==MINE")
	q.Set("startDate", period)
	q.Set("endDate", period)
	q.Set("metrics", "estimatedRevenue")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.base()+"/reports?"+q.Encode(), nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	resp, err := a.http().Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, "", fmt.Errorf("revenue analytics HTTP %d: %s", resp.StatusCode, redact(string(raw)))
	}
	return parseRevenueReport(raw)
}

func parseRevenueReport(raw []byte) (float64, string, error) {
	var rep struct {
		ColumnHeaders []struct {
			Name string `json:"name"`
		} `json:"columnHeaders"`
		Rows [][]any `json:"rows"`
	}
	if err := json.Unmarshal(raw, &rep); err != nil {
		return 0, "", err
	}
	if len(rep.Rows) == 0 {
		return 0, "USD", nil
	}
	idx := map[string]int{}
	for i, h := range rep.ColumnHeaders {
		idx[h.Name] = i
	}
	row := rep.Rows[0]
	i, ok := idx["estimatedRevenue"]
	if !ok || i >= len(row) {
		return 0, "USD", nil
	}
	switch v := row[i].(type) {
	case float64:
		return v, "USD", nil
	case string:
		f, _ := strconv.ParseFloat(v, 64)
		return f, "USD", nil
	default:
		return 0, "USD", nil
	}
}

func redact(s string) string {
	if i := strings.Index(strings.ToLower(s), "bearer "); i >= 0 {
		return s[:i] + "bearer [REDACTED]"
	}
	return s
}
