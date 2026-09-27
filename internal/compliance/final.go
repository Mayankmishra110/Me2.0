// Final gates F1–F6 (COMPLIANCE §3) run after render, before approval.
package compliance

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mayank2/internal/queue"
)

const JobFinalCompliance = "compliance.final"

// AssetRef is one assets-table row (or equivalent) for final checks.
type AssetRef struct {
	Kind       string // voice|clip|render|thumb|subs|mdx
	Path       string
	LicenseURL string
}

// TechnicalInfo is probed/declared media properties for F4.
type TechnicalInfo struct {
	DurationSec      float64
	LoudnessLUFS     float64 // target −14; 0 means unset
	LoudnessSet      bool
	Width            int
	Height           int
	MaxBlackFrameSec float64
	CaptionsBurned   bool // Shorts/Reels burned-in captions
}

// FinalDisclosures is the F3 disclosure state on the item.
type FinalDisclosures struct {
	SyntheticDecided  bool // true once a decision was recorded (value may be false)
	Synthetic         bool
	HasAffiliateLinks bool
	AffiliatePresent  bool
	FinanceChannel    bool
	FinanceDisclaimer bool
}

// QuotaUsage is one quotas-table window for F6.
type QuotaUsage struct {
	Provider string
	Used     int
	Limit    int // 0 = unlimited / not configured
}

// Destination is a planned publish target for F5/F6 and scheduling.
type Destination struct {
	Platform string
	Account  string
}

// Caps is cadence limits for F5 (same shape as content.Caps).
type Caps struct {
	ShortsPerDay int
	LongPerWeek  int
}

// FinalItem is the input for F1–F6.
type FinalItem struct {
	ID           string
	ChannelID    string
	Kind         string // long|short|blog|post
	Language     string
	Assets       []AssetRef
	MusicPaths   []string // optional music files that need license sidecars
	Technical    TechnicalInfo
	Disclosures  FinalDisclosures
	Destinations []Destination
	Caps         Caps
	ShortsToday  int
	LongThisWeek int
	// PlatformPostsToday counts today's posts per platform account key "platform:account".
	PlatformPostsToday map[string]int
	Quotas             []QuotaUsage
	PreviewPath        string
	Summary            string
	Title              string
	Format             string
}

// FinalPayload is the compliance.final job body.
type FinalPayload struct {
	ContentID string    `json:"content_id"`
	Item      FinalItem `json:"item"`
}

// ApprovalStarter creates a pending approval and enqueues approval.request
// (implemented by content.ApprovalService; avoids importing content).
type ApprovalStarter interface {
	Start(ctx context.Context, contentID, kind, summary, previewPath string) (approvalID string, err error)
}

// FinalEngine runs F1–F6 and starts approval on pass.
type FinalEngine struct {
	Reports   ReportSaver
	Approvals ApprovalStarter
	Log       *slog.Logger
}

// NewFinalEngine returns a FinalEngine.
func NewFinalEngine(reports ReportSaver, approvals ApprovalStarter) *FinalEngine {
	return &FinalEngine{Reports: reports, Approvals: approvals, Log: slog.Default()}
}

// RegisterHandlers wires compliance.final (light: no local model load).
func (e *FinalEngine) RegisterHandlers(q *queue.Queue) {
	if e == nil || q == nil {
		return
	}
	q.Register(JobFinalCompliance, queue.ResourceLight, 3, e.handleJob)
}

func (e *FinalEngine) handleJob(ctx context.Context, job queue.Job) (json.RawMessage, error) {
	var p FinalPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return nil, queue.Permanent(fmt.Errorf("compliance.final payload: %w", err))
	}
	if p.ContentID == "" && job.ContentID != nil {
		p.ContentID = *job.ContentID
	}
	out, err := e.Handle(ctx, p)
	raw, _ := MarshalReport(out.Report)
	return raw, err
}

// FinalOutcome is the result of Handle.
type FinalOutcome struct {
	Report     Report
	ApprovalID string
}

