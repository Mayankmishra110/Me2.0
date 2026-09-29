// Visuals stage (M2-117): the visuals.fetch queue handler, a thin adapter
// over Stock.Fetch (M2-207, stock.go) that downloads licensed B-roll per
// script beat and records each clip in the assets table (kind="clip") so
// render.long/render.short (internal/content/render.go) can stage it
// (ARCHITECTURE §3.1: voice.tts -> visuals.fetch -> render.long/short).
//
// Scope note: this handler does not itself enqueue render.long/render.short
// — joining voice.tts's output with visuals.fetch's output into one
// render.* payload (both must finish first) is an orchestration question
// ARCHITECTURE doesn't answer yet (no "join" job type exists in SPEC §5),
// so it is left to whatever calls research.brief/script.write's chain today
// (flagged in CONTEXT.md as a follow-up rather than guessed here).
package media

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"mayank2/internal/queue"
	"mayank2/internal/storage"
)

// JobVisualsFetch is the queue job type (SPEC §5).
const JobVisualsFetch = "visuals.fetch"

const assetKindClip = "clip"

// VisualsDB is the subset of *sql.DB the visuals.fetch handler needs.
type VisualsDB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// VisualsFetcher wires visuals.fetch onto the net resource class: it only
// makes Pexels/Pixabay HTTP calls, never loads a local model or renders, so
// CLAUDE.md's "heavy" resource rule does not apply (see cmd/mayank2/run.go
// registration comment).
type VisualsFetcher struct {
	DB     VisualsDB
	Stock  *Stock
	Layout *storage.Layout

	RetentionDays int
	Now           func() time.Time
	NewID         func() string
	Log           *slog.Logger
}

// RegisterHandler registers visuals.fetch on the net pool.
func (v *VisualsFetcher) RegisterHandler(q *queue.Queue) {
	if q == nil || v == nil {
		return
	}
	q.Register(JobVisualsFetch, queue.ResourceNet, 3, v.handle)
}

func (v *VisualsFetcher) log() *slog.Logger {
	if v != nil && v.Log != nil {
		return v.Log
	}
	return slog.Default()
}

func (v *VisualsFetcher) now() time.Time {
	if v != nil && v.Now != nil {
		return v.Now().UTC()
	}
	return time.Now().UTC()
}

func (v *VisualsFetcher) newID() string {
	if v != nil && v.NewID != nil {
		return v.NewID()
	}
	return newVisualsULID(time.Now)
}

func (v *VisualsFetcher) retention() int {
	if v != nil && v.RetentionDays > 0 {
		return v.RetentionDays
	}
	return 7
}

// VisualBeatRequest is one script beat to source a clip for.
type VisualBeatRequest struct {
	Text      string   `json:"text"`
	VisualCue string   `json:"visual_cue"`
	Keywords  []string `json:"keywords,omitempty"` // override; default derived from visual_cue/text
}

// VisualsFetchPayload is the visuals.fetch job body.
type VisualsFetchPayload struct {
	ContentID   string              `json:"content_id"`
	ChannelID   string              `json:"channel_id"`
	Orientation string              `json:"orientation,omitempty"` // landscape|portrait|square; default landscape
	Count       int                 `json:"count,omitempty"`       // clips per beat; default 1
	Beats       []VisualBeatRequest `json:"beats"`
}

// VisualBeatResult is one beat's fetched clip, shaped to drop straight into
// content.renderBeat's ClipPath/DurationInSeconds (internal/content/render.go).
type VisualBeatResult struct {
	ClipPath          string `json:"clip_path"`
	DurationInSeconds int    `json:"duration_in_seconds"`
	Provider          string `json:"provider"`
	LicenseURL        string `json:"license_url"`
}

// VisualsFetchResult is the visuals.fetch job result payload.
type VisualsFetchResult struct {
	ContentID string             `json:"content_id"`
	Beats     []VisualBeatResult `json:"beats"`
}

