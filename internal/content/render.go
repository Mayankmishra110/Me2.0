// Render stage (M2-209): queue handlers that turn script + voice + clips into
// final files (ARCHITECTURE §3.1). All four job types run on the heavy
// resource class (16 GB RAM). Child processes go through internal/media.
package content

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mayank2/internal/media"
	"mayank2/internal/queue"
	"mayank2/internal/storage"
)

// Job type names (SPEC §5).
const (
	JobVoiceTTS        = "voice.tts"
	JobRenderLong      = "render.long"
	JobRenderShort     = "render.short"
	JobRenderThumbnail = "render.thumbnail"
)

const (
	assetKindVoice  = "voice"
	assetKindRender = "render"
	assetKindThumb  = "thumb"
	assetKindSubs   = "subs"

	defaultRetentionDays = 7
)

// crockford alphabet for ULID-ish asset ids (same charset as queue).
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// RenderDB is the subset of *sql.DB the render handlers need.
type RenderDB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Renderer wires heavy-class render jobs to media.Tools and the assets table.
type Renderer struct {
	DB            RenderDB
	Layout        *storage.Layout
	Tools         *media.Tools
	RetentionDays int
	Now           func() time.Time
	NewID         func() string
	Log           *slog.Logger
}

// RegisterHandlers registers voice.tts / render.* on the heavy pool.
func (r *Renderer) RegisterHandlers(q *queue.Queue) {
	if q == nil || r == nil {
		return
	}
	q.Register(JobVoiceTTS, queue.ResourceHeavy, 3, r.handleVoiceTTS)
	q.Register(JobRenderLong, queue.ResourceHeavy, 3, r.handleRenderLong)
	q.Register(JobRenderShort, queue.ResourceHeavy, 3, r.handleRenderShort)
	q.Register(JobRenderThumbnail, queue.ResourceHeavy, 3, r.handleRenderThumbnail)
}

func (r *Renderer) log() *slog.Logger {
	if r != nil && r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

func (r *Renderer) now() time.Time {
	if r != nil && r.Now != nil {
		return r.Now()
	}
	return time.Now().UTC()
}

func (r *Renderer) newID() string {
	if r != nil && r.NewID != nil {
		return r.NewID()
	}
	return newRenderULID(time.Now)
}

func (r *Renderer) retention() int {
	if r != nil && r.RetentionDays >= 0 {
		return r.RetentionDays
	}
	return defaultRetentionDays
}

func (r *Renderer) deleteAfter() string {
	return storage.DeleteAfter(r.now(), r.retention()).UTC().Format(time.RFC3339Nano)
}

// --- payloads ----------------------------------------------------------------

type voiceTTSPayload struct {
	ContentID string  `json:"content_id"`
	Text      string  `json:"text"`
	Lang      string  `json:"lang"`
	Voice     string  `json:"voice"`
	Speed     float64 `json:"speed"`
}

type renderBeat struct {
	Text              string  `json:"text"`
	ClipPath          string  `json:"clip_path"` // absolute local path to stage
	DurationInSeconds float64 `json:"duration_in_seconds"`
}

type renderPayload struct {
	ContentID string             `json:"content_id"`
	Format    string             `json:"format"`
	BrandKit  BrandKit           `json:"brand_kit"`
	Beats     []renderBeat       `json:"beats"`
	VoicePath string             `json:"voice_path"`
	Words     []media.WordTiming `json:"words"`
	ThumbText string             `json:"thumb_text"`
	// FocalClipPath is optional for thumbnail (absolute); defaults to first beat clip.
	FocalClipPath string `json:"focal_clip_path"`
}

type recordedAsset struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

// --- handlers ----------------------------------------------------------------

func (r *Renderer) handleVoiceTTS(ctx context.Context, job queue.Job) (json.RawMessage, error) {
	var p voiceTTSPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return nil, queue.Permanent(fmt.Errorf("content voice.tts payload: %w", err))
	}
	contentID := firstNonEmpty(p.ContentID, deref(job.ContentID))
	if contentID == "" {
		return nil, queue.Permanent(fmt.Errorf("content voice.tts: content_id required"))
	}
	if strings.TrimSpace(p.Text) == "" {
		return nil, queue.Permanent(fmt.Errorf("content voice.tts: text required"))
	}
	lang := strings.TrimSpace(p.Lang)
	if lang == "" {
		lang = "en"
	}

	outDir, err := r.renderDir(contentID)
	if err != nil {
		return nil, err
	}
	wavPath := filepath.Join(outDir, "voice.wav")

	if existing, ok, err := r.findAsset(ctx, contentID, assetKindVoice); err != nil {
		return nil, err
	} else if ok && fileExists(existing.Path) {
		return json.Marshal(map[string]any{"skipped": "already_rendered", "asset": existing, "words": []media.WordTiming{}})
	}

	jobDir, err := r.jobDir(job.ID)
	if err != nil {
		return nil, err
	}
	workWAV := filepath.Join(jobDir, "voice.wav")
	res, err := r.Tools.TTS(ctx, media.TTSRequest{
		Text:   p.Text,
		Lang:   lang,
		Voice:  p.Voice,
		Speed:  p.Speed,
		OutWAV: workWAV,
	})
	if err != nil {
		return nil, fmt.Errorf("content voice.tts: %w", err)
	}
	if err := copyFileSimple(res.WAVPath, wavPath); err != nil {
		return nil, err
	}
	asset, err := r.recordFile(ctx, contentID, assetKindVoice, wavPath)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"asset":   asset,
		"words":   res.Words,
		"wav":     wavPath,
		"voice":   res.Voice,
		"lang":    res.Lang,
		"seconds": res.DurationSec,
	})
}