// Handle runs F1–F6, saves the report, and on pass starts approval.
func (e *FinalEngine) Handle(ctx context.Context, p FinalPayload) (FinalOutcome, error) {
	item := p.Item
	if item.ID == "" {
		item.ID = p.ContentID
	}
	if item.ID == "" {
		return FinalOutcome{}, queue.Permanent(fmt.Errorf("compliance.final: content_id required"))
	}

	report := RunFinalGates(ctx, item)
	if e.Reports != nil {
		if err := e.Reports.SaveCompliance(ctx, item.ID, report); err != nil {
			return FinalOutcome{Report: report}, fmt.Errorf("compliance.final: save report: %w", err)
		}
	}
	if !report.Passed {
		return FinalOutcome{Report: report}, queue.Permanent(
			fmt.Errorf("compliance.final: gates failed: %s", gateFeedback(report)),
		)
	}
	if e.Approvals == nil {
		return FinalOutcome{Report: report}, queue.Permanent(fmt.Errorf("compliance.final: ApprovalStarter required on pass"))
	}
	kind := item.Kind
	if kind == "" {
		kind = "short"
	}
	summary := item.Summary
	if summary == "" {
		summary = buildApprovalSummary(item)
	}
	id, err := e.Approvals.Start(ctx, item.ID, kind, summary, item.PreviewPath)
	if err != nil {
		return FinalOutcome{Report: report}, fmt.Errorf("compliance.final: start approval: %w", err)
	}
	e.log().Info("compliance.final passed", "content_id", item.ID, "approval_id", id)
	return FinalOutcome{Report: report, ApprovalID: id}, nil
}

func (e *FinalEngine) log() *slog.Logger {
	if e != nil && e.Log != nil {
		return e.Log
	}
	return slog.Default()
}

func buildApprovalSummary(item FinalItem) string {
	title := item.Title
	if title == "" {
		title = item.ID
	}
	format := item.Format
	if format == "" {
		format = item.Kind
	}
	kind := item.Kind
	if kind != "" {
		kind = strings.ToUpper(kind[:1]) + kind[1:]
	}
	return fmt.Sprintf("🎬 %s · %s · %s\n%q", item.ChannelID, kind, format, title)
}

// RunFinalGates evaluates F1–F6 and returns a COMPLIANCE §5 report.
func RunFinalGates(ctx context.Context, item FinalItem) Report {
	type finalGate interface {
		ID() string
		check(FinalItem) GateResult
	}
	gates := []finalGate{F1{}, F2{}, F3{}, F4{}, F5{}, F6{}}
	results := make([]GateResult, 0, len(gates))
	passed := true
	for _, g := range gates {
		if err := ctx.Err(); err != nil {
			results = append(results, GateResult{ID: g.ID(), Passed: false, Detail: err.Error()})
			passed = false
			continue
		}
		r := g.check(item)
		if r.ID == "" {
			r.ID = g.ID()
		}
		results = append(results, r)
		if !r.Passed {
			passed = false
		}
	}
	return Report{
		Passed: passed,
		Gates:  results,
		Disclosures: Disclosures{
			Synthetic:         item.Disclosures.Synthetic,
			Affiliate:         item.Disclosures.AffiliatePresent,
			FinanceDisclaimer: item.Disclosures.FinanceDisclaimer,
		},
		CheckedAt: time.Now().UTC(),
	}
}

// --- F1 Assets licensed -------------------------------------------------------

// F1 checks every clip/music file has a license record (COMPLIANCE §3).
type F1 struct{}

func (F1) ID() string { return "F1" }

func (F1) Check(ctx context.Context, item ContentItem) GateResult {
	// Gate interface compliance; real path uses check(FinalItem).
	return GateResult{ID: "F1", Passed: false, Detail: "F1 requires FinalItem"}
}