func (v *VisualsFetcher) handle(ctx context.Context, job queue.Job) (json.RawMessage, error) {
	var p VisualsFetchPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return nil, queue.Permanent(fmt.Errorf("media visuals.fetch payload: %w", err))
	}
	contentID := p.ContentID
	if contentID == "" && job.ContentID != nil {
		contentID = *job.ContentID
	}
	contentID = strings.TrimSpace(contentID)
	if contentID == "" {
		return nil, queue.Permanent(fmt.Errorf("media visuals.fetch: content_id required"))
	}
	channelID := strings.TrimSpace(p.ChannelID)
	if channelID == "" {
		return nil, queue.Permanent(fmt.Errorf("media visuals.fetch: channel_id required"))
	}
	if len(p.Beats) == 0 {
		return nil, queue.Permanent(fmt.Errorf("media visuals.fetch: beats required"))
	}
	if v.Stock == nil {
		return nil, queue.Permanent(fmt.Errorf("media visuals.fetch: no stock provider configured (PEXELS_API_KEY/PIXABAY_API_KEY unset — CONTEXT D24)"))
	}
	if v.Layout == nil {
		return nil, fmt.Errorf("media visuals.fetch: Layout is required")
	}

	orientation := Orientation(strings.TrimSpace(p.Orientation))
	if orientation == "" {
		orientation = OrientationLandscape
	}
	if !orientation.Valid() {
		return nil, queue.Permanent(fmt.Errorf("media visuals.fetch: orientation %q invalid", p.Orientation))
	}
	count := p.Count
	if count <= 0 {
		count = 1
	}

	clipsDir, err := v.Layout.Path(storage.KindRaw, contentID, "clips")
	if err != nil {
		return nil, fmt.Errorf("media visuals.fetch: clips dir: %w", err)
	}

	beats := make([]VisualBeatResult, 0, len(p.Beats))
	for i, beat := range p.Beats {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		keywords := beat.Keywords
		if len(keywords) == 0 {
			keywords = deriveKeywords(beat.VisualCue, beat.Text)
		}
		if len(keywords) == 0 {
			return nil, queue.Permanent(fmt.Errorf("media visuals.fetch: beats[%d] has no keywords/visual_cue/text", i))
		}
		assets, err := v.Stock.Fetch(ctx, FetchRequest{
			ChannelID:   channelID,
			JobDir:      clipsDir,
			Keywords:    keywords,
			Orientation: orientation,
			Count:       count,
		})
		if err != nil {
			return nil, fmt.Errorf("media visuals.fetch: beats[%d]: %w", i, err)
		}
		if len(assets) == 0 {
			return nil, fmt.Errorf("media visuals.fetch: beats[%d]: no clip found", i)
		}
		asset := assets[0]
		if v.DB != nil {
			var licenseURL any
			if asset.License.LicenseURL != "" {
				licenseURL = asset.License.LicenseURL
			}
			deleteAfter := storage.DeleteAfter(v.now(), v.retention()).UTC().Format(time.RFC3339Nano)
			if _, err := v.DB.ExecContext(ctx, `
INSERT INTO assets (id, content_id, kind, path, r2_key, license_url, sha256, bytes, delete_after)
VALUES (?, ?, ?, ?, NULL, ?, ?, ?, ?)`,
				v.newID(), contentID, assetKindClip, asset.Path, licenseURL, asset.SHA256, asset.Bytes, deleteAfter,
			); err != nil {
				return nil, fmt.Errorf("media visuals.fetch: record asset beats[%d]: %w", i, err)
			}
		}
		beats = append(beats, VisualBeatResult{
			ClipPath:          asset.Path,
			DurationInSeconds: asset.Seconds,
			Provider:          asset.License.Provider,
			LicenseURL:        asset.License.LicenseURL,
		})
	}

	v.log().Info("media: visuals.fetch done", "content_id", contentID, "beats", len(beats))
	return json.Marshal(VisualsFetchResult{ContentID: contentID, Beats: beats})
}

// deriveKeywords turns a beat's visual cue (preferred) or spoken text into a
// short keyword list for the stock search when the payload doesn't supply
// explicit Keywords.
func deriveKeywords(visualCue, text string) []string {
	src := strings.TrimSpace(visualCue)
	if src == "" {
		src = strings.TrimSpace(text)
	}
	if src == "" {
		return nil
	}
	fields := strings.Fields(src)
	if len(fields) > 6 {
		fields = fields[:6]
	}
	return fields
}

// newVisualsULID is a local Crockford ULID so this file does not need a new
// cross-package dependency for ids (mirrors render.go's newRenderULID /
// scout.go's newScoutULID in internal/content).
func newVisualsULID(now func() time.Time) string {
	const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	ms := now().UTC().UnixMilli()
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