func (r *Renderer) handleRenderLong(ctx context.Context, job queue.Job) (json.RawMessage, error) {
	return r.renderVideo(ctx, job, "16:9", "long.mp4", true)
}

func (r *Renderer) handleRenderShort(ctx context.Context, job queue.Job) (json.RawMessage, error) {
	return r.renderVideo(ctx, job, "9:16", "short.mp4", false)
}

func (r *Renderer) handleRenderThumbnail(ctx context.Context, job queue.Job) (json.RawMessage, error) {
	var p renderPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return nil, queue.Permanent(fmt.Errorf("content render.thumbnail payload: %w", err))
	}
	contentID := firstNonEmpty(p.ContentID, deref(job.ContentID))
	if contentID == "" {
		return nil, queue.Permanent(fmt.Errorf("content render.thumbnail: content_id required"))
	}
	if strings.TrimSpace(p.ThumbText) == "" {
		return nil, queue.Permanent(fmt.Errorf("content render.thumbnail: thumb_text required"))
	}

	outDir, err := r.renderDir(contentID)
	if err != nil {
		return nil, err
	}
	thumbPath := filepath.Join(outDir, "thumb.png")
	if existing, ok, err := r.findAsset(ctx, contentID, assetKindThumb); err != nil {
		return nil, err
	} else if ok && fileExists(existing.Path) {
		return json.Marshal(map[string]any{"skipped": "already_rendered", "asset": existing})
	}

	focal := p.FocalClipPath
	if focal == "" && len(p.Beats) > 0 {
		focal = p.Beats[0].ClipPath
	}
	if focal == "" {
		return nil, queue.Permanent(fmt.Errorf("content render.thumbnail: focal_clip_path or beats[0].clip_path required"))
	}

	jobDir, err := r.jobDir(job.ID)
	if err != nil {
		return nil, err
	}
	defer media.RemoveStagedPublic(r.Tools.RemotionRoot, job.ID)

	relClip, _, err := media.StagePublic(r.Tools.RemotionRoot, job.ID, focal, "focal"+filepath.Ext(focal))
	if err != nil {
		return nil, fmt.Errorf("content render.thumbnail stage: %w", err)
	}

	props := map[string]any{
		"brandKit":      p.BrandKit,
		"title":         p.ThumbText,
		"focalClipPath": relClip,
		"orientation":   "16:9",
	}
	propsPath := filepath.Join(jobDir, "thumb-props.json")
	if err := writeJSON(propsPath, props); err != nil {
		return nil, err
	}
	rawStill := filepath.Join(jobDir, "thumb-raw.png")
	if err := r.Tools.RemotionRender(ctx, "thumbnail", propsPath, rawStill, true); err != nil {
		return nil, fmt.Errorf("content render.thumbnail: %w", err)
	}
	if err := copyFileSimple(rawStill, thumbPath); err != nil {
		return nil, err
	}
	asset, err := r.recordFile(ctx, contentID, assetKindThumb, thumbPath)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"asset": asset, "path": thumbPath})
}