func (F1) check(item FinalItem) GateResult {
	var missing []string
	for _, a := range item.Assets {
		if a.Kind != "clip" {
			continue
		}
		if licensedAsset(a) {
			continue
		}
		missing = append(missing, filepath.Base(a.Path))
	}
	for _, p := range item.MusicPaths {
		if licensedPath(p, "") {
			continue
		}
		missing = append(missing, filepath.Base(p))
	}
	if len(missing) > 0 {
		return GateResult{ID: "F1", Passed: false, Detail: "unlicensed assets: " + strings.Join(missing, ", ")}
	}
	clips := 0
	for _, a := range item.Assets {
		if a.Kind == "clip" {
			clips++
		}
	}
	if clips == 0 && len(item.MusicPaths) == 0 {
		// Blog/text items may have no clips; only fail when media kinds imply clips.
		if item.Kind == "long" || item.Kind == "short" {
			return GateResult{ID: "F1", Passed: false, Detail: "no clip assets to license-check"}
		}
	}
	return GateResult{ID: "F1", Passed: true, Detail: "all clips/music licensed"}
}

func licensedAsset(a AssetRef) bool {
	return licensedPath(a.Path, a.LicenseURL)
}

func licensedPath(path, licenseURL string) bool {
	if strings.TrimSpace(licenseURL) != "" {
		return true
	}
	if strings.TrimSpace(path) == "" {
		return false
	}
	sidecar := path + ".license.json"
	if _, err := os.Stat(sidecar); err == nil {
		return true
	}
	// stock.go writes base+".license.json" next to file (e.g. clip.mp4.license.json already covered;
	// also accept stem.license.json).
	ext := filepath.Ext(path)
	alt := strings.TrimSuffix(path, ext) + ".license.json"
	if _, err := os.Stat(alt); err == nil {
		return true
	}
	return false
}

// --- F2 Captions --------------------------------------------------------------

// F2 checks burned-in captions for shorts and .srt for long videos.
type F2 struct{}

func (F2) ID() string { return "F2" }

func (F2) Check(ctx context.Context, item ContentItem) GateResult {
	return GateResult{ID: "F2", Passed: false, Detail: "F2 requires FinalItem"}
}

func (F2) check(item FinalItem) GateResult {
	switch item.Kind {
	case "short":
		if !item.Technical.CaptionsBurned {
			return GateResult{ID: "F2", Passed: false, Detail: "shorts require burned-in captions"}
		}
		return GateResult{ID: "F2", Passed: true, Detail: "burned-in captions present"}
	case "long":
		for _, a := range item.Assets {
			if a.Kind == "subs" && strings.EqualFold(filepath.Ext(a.Path), ".srt") {
				if a.Path == "" {
					break
				}
				if _, err := os.Stat(a.Path); err != nil {
					return GateResult{ID: "F2", Passed: false, Detail: "srt missing on disk: " + filepath.Base(a.Path)}
				}
				return GateResult{ID: "F2", Passed: true, Detail: "srt present for long video"}
			}
		}
		return GateResult{ID: "F2", Passed: false, Detail: "long videos require .srt subs asset"}
	default:
		return GateResult{ID: "F2", Passed: true, Detail: "captions n/a for kind " + item.Kind}
	}
}

// --- F3 Disclosures -----------------------------------------------------------

// F3 checks synthetic/affiliate/finance disclosure flags.
type F3 struct{}

func (F3) ID() string { return "F3" }

func (F3) Check(ctx context.Context, item ContentItem) GateResult {
	return GateResult{ID: "F3", Passed: false, Detail: "F3 requires FinalItem"}
}

func (F3) check(item FinalItem) GateResult {
	d := item.Disclosures
	var fails []string
	if !d.SyntheticDecided {
		fails = append(fails, "synthetic-content flag not decided")
	}
	if d.HasAffiliateLinks && !d.AffiliatePresent {
		fails = append(fails, "affiliate disclosure required when links exist")
	}
	if d.FinanceChannel && !d.FinanceDisclaimer {
		fails = append(fails, "finance disclaimer required on finance channel")
	}
	if len(fails) > 0 {
		return GateResult{ID: "F3", Passed: false, Detail: strings.Join(fails, "; ")}
	}
	return GateResult{ID: "F3", Passed: true, Detail: "disclosures set"}
}

