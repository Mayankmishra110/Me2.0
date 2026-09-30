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
	"path"
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

	// R2 uploads finished render/thumbnail assets to Cloudflare R2 (M2-123,
	// CONTEXT D20) right after each one is written locally, so the assets
	// row's r2_key is populated and internal/publish's
	// Instagram/Facebook/Pinterest publishers (which presign R2KeyResolver's
	// result, or fall back to VideoPath — see M2-122) have a real object to
	// point at. R2 is optional (D24, "run with only the keys you have"): a
	// nil R2 (no R2_* env keys configured) means every render still
	// completes and is fully usable locally (dashboard preview,
	// /media/{asset-id}, YouTube/X direct-upload publishers) — it just has
	// no r2_key, so an Instagram/Facebook/Pinterest publish attempt for that
	// asset fails cleanly at presign time instead of at render time. An R2
	// upload failure while R2 *is* configured (network blip, bad creds)
	// follows the same rule: it's logged and the render still succeeds
	// locally rather than failing the whole render job — a transient R2
	// outage must not block content from being produced and queued.
	R2 *storage.R2Client
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
	// Unset / zero means "use default"; RetentionDays: 0 must not expire immediately.
	if r != nil && r.RetentionDays > 0 {
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
	// R2Key is empty when the asset was never uploaded to R2 (R2 not
	// configured, this asset kind isn't uploaded, or the upload failed).
	R2Key string `json:"r2_key,omitempty"`
}

// uploadKinds are the asset kinds a publisher can presign a public URL for
// (Instagram/Facebook/Pinterest video via VideoPath, Pinterest cover image
// via ThumbnailPath — internal/publish). Voice audio and subtitle files are
// never presigned by any publisher, so they are never uploaded.
var uploadKinds = map[string]bool{
	assetKindRender: true,
	assetKindThumb:  true,
}

// r2ObjectKey builds the R2 key for a render/thumbnail asset. It always uses
// forward slashes (R2/S3 keys, not OS paths) and mirrors the local layout
// (Layout.Path(storage.KindRenders, contentID)/<filename>) so the key is
// predictable from content_id + filename alone.
func r2ObjectKey(contentID, filename string) string {
	return path.Join("renders", contentID, filename)
}

// r2ContentType is a minimal extension -> MIME map for the two kinds
// uploadKinds ever uploads; anything else is sent with no Content-Type.
func r2ContentType(filename string) string {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".mp4":
		return "video/mp4"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	default:
		return ""
	}
}