func (r *Renderer) renderVideo(ctx context.Context, job queue.Job, orientation, outName string, withThumb bool) (json.RawMessage, error) {
	var p renderPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return nil, queue.Permanent(fmt.Errorf("content render payload: %w", err))
	}
	contentID := firstNonEmpty(p.ContentID, deref(job.ContentID))
	if contentID == "" {
		return nil, queue.Permanent(fmt.Errorf("content render: content_id required"))
	}
	if strings.TrimSpace(p.Format) == "" {
		return nil, queue.Permanent(fmt.Errorf("content render: format required"))
	}
	if len(p.Beats) == 0 {
		return nil, queue.Permanent(fmt.Errorf("content render: beats required"))
	}
	if strings.TrimSpace(p.VoicePath) == "" {
		return nil, queue.Permanent(fmt.Errorf("content render: voice_path required"))
	}

	compID, err := media.CompositionID(p.Format, orientation)
	if err != nil {
		return nil, queue.Permanent(fmt.Errorf("content render: %w", err))
	}

	outDir, err := r.renderDir(contentID)
	if err != nil {
		return nil, err
	}
	finalPath := filepath.Join(outDir, outName)
	if existing, ok, err := r.findAsset(ctx, contentID, assetKindRender); err != nil {
		return nil, err
	} else if ok && fileExists(existing.Path) && filepath.Base(existing.Path) == outName {
		return json.Marshal(map[string]any{"skipped": "already_rendered", "asset": existing})
	}

	jobDir, err := r.jobDir(job.ID)
	if err != nil {
		return nil, err
	}
	defer media.RemoveStagedPublic(r.Tools.RemotionRoot, job.ID)

	relVoice, _, err := media.StagePublic(r.Tools.RemotionRoot, job.ID, p.VoicePath, "voice"+filepath.Ext(p.VoicePath))
	if err != nil {
		return nil, fmt.Errorf("content render stage voice: %w", err)
	}

	beats := make([]map[string]any, 0, len(p.Beats))
	for i, b := range p.Beats {
		if b.ClipPath == "" {
			return nil, queue.Permanent(fmt.Errorf("content render: beats[%d].clip_path required", i))
		}
		rel, _, err := media.StagePublic(r.Tools.RemotionRoot, job.ID, b.ClipPath, fmt.Sprintf("beat-%d%s", i, filepath.Ext(b.ClipPath)))
		if err != nil {
			return nil, fmt.Errorf("content render stage beat %d: %w", i, err)
		}
		dur := b.DurationInSeconds
		if dur <= 0 {
			dur = 2
		}
		beats = append(beats, map[string]any{
			"text":              b.Text,
			"clipPath":          rel,
			"durationInSeconds": dur,
		})
	}

	props := map[string]any{
		"brandKit":    p.BrandKit,
		"beats":       beats,
		"audioSrc":    relVoice,
		"captions":    map[string]any{"words": media.WordsToRemotion(p.Words)},
		"orientation": orientation,
	}
	propsPath := filepath.Join(jobDir, "props.json")
	if err := writeJSON(propsPath, props); err != nil {
		return nil, err
	}

	rawMP4 := filepath.Join(jobDir, "raw.mp4")
	if err := r.Tools.RemotionRender(ctx, compID, propsPath, rawMP4, false); err != nil {
		return nil, fmt.Errorf("content render remotion: %w", err)
	}

	// Optional remux if Remotion output has no usable audio: mux voice over video,
	// then loudnorm. When Remotion already baked audio, loudnorm alone is enough;
	// MuxAV is still available for callers that pass a silent plate.
	muxed := filepath.Join(jobDir, "muxed.mp4")
	if err := r.Tools.MuxAV(ctx, rawMP4, p.VoicePath, muxed); err != nil {
		// Fall back to raw remotion output (already has Audio track).
		r.log().Warn("content render: mux skipped, using remotion output", "error", err)
		muxed = rawMP4
	}

	normed := filepath.Join(jobDir, "loud.mp4")
	if err := r.Tools.NormalizeLoudness(ctx, muxed, normed); err != nil {
		return nil, fmt.Errorf("content render loudnorm: %w", err)
	}
	if err := copyFileSimple(normed, finalPath); err != nil {
		return nil, err
	}
	renderAsset, err := r.recordFile(ctx, contentID, assetKindRender, finalPath)
	if err != nil {
		return nil, err
	}

	result := map[string]any{
		"render":      renderAsset,
		"composition": compID,
		"orientation": orientation,
		"path":        finalPath,
	}

	srtPath := filepath.Join(outDir, "subs.srt")
	if err := media.WriteSRT(p.Words, srtPath); err != nil {
		return nil, fmt.Errorf("content render srt: %w", err)
	}
	subsAsset, err := r.recordFile(ctx, contentID, assetKindSubs, srtPath)
	if err != nil {
		return nil, err
	}
	result["subs"] = subsAsset

	if withThumb {
		thumbText := p.ThumbText
		if thumbText == "" && len(p.Beats) > 0 {
			thumbText = p.Beats[0].Text
		}
		if thumbText != "" {
			focal := p.FocalClipPath
			if focal == "" {
				focal = p.Beats[0].ClipPath
			}
			relClip, _, err := media.StagePublic(r.Tools.RemotionRoot, job.ID, focal, "focal"+filepath.Ext(focal))
			if err == nil {
				tprops := map[string]any{
					"brandKit":      p.BrandKit,
					"title":         thumbText,
					"focalClipPath": relClip,
					"orientation":   "16:9",
				}
				tpropsPath := filepath.Join(jobDir, "thumb-props.json")
				rawStill := filepath.Join(jobDir, "thumb-raw.png")
				thumbPath := filepath.Join(outDir, "thumb.png")
				if err := writeJSON(tpropsPath, tprops); err == nil {
					if err := r.Tools.RemotionRender(ctx, "thumbnail", tpropsPath, rawStill, true); err == nil {
						if err := copyFileSimple(rawStill, thumbPath); err == nil {
							if a, err := r.recordFile(ctx, contentID, assetKindThumb, thumbPath); err == nil {
								result["thumb"] = a
							}
						}
					}
				}
			}
		}
	}

	return json.Marshal(result)
}