// --- F4 Technical -------------------------------------------------------------

// Platform duration caps (seconds) used by F4.
const (
	maxShortSec    = 60.0
	maxLongSec     = 12 * 60.0
	maxReelSec     = 90.0
	loudnessTarget = -14.0
	loudnessTol    = 1.5
)

// F4 checks duration, loudness −14 LUFS, resolution, black frames.
type F4 struct{}

func (F4) ID() string { return "F4" }

func (F4) Check(ctx context.Context, item ContentItem) GateResult {
	return GateResult{ID: "F4", Passed: false, Detail: "F4 requires FinalItem"}
}

func (F4) check(item FinalItem) GateResult {
	t := item.Technical
	var fails []string

	if item.Kind == "short" || item.Kind == "long" {
		if t.DurationSec <= 0 {
			fails = append(fails, "duration missing")
		} else {
			max := maxLongSec
			if item.Kind == "short" {
				max = maxShortSec
			}
			// Reels/Shorts shared render: allow up to maxReelSec for short.
			if item.Kind == "short" && t.DurationSec > maxReelSec {
				fails = append(fails, fmt.Sprintf("duration %.1fs exceeds short/reel max %.0fs", t.DurationSec, maxReelSec))
			} else if item.Kind == "long" && t.DurationSec > max {
				fails = append(fails, fmt.Sprintf("duration %.1fs exceeds long max %.0fs", t.DurationSec, max))
			}
		}
		if !t.LoudnessSet {
			fails = append(fails, "loudness not measured")
		} else if math.Abs(t.LoudnessLUFS-loudnessTarget) > loudnessTol {
			fails = append(fails, fmt.Sprintf("loudness %.1f LUFS (want %.0f±%.1f)", t.LoudnessLUFS, loudnessTarget, loudnessTol))
		}
		if t.Width <= 0 || t.Height <= 0 {
			fails = append(fails, "resolution missing")
		} else if item.Kind == "short" && t.Height < t.Width {
			fails = append(fails, fmt.Sprintf("short resolution %dx%d is not portrait", t.Width, t.Height))
		}
		if t.MaxBlackFrameSec > 1.0 {
			fails = append(fails, fmt.Sprintf("black frame run %.2fs > 1s", t.MaxBlackFrameSec))
		}
	}

	if len(fails) > 0 {
		return GateResult{ID: "F4", Passed: false, Detail: strings.Join(fails, "; ")}
	}
	return GateResult{ID: "F4", Passed: true, Score: scorePtr(t.DurationSec), Detail: "technical checks ok"}
}

// --- F5 Cadence ---------------------------------------------------------------

// Daily caps for non-YouTube platforms (COMPLIANCE §4).
var platformDailyCaps = map[string]int{
	"instagram":  3,
	"facebook":   3,
	"x":          5, // business default; personal uses account key
	"x_business": 5,
	"x_personal": 3,
	"pinterest":  5,
	"linkedin":   1,
	"youtube":    0, // channel Caps apply instead
}

// F5 checks channel ramp-up caps and X business/personal no-duplicate.
type F5 struct{}

func (F5) ID() string { return "F5" }

func (F5) Check(ctx context.Context, item ContentItem) GateResult {
	return GateResult{ID: "F5", Passed: false, Detail: "F5 requires FinalItem"}
}

