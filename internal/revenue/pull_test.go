package revenue

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

type staticTok struct{ tok *oauth2.Token }

func (s staticTok) Token() (*oauth2.Token, error) { return s.tok, nil }

type fakeAdsAPI struct {
	amount float64
	calls  int
	err    error
}

func (f *fakeAdsAPI) PullChannelRevenue(ctx context.Context, tok *oauth2.Token, channelID, period string) (float64, string, error) {
	f.calls++
	if f.err != nil {
		return 0, "", f.err
	}
	return f.amount, "USD", nil
}

func TestPullYouTubeIdempotentUpdate(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	api := &fakeAdsAPI{amount: 10}
	p := &Puller{
		DB: d,
		Tokens: func(ctx context.Context, account string) (oauth2.TokenSource, error) {
			return staticTok{&oauth2.Token{AccessToken: "tok"}}, nil
		},
		API: api,
	}
	req := PullRequest{ChannelID: "yt-money-en", Platform: "youtube", Period: "2026-09-07"}
	id1, err := p.Pull(ctx, req)
	if err != nil || id1 == "" {
		t.Fatalf("pull1: id=%s err=%v", id1, err)
	}
	api.amount = 15.5
	id2, err := p.Pull(ctx, req)
	if err != nil || id2 != id1 {
		t.Fatalf("pull2: id=%s want=%s err=%v", id2, id1, err)
	}
	if api.calls != 2 {
		t.Fatalf("calls=%d", api.calls)
	}
	entries, err := List(ctx, d, Filter{Line: LineAds})
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries=%d err=%v", len(entries), err)
	}
	if entries[0].Amount != 15.5 {
		t.Fatalf("amount=%v", entries[0].Amount)
	}
}

func TestPullMetaUnsupported(t *testing.T) {
	p := &Puller{DB: testDB(t)}
	_, err := p.Pull(context.Background(), PullRequest{
		ChannelID: "page", Platform: "meta", Period: "2026-09-07",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported platform") {
		t.Fatalf("want unsupported platform, got %v", err)
	}
}

func TestHTTPAdsRevenueAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"columnHeaders": []map[string]string{{"name": "estimatedRevenue"}},
			"rows":          [][]any{{9.25}},
		})
	}))
	t.Cleanup(srv.Close)
	api := &HTTPAdsRevenueAPI{HTTP: srv.Client(), BaseURL: srv.URL}
	amt, cur, err := api.PullChannelRevenue(context.Background(), &oauth2.Token{AccessToken: "t"}, "ch", "2026-09-01")
	if err != nil || amt != 9.25 || cur != "USD" {
		t.Fatalf("amt=%v cur=%s err=%v", amt, cur, err)
	}
}

func TestHTTPAdsRevenueRedactsBearer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`error bearer ya29.secret`))
	}))
	t.Cleanup(srv.Close)
	api := &HTTPAdsRevenueAPI{HTTP: srv.Client(), BaseURL: srv.URL}
	_, _, err := api.PullChannelRevenue(context.Background(), &oauth2.Token{AccessToken: "t"}, "ch", "2026-09-01")
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), "ya29.") {
		t.Fatalf("token leaked: %v", err)
	}
}