// --- assets / paths ----------------------------------------------------------

func (r *Renderer) jobDir(jobID string) (string, error) {
	if r.Layout == nil {
		return "", fmt.Errorf("content render: Layout is required")
	}
	dir, err := r.Layout.JobDir(jobID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("content render: mkdir job dir %s: %w", dir, err)
	}
	return dir, nil
}

func (r *Renderer) renderDir(contentID string) (string, error) {
	if r.Layout == nil {
		return "", fmt.Errorf("content render: Layout is required")
	}
	if contentID != filepath.Base(contentID) || strings.Contains(contentID, "..") {
		return "", queue.Permanent(fmt.Errorf("content render: unsafe content_id %q", contentID))
	}
	dir, err := r.Layout.Path(storage.KindRenders, contentID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("content render: mkdir renders %s: %w", dir, err)
	}
	return dir, nil
}

func (r *Renderer) recordFile(ctx context.Context, contentID, kind, path string) (recordedAsset, error) {
	var zero recordedAsset
	sha, size, err := media.DigestFile(path)
	if err != nil {
		return zero, err
	}
	id := r.newID()
	da := r.deleteAfter()
	_, err = r.DB.ExecContext(ctx, `
INSERT INTO assets (id, content_id, kind, path, sha256, bytes, delete_after)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, contentID, kind, path, sha, size, da,
	)
	if err != nil {
		return zero, fmt.Errorf("content render: insert asset %s: %w", kind, err)
	}
	return recordedAsset{ID: id, Kind: kind, Path: path, SHA256: sha, Bytes: size}, nil
}

func (r *Renderer) findAsset(ctx context.Context, contentID, kind string) (recordedAsset, bool, error) {
	var a recordedAsset
	err := r.DB.QueryRowContext(ctx, `
SELECT id, kind, path, COALESCE(sha256, ''), COALESCE(bytes, 0)
FROM assets WHERE content_id=? AND kind=?
ORDER BY id DESC LIMIT 1`, contentID, kind).Scan(&a.ID, &a.Kind, &a.Path, &a.SHA256, &a.Bytes)
	if err == sql.ErrNoRows {
		return a, false, nil
	}
	if err != nil {
		return a, false, fmt.Errorf("content render: find asset %s/%s: %w", contentID, kind, err)
	}
	return a, true, nil
}

func writeJSON(path string, v any) error {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("content render: marshal json: %w", err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fmt.Errorf("content render: write %s: %w", path, err)
	}
	return nil
}

func copyFileSimple(src, dst string) error {
	in, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("content render: read %s: %w", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("content render: mkdir for %s: %w", dst, err)
	}
	if err := os.WriteFile(dst, in, 0o644); err != nil {
		return fmt.Errorf("content render: write %s: %w", dst, err)
	}
	return nil
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func newRenderULID(now func() time.Time) string {
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