func (F5) check(item FinalItem) GateResult {
	var fails []string

	hasXBiz, hasXPersonal := false, false
	for _, d := range item.Destinations {
		p := strings.ToLower(d.Platform)
		acc := strings.ToLower(d.Account)
		if p == "x" || p == "x_business" {
			if strings.Contains(acc, "personal") || p == "x_personal" {
				hasXPersonal = true
			} else {
				hasXBiz = true
			}
		}
		if p == "x_personal" {
			hasXPersonal = true
		}
	}
	if hasXBiz && hasXPersonal {
		fails = append(fails, "X business/personal must never duplicate the same item")
	}

	if item.Kind == "short" {
		if item.Caps.ShortsPerDay <= 0 {
			fails = append(fails, "channel short cap is 0 (ramp-up not started)")
		} else if item.ShortsToday >= item.Caps.ShortsPerDay {
			fails = append(fails, fmt.Sprintf("short daily cap reached (%d/%d)", item.ShortsToday, item.Caps.ShortsPerDay))
		}
	}
	if item.Kind == "long" {
		if item.Caps.LongPerWeek <= 0 {
			fails = append(fails, "channel long cap is 0 (ramp-up not started)")
		} else if item.LongThisWeek >= item.Caps.LongPerWeek {
			fails = append(fails, fmt.Sprintf("long weekly cap reached (%d/%d)", item.LongThisWeek, item.Caps.LongPerWeek))
		}
	}

	for _, d := range item.Destinations {
		p := strings.ToLower(d.Platform)
		cap, ok := platformDailyCaps[p]
		if !ok || cap <= 0 {
			continue
		}
		key := p + ":" + d.Account
		n := 0
		if item.PlatformPostsToday != nil {
			n = item.PlatformPostsToday[key]
		}
		if n >= cap {
			fails = append(fails, fmt.Sprintf("%s daily cap reached (%d/%d)", key, n, cap))
		}
	}

	if len(fails) > 0 {
		return GateResult{ID: "F5", Passed: false, Detail: strings.Join(fails, "; ")}
	}
	return GateResult{ID: "F5", Passed: true, Detail: "cadence within caps"}
}

// --- F6 Quota -----------------------------------------------------------------

// F6 checks platform API quota remains for the slot.
type F6 struct{}

func (F6) ID() string { return "F6" }

func (F6) Check(ctx context.Context, item ContentItem) GateResult {
	return GateResult{ID: "F6", Passed: false, Detail: "F6 requires FinalItem"}
}

func (F6) check(item FinalItem) GateResult {
	byProvider := map[string]QuotaUsage{}
	for _, q := range item.Quotas {
		byProvider[strings.ToLower(q.Provider)] = q
	}
	var fails []string
	seen := map[string]bool{}
	for _, d := range item.Destinations {
		provider := quotaProviderFor(d.Platform)
		if provider == "" || seen[provider] {
			continue
		}
		seen[provider] = true
		q, ok := byProvider[provider]
		if !ok || q.Limit <= 0 {
			// D24: missing quota row → treat as available (not configured yet).
			continue
		}
		if q.Used >= q.Limit {
			fails = append(fails, fmt.Sprintf("%s quota exhausted (%d/%d)", provider, q.Used, q.Limit))
		}
	}
	if len(fails) > 0 {
		return GateResult{ID: "F6", Passed: false, Detail: strings.Join(fails, "; ")}
	}
	return GateResult{ID: "F6", Passed: true, Detail: "quota available for destinations"}
}

func quotaProviderFor(platform string) string {
	switch strings.ToLower(platform) {
	case "youtube":
		return "youtube"
	case "instagram", "facebook":
		return "meta"
	case "x", "x_business", "x_personal":
		return "x"
	case "pinterest":
		return "pinterest"
	case "linkedin":
		return "linkedin"
	default:
		return strings.ToLower(platform)
	}
}

// LoadFinalAssets reads assets rows for a content item (helper for callers).
func LoadFinalAssets(ctx context.Context, db *sql.DB, contentID string) ([]AssetRef, error) {
	rows, err := db.QueryContext(ctx, `
SELECT kind, path, COALESCE(license_url,'') FROM assets WHERE content_id=?`, contentID)
	if err != nil {
		return nil, fmt.Errorf("compliance.final: load assets %s: %w", contentID, err)
	}
	defer rows.Close()
	var out []AssetRef
	for rows.Next() {
		var a AssetRef
		if err := rows.Scan(&a.Kind, &a.Path, &a.LicenseURL); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
