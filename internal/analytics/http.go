package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// HTTPAnalyticsAPI calls the official YouTube Analytics API v2 reports endpoint.
type HTTPAnalyticsAPI struct {
	HTTP    *http.Client
	BaseURL string // tests override
}

func (a *HTTPAnalyticsAPI) http() *http.Client {
	if a != nil && a.HTTP != nil {
		return a.HTTP
	}
	return http.DefaultClient
}

func (a *HTTPAnalyticsAPI) base() string {
	if a != nil && strings.TrimSpace(a.BaseURL) != "" {
		return strings.TrimRight(a.BaseURL, "/")
	}
	return "https://youtubeanalytics.googleapis.com/v2"
}

// PullVideo fetches views / avgViewPercentage / annotationClickThroughRate-like CTR
// proxies, likes, comments, shares, subscribersGained for one video.
func (a *HTTPAnalyticsAPI) PullVideo(ctx context.Context, tok *oauth2.Token, videoID, window string) (VideoMetrics, error) {
	var zero VideoMetrics
	if tok == nil || tok.AccessToken == "" {
		return zero, fmt.Errorf("missing access token")
	}
	end := time.Now().UTC()
	start := end.Add(-24 * time.Hour)
	if window == Window7d {
		start = end.Add(-7 * 24 * time.Hour)
	}
	q := url.Values{}
	q.Set("ids", "channel==MINE")
	q.Set("startDate", start.Format("2006-01-02"))
	q.Set("endDate", end.Format("2006-01-02"))
	q.Set("metrics", "views,estimatedMinutesWatched,averageViewPercentage,likes,comments,shares,subscribersGained,annotationClickThroughRate")
	q.Set("dimensions", "video")
	q.Set("filters", "video=="+videoID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.base()+"/reports?"+q.Encode(), nil)
	if err != nil {
		return zero, err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	resp, err := a.http().Do(req)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return zero, fmt.Errorf("analytics HTTP %d: %s", resp.StatusCode, redact(string(raw)))
	}
	return parseAnalyticsReport(raw)
}

func parseAnalyticsReport(raw []byte) (VideoMetrics, error) {
	var zero VideoMetrics
	var rep struct {
		ColumnHeaders []struct {
			Name string `json:"name"`
		} `json:"columnHeaders"`
		Rows [][]any `json:"rows"`
	}
	if err := json.Unmarshal(raw, &rep); err != nil {
		return zero, err
	}
	if len(rep.Rows) == 0 {
		return zero, nil
	}
	idx := map[string]int{}
	for i, h := range rep.ColumnHeaders {
		idx[h.Name] = i
	}
	row := rep.Rows[0]
	get := func(name string) float64 {
		i, ok := idx[name]
		if !ok || i >= len(row) {
			return 0
		}
		switch v := row[i].(type) {
		case float64:
			return v
		case string:
			f, _ := strconv.ParseFloat(v, 64)
			return f
		default:
			return 0
		}
	}
	minutes := get("estimatedMinutesWatched")
	return VideoMetrics{
		Views:        int64(get("views")),
		WatchSeconds: minutes * 60,
		AvgViewPct:   get("averageViewPercentage"),
		Likes:        int64(get("likes")),
		Comments:     int64(get("comments")),
		Shares:       int64(get("shares")),
		SubsGained:   int64(get("subscribersGained")),
		CTR:          get("annotationClickThroughRate"),
	}, nil
}

func redact(s string) string {
	if i := strings.Index(strings.ToLower(s), "bearer "); i >= 0 {
		return s[:i] + "bearer [REDACTED]"
	}
	return s
}