// uploadToR2 uploads localPath under its content-addressed key and returns
// the key. Callers treat a non-nil error as "log and continue" (see
// Renderer.R2's doc comment) — never as a reason to fail the render.
func (r *Renderer) uploadToR2(ctx context.Context, contentID, localPath string) (string, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("content render: r2 upload open %s: %w", localPath, err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("content render: r2 upload stat %s: %w", localPath, err)
	}
	filename := filepath.Base(localPath)
	key := r2ObjectKey(contentID, filename)
	if err := r.R2.Upload(ctx, key, f, st.Size(), r2ContentType(filename)); err != nil {
		return "", err
	}
	return key, nil
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
	focal, err = r.confineMediaPath(focal, "focal_clip_path")
	if err != nil {
		return nil, err
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

	voicePath, err := r.confineMediaPath(p.VoicePath, "voice_path")
	if err != nil {
		return nil, err
	}

	jobDir, err := r.jobDir(job.ID)
	if err != nil {
		return nil, err
	}
	defer media.RemoveStagedPublic(r.Tools.RemotionRoot, job.ID)

	relVoice, _, err := media.StagePublic(r.Tools.RemotionRoot, job.ID, voicePath, "voice"+filepath.Ext(voicePath))
	if err != nil {
		return nil, fmt.Errorf("content render stage voice: %w", err)
	}

	beats := make([]map[string]any, 0, len(p.Beats))
	for i, b := range p.Beats {
		if b.ClipPath == "" {
			return nil, queue.Permanent(fmt.Errorf("content render: beats[%d].clip_path required", i))
		}
		clipPath, err := r.confineMediaPath(b.ClipPath, fmt.Sprintf("beats[%d].clip_path", i))
		if err != nil {
			return nil, err
		}
		rel, _, err := media.StagePublic(r.Tools.RemotionRoot, job.ID, clipPath, fmt.Sprintf("beat-%d%s", i, filepath.Ext(clipPath)))
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
		p.Beats[i].ClipPath = clipPath
	}
	p.VoicePath = voicePath

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
	if err := r.Tools.MuxAV(ctx, rawMP4, voicePath, muxed); err != nil {
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
			if confined, cerr := r.confineMediaPath(focal, "focal_clip_path"); cerr != nil {
				r.log().Warn("content render: thumb skipped, path outside media root", "error", cerr)
			} else if relClip, _, err := media.StagePublic(r.Tools.RemotionRoot, job.ID, confined, "focal"+filepath.Ext(confined)); err == nil {
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

// confineMediaPath Abs+Cleans path and refuses anything outside Layout.Root()
// (data/media). Permanent — a crafted payload must not copy host files into
// remotion/public.
func (r *Renderer) confineMediaPath(path, field string) (string, error) {
	if r.Layout == nil {
		return "", fmt.Errorf("content render: Layout is required")
	}
	abs, err := media.ConfineUnderRoot(r.Layout.Root(), path)
	if err != nil {
		return "", queue.Permanent(fmt.Errorf("content render: %s: %w", field, err))
	}
	return abs, nil
}

func (r *Renderer) recordFile(ctx context.Context, contentID, kind, filePath string) (recordedAsset, error) {
	var zero recordedAsset
	sha, size, err := media.DigestFile(filePath)
	if err != nil {
		return zero, err
	}

	var r2Key string
	if r.R2 != nil && uploadKinds[kind] {
		key, uerr := r.uploadToR2(ctx, contentID, filePath)
		if uerr != nil {
			// R2 configured but this upload failed: local render already
			// succeeded and is already on disk, so this is a warning, not a
			// render failure (see Renderer.R2's doc comment / CONTEXT D24).
			// The asset row keeps r2_key NULL; a later publish attempt for
			// it fails cleanly at presign time instead of silently pointing
			// at an object that doesn't exist.
			r.log().Warn("content render: r2 upload failed, asset stays local-only", "content_id", contentID, "kind", kind, "path", filePath, "error", uerr)
		} else {
			r2Key = key
		}
	}

	id := r.newID()
	da := r.deleteAfter()
	var r2KeyArg any
	if r2Key != "" {
		r2KeyArg = r2Key
	}
	_, err = r.DB.ExecContext(ctx, `
INSERT INTO assets (id, content_id, kind, path, r2_key, sha256, bytes, delete_after)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, contentID, kind, filePath, r2KeyArg, sha, size, da,
	)
	if err != nil {
		return zero, fmt.Errorf("content render: insert asset %s: %w", kind, err)
	}
	return recordedAsset{ID: id, Kind: kind, Path: filePath, SHA256: sha, Bytes: size, R2Key: r2Key}, nil
}

func (r *Renderer) findAsset(ctx context.Context, contentID, kind string) (recordedAsset, bool, error) {
	var a recordedAsset
	var r2Key sql.NullString
	err := r.DB.QueryRowContext(ctx, `
SELECT id, kind, path, COALESCE(sha256, ''), COALESCE(bytes, 0), r2_key
FROM assets WHERE content_id=? AND kind=?
ORDER BY id DESC LIMIT 1`, contentID, kind).Scan(&a.ID, &a.Kind, &a.Path, &a.SHA256, &a.Bytes, &r2Key)
	if err == sql.ErrNoRows {
		return a, false, nil
	}
	if err != nil {
		return a, false, fmt.Errorf("content render: find asset %s/%s: %w", contentID, kind, err)
	}
	a.R2Key = r2Key.String
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
