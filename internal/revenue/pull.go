package revenue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// AdsRevenueAPI is the remote surface for a semi-automated ads-revenue pull
// (fakeable in tests). HTTPAdsRevenueAPI is the real YouTube Analytics
// implementation.
//
// Flagged, not asserted (see tickets/M2-601.md Notes and
// docs/CONTEXT.md §5): which YouTube Analytics metric actually exposes ad
// revenue (`estimatedRevenue` vs `estimatedAdRevenue`) and whether the
// `yt-analytics-monetary.readonly` OAuth scope is already granted alongside
// M2-211/M2-212's existing scopes needs verification against current
// YouTube Analytics API docs before this is relied on for real numbers.
// PullChannelRevenue is written against the same reports endpoint shape
// M2-212's HTTPAnalyticsAPI already uses, but is not wired into any cron
// trigger or queue job by this ticket — see RegisterAsJob's doc comment.
type AdsRevenueAPI interface {
	PullChannelRevenue(ctx context.Context, tok *oauth2.Token, channelID, period string) (amount float64, currency string, err error)
}

// TokenSourceFor returns OAuth for a YouTube Brand Account, matching
// internal/analytics.TokenSourceFor's shape so callers can share one
// implementation.
type TokenSourceFor func(ctx context.Context, account string) (oauth2.TokenSource, error)

// Puller runs the semi-automated ads-revenue pull.
type Puller struct {
	DB     execer
	Tokens TokenSourceFor
	API    AdsRevenueAPI
	Now    func() time.Time
	Log    *slog.Logger
}

func (p *Puller) now() time.Time {
	if p != nil && p.Now != nil {
		return p.Now().UTC()
	}
	return time.Now().UTC()
}

func (p *Puller) log() *slog.Logger {
	if p != nil && p.Log != nil {
		return p.Log
	}
	return slog.Default()
}

// PullRequest identifies one channel+platform+period to pull ads revenue
// for. Period is a YYYY-MM-DD date, matching Entry.Date.
type PullRequest struct {
	ChannelID string
	Platform  string // "youtube" today; Meta has no equivalent API (see doc.go / ticket Notes)
	Period    string
}

// Pull fetches ads revenue for one channel+period and upserts a single
// `line: "ads"` entry, keyed for idempotency on (platform, channelID,
// period): re-running the pull for a period that already has an entry
// updates it in place rather than inserting a duplicate row.
func (p *Puller) Pull(ctx context.Context, req PullRequest) (id string, err error) {
	if p == nil || p.DB == nil {
		return "", fmt.Errorf("revenue: nil puller/db")
	}
	if strings.TrimSpace(req.ChannelID) == "" {
		return "", fmt.Errorf("revenue: channel_id required")
	}
	platform := strings.ToLower(strings.TrimSpace(req.Platform))
	if platform != "youtube" {
		return "", fmt.Errorf("revenue: unsupported platform %q (only youtube auto-pull is implemented)", req.Platform)
	}
	if strings.TrimSpace(req.Period) == "" {
		return "", fmt.Errorf("revenue: period required")
	}

	tok, err := p.token(ctx, req.ChannelID)
	if err != nil {
		return "", err
	}
	api := p.API
	if api == nil {
		api = &HTTPAdsRevenueAPI{}
	}
	amount, currency, err := api.PullChannelRevenue(ctx, tok, req.ChannelID, req.Period)
	if err != nil {
		return "", fmt.Errorf("revenue: pull %s/%s: %w", platform, req.ChannelID, err)
	}
	if currency == "" {
		currency = "USD"
	}

	source := idempotencySource(platform, req.ChannelID)

	var existingID string
	err = p.DB.QueryRowContext(ctx, `
SELECT id FROM revenue WHERE line=? AND source=? AND date=?`,
		string(LineAds), source, req.Period).Scan(&existingID)
	switch {
	case err == nil:
		if _, err := p.DB.ExecContext(ctx, `
UPDATE revenue SET amount=?, currency=? WHERE id=?`, amount, currency, existingID); err != nil {
			return "", fmt.Errorf("revenue: update %s: %w", existingID, err)
		}
		p.log().Info("revenue: ads pull updated", "channel_id", req.ChannelID, "period", req.Period, "amount", amount)
		return existingID, nil
	case errors.Is(err, sql.ErrNoRows):
		newID, recErr := Record(ctx, p.DB, Entry{
			Line:     LineAds,
			Source:   source,
			Amount:   amount,
			Currency: currency,
			Date:     req.Period,
			Note:     "auto-pulled from YouTube Analytics",
		})
		if recErr != nil {
			return "", fmt.Errorf("revenue: insert: %w", recErr)
		}
		p.log().Info("revenue: ads pull inserted", "channel_id", req.ChannelID, "period", req.Period, "amount", amount)
		return newID, nil
	default:
		return "", fmt.Errorf("revenue: lookup existing entry: %w", err)
	}
}

// idempotencySource builds the source value auto-pulled ads rows use to
// find themselves on a re-run: "<platform>:<channel_id>".
func idempotencySource(platform, channelID string) string {
	return platform + ":" + channelID
}

func (p *Puller) token(ctx context.Context, account string) (*oauth2.Token, error) {
	if p.Tokens == nil {
		return nil, fmt.Errorf("revenue: token source not configured")
	}
	ts, err := p.Tokens(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("revenue: token for %s: %w", account, err)
	}
	tok, err := ts.Token()
	if err != nil {
		return nil, fmt.Errorf("revenue: refresh for %s: %w", account, err)
	}
	if tok == nil || tok.AccessToken == "" {
		return nil, fmt.Errorf("revenue: empty access token for %s", account)
	}
	return tok, nil
}

// Note on wiring: this ticket does not register a `revenue.pull` queue job
// type or a scheduler.RegisterHandlers entry. SPEC.md §5's job-type list
// does not currently list `revenue.pull`, and cmd/mayank2/run.go (M2-116)
// has not shipped a daemon wiring point yet — tickets/M2-601.md's own Notes
// flag this gap explicitly ("needs wiring into cmd/mayank2/run.go... note
// the gap ... if M2-116 has already shipped by the time this is picked
// up"). Puller.Pull is exported and fully unit-tested so a follow-up ticket
// (or M2-116 itself once it exists) can wire it behind a job type without
// changing this package.
