// Render helpers (M2-209): TTS, Remotion, ffmpeg mux + loudness −14 LUFS, and
// .srt from word timings for the faceless video pipeline (ARCHITECTURE §3.1).
// Every external tool uses a fixed argument list and timeout (§2 / §7).
package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Loudness target for YouTube / COMPLIANCE F4 (ARCHITECTURE §3.1).
const LoudnessLUFS = -14.0

// Defaults for render child processes.
const (
	DefaultRenderTimeout = 30 * time.Minute
	DefaultTTSTimeout    = 15 * time.Minute
)

// WordTiming is one spoken word with times in seconds (media-tools TTS contract).
type WordTiming struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// RemotionWordTiming is the Remotion captions shape (milliseconds).
type RemotionWordTiming struct {
	Word    string `json:"word"`
	StartMs int    `json:"startMs"`
	EndMs   int    `json:"endMs"`
}

// ExecRunner runs an external binary. Tests inject a fake; production uses
// exec.CommandContext. name and args are never passed through a shell.
type ExecRunner func(ctx context.Context, name string, args []string, dir string, env []string) (stdout, stderr []byte, err error)

// DefaultExecRunner is the production runner (fixed argv, no shell).
func DefaultExecRunner(ctx context.Context, name string, args []string, dir string, env []string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// Tools wraps ffmpeg, media-tools (uv), and Remotion CLI invocations.
type Tools struct {
	Run           ExecRunner
	FFmpeg        string // default "ffmpeg"
	UV            string // default "uv"
	Node          string // default "node"
	RemotionRoot  string // absolute path to remotion/
	MediaToolsDir string // absolute path to media-tools/
	Timeout       time.Duration
	Logger        *slog.Logger
}

// NewTools builds Tools with production defaults. remotionRoot and mediaToolsDir
// may be empty until the caller sets them (handlers fail clearly if missing).
func NewTools(remotionRoot, mediaToolsDir string) *Tools {
	return &Tools{
		Run:           DefaultExecRunner,
		FFmpeg:        "ffmpeg",
		UV:            "uv",
		Node:          "node",
		RemotionRoot:  remotionRoot,
		MediaToolsDir: mediaToolsDir,
		Timeout:       DefaultRenderTimeout,
		Logger:        slog.Default(),
	}
}

func (t *Tools) log() *slog.Logger {
	if t == nil || t.Logger == nil {
		return slog.Default()
	}
	return t.Logger
}

func (t *Tools) run() ExecRunner {
	if t != nil && t.Run != nil {
		return t.Run
	}
	return DefaultExecRunner
}

func (t *Tools) timeout() time.Duration {
	if t != nil && t.Timeout > 0 {
		return t.Timeout
	}
	return DefaultRenderTimeout
}

func (t *Tools) ffmpegBin() string {
	if t != nil && t.FFmpeg != "" {
		return t.FFmpeg
	}
	return "ffmpeg"
}

func (t *Tools) uvBin() string {
	if t != nil && t.UV != "" {
		return t.UV
	}
	return "uv"
}

func (t *Tools) nodeBin() string {
	if t != nil && t.Node != "" {
		return t.Node
	}
	return "node"
}

// TTSRequest is the media-tools tts input (ARCHITECTURE §2).
type TTSRequest struct {
	Text   string  `json:"text"`
	Lang   string  `json:"lang"`
	Voice  string  `json:"voice,omitempty"`
	Speed  float64 `json:"speed,omitempty"`
	OutWAV string  `json:"out_wav"`
}

// TTSResult is the media-tools tts output.
type TTSResult struct {
	WAVPath     string       `json:"wav_path"`
	SampleRate  int          `json:"sample_rate"`
	DurationSec float64      `json:"duration_sec"`
	Lang        string       `json:"lang"`
	Voice       string       `json:"voice"`
	Words       []WordTiming `json:"words"`
}

// TTS runs `uv run mediatools tts --in … --out …` in MediaToolsDir.
func (t *Tools) TTS(ctx context.Context, req TTSRequest) (TTSResult, error) {
	var zero TTSResult
	if strings.TrimSpace(req.Text) == "" {
		return zero, errors.New("media tts: text is required")
	}
	if strings.TrimSpace(req.OutWAV) == "" {
		return zero, errors.New("media tts: out_wav is required")
	}
	if strings.TrimSpace(t.MediaToolsDir) == "" {
		return zero, errors.New("media tts: MediaToolsDir is required")
	}
	if err := os.MkdirAll(filepath.Dir(req.OutWAV), 0o755); err != nil {
		return zero, fmt.Errorf("media tts: mkdir for %s: %w", req.OutWAV, err)
	}

	jobDir := filepath.Dir(req.OutWAV)
	inPath := filepath.Join(jobDir, "tts-input.json")
	outPath := filepath.Join(jobDir, "tts-output.json")
	body, err := json.Marshal(req)
	if err != nil {
		return zero, fmt.Errorf("media tts: marshal input: %w", err)
	}
	if err := os.WriteFile(inPath, body, 0o644); err != nil {
		return zero, fmt.Errorf("media tts: write input %s: %w", inPath, err)
	}

	cctx, cancel := context.WithTimeout(ctx, t.timeout())
	defer cancel()
	args := []string{"run", "mediatools", "tts", "--in", inPath, "--out", outPath}
	stdout, stderr, err := t.run()(cctx, t.uvBin(), args, t.MediaToolsDir, nil)
	if err != nil {
		return zero, fmt.Errorf("media tts: uv run mediatools: %w (%s)", err, truncateBytes(stderr, 400))
	}
	_ = stdout

	raw, err := os.ReadFile(outPath)
	if err != nil {
		return zero, fmt.Errorf("media tts: read output %s: %w", outPath, err)
	}
	var res TTSResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return zero, fmt.Errorf("media tts: decode output: %w", err)
	}
	if res.WAVPath == "" {
		res.WAVPath = req.OutWAV
	}
	t.log().Info("media tts ok", "wav", res.WAVPath, "words", len(res.Words), "duration_sec", res.DurationSec)
	return res, nil
}

// RemotionProps is the JSON props file passed to the Remotion CLI.
type RemotionProps map[string]any

// RemotionRender renders a composition to an mp4 (or still image for thumbnail).
// composition is a Remotion id (e.g. explained-60s-landscape). still=true uses
// `still` instead of `render`.
func (t *Tools) RemotionRender(ctx context.Context, composition, propsPath, outPath string, still bool) error {
	if strings.TrimSpace(t.RemotionRoot) == "" {
		return errors.New("media remotion: RemotionRoot is required")
	}
	if composition == "" || propsPath == "" || outPath == "" {
		return errors.New("media remotion: composition, propsPath, and outPath are required")
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("media remotion: mkdir for %s: %w", outPath, err)
	}

	cli := filepath.Join(t.RemotionRoot, "node_modules", "@remotion", "cli", "remotion-cli.js")
	entry := filepath.Join("src", "index.ts")
	var args []string
	if still {
		args = []string{cli, "still", entry, composition, outPath, "--props=" + propsPath}
	} else {
		args = []string{cli, "render", entry, composition, outPath, "--props=" + propsPath}
	}

	cctx, cancel := context.WithTimeout(ctx, t.timeout())
	defer cancel()
	stdout, stderr, err := t.run()(cctx, t.nodeBin(), args, t.RemotionRoot, nil)
	if err != nil {
		return fmt.Errorf("media remotion %s: %w (%s)", composition, err, truncateBytes(stderr, 400))
	}
	_ = stdout
	if _, err := os.Stat(outPath); err != nil {
		return fmt.Errorf("media remotion %s: output missing %s: %w", composition, outPath, err)
	}
	t.log().Info("media remotion ok", "composition", composition, "out", outPath, "still", still)
	return nil
}

// MuxAV muxes a video stream with an audio stream into outPath (ffmpeg).
// Video is taken from videoPath; audio from audioPath. Video is stream-copied;
// audio is encoded AAC.
func (t *Tools) MuxAV(ctx context.Context, videoPath, audioPath, outPath string) error {
	if videoPath == "" || audioPath == "" || outPath == "" {
		return errors.New("media mux: video, audio, and out paths are required")
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("media mux: mkdir for %s: %w", outPath, err)
	}
	args := []string{
		"-y",
		"-i", videoPath,
		"-i", audioPath,
		"-map", "0:v:0",
		"-map", "1:a:0",
		"-c:v", "copy",
		"-c:a", "aac",
		"-shortest",
		outPath,
	}
	cctx, cancel := context.WithTimeout(ctx, t.timeout())
	defer cancel()
	_, stderr, err := t.run()(cctx, t.ffmpegBin(), args, "", nil)
	if err != nil {
		return fmt.Errorf("media mux: ffmpeg: %w (%s)", err, truncateBytes(stderr, 400))
	}
	if _, err := os.Stat(outPath); err != nil {
		return fmt.Errorf("media mux: output missing %s: %w", outPath, err)
	}
	return nil
}

// LoudnormArgs returns the fixed ffmpeg argv for −14 LUFS loudness normalization
// (COMPLIANCE F4). Exposed for tests so we can assert the filter without a binary.
func LoudnormArgs(inPath, outPath string) []string {
	filter := fmt.Sprintf("loudnorm=I=%.0f:TP=-1.5:LRA=11", LoudnessLUFS)
	return []string{
		"-y",
		"-i", inPath,
		"-af", filter,
		"-c:v", "copy",
		"-c:a", "aac",
		outPath,
	}
}

// NormalizeLoudness rewrites inPath to outPath at −14 LUFS via ffmpeg loudnorm.
func (t *Tools) NormalizeLoudness(ctx context.Context, inPath, outPath string) error {
	if inPath == "" || outPath == "" {
		return errors.New("media loudnorm: in and out paths are required")
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("media loudnorm: mkdir for %s: %w", outPath, err)
	}
	args := LoudnormArgs(inPath, outPath)
	cctx, cancel := context.WithTimeout(ctx, t.timeout())
	defer cancel()
	_, stderr, err := t.run()(cctx, t.ffmpegBin(), args, "", nil)
	if err != nil {
		return fmt.Errorf("media loudnorm: ffmpeg: %w (%s)", err, truncateBytes(stderr, 400))
	}
	if _, err := os.Stat(outPath); err != nil {
		return fmt.Errorf("media loudnorm: output missing %s: %w", outPath, err)
	}
	return nil
}

// CompositionID maps a content format id + orientation to a Remotion composition
// id (M2-208: underscores become hyphens; Remotion forbids `_` in ids).
// orientation is "16:9" (landscape) or "9:16" (portrait).
func CompositionID(format, orientation string) (string, error) {
	base := strings.ReplaceAll(strings.TrimSpace(format), "_", "-")
	if base == "" {
		return "", errors.New("media: empty format")
	}
	switch orientation {
	case "16:9":
		return base + "-landscape", nil
	case "9:16":
		return base + "-portrait", nil
	default:
		return "", fmt.Errorf("media: orientation %q: want 16:9 or 9:16", orientation)
	}
}

// WordsToRemotion converts media-tools second timings to Remotion millisecond captions.
func WordsToRemotion(words []WordTiming) []RemotionWordTiming {
	out := make([]RemotionWordTiming, 0, len(words))
	for _, w := range words {
		out = append(out, RemotionWordTiming{
			Word:    w.Word,
			StartMs: int(w.Start * 1000),
			EndMs:   int(w.End * 1000),
		})
	}
	return out
}

// WriteSRT writes a SubRip (.srt) file from word timings. Words are grouped into
// cues of up to ~3 s or ~42 characters, broken on gaps ≥ 0.4 s.
func WriteSRT(words []WordTiming, path string) error {
	if path == "" {
		return errors.New("media srt: path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("media srt: mkdir for %s: %w", path, err)
	}
	cues := groupWordsForSRT(words)
	var b strings.Builder
	for i, c := range cues {
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1, srtStamp(c.start), srtStamp(c.end), c.text)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("media srt: write %s: %w", path, err)
	}
	return nil
}

type srtCue struct {
	start, end float64
	text       string
}

func groupWordsForSRT(words []WordTiming) []srtCue {
	const maxGap = 0.4
	const maxDur = 3.0
	const maxChars = 42

	var cues []srtCue
	var cur []WordTiming
	flush := func() {
		if len(cur) == 0 {
			return
		}
		parts := make([]string, 0, len(cur))
		for _, w := range cur {
			parts = append(parts, w.Word)
		}
		cues = append(cues, srtCue{
			start: cur[0].Start,
			end:   cur[len(cur)-1].End,
			text:  strings.Join(parts, " "),
		})
		cur = nil
	}
	for _, w := range words {
		if w.Word == "" {
			continue
		}
		if len(cur) == 0 {
			cur = append(cur, w)
			continue
		}
		prev := cur[len(cur)-1]
		gap := w.Start - prev.End
		dur := w.End - cur[0].Start
		chars := 0
		for _, x := range cur {
			chars += len(x.Word) + 1
		}
		chars += len(w.Word)
		if gap >= maxGap || dur > maxDur || chars > maxChars {
			flush()
		}
		cur = append(cur, w)
	}
	flush()
	return cues
}

func srtStamp(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	ms := int(sec*1000 + 0.5)
	h := ms / 3_600_000
	ms %= 3_600_000
	m := ms / 60_000
	ms %= 60_000
	s := ms / 1000
	ms %= 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}

// DigestFile returns the sha256 hex digest and size of path.
func DigestFile(path string) (sha string, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("media digest: open %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, fmt.Errorf("media digest: read %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// ConfineUnderRoot returns the absolute cleaned form of path if it lies under
// root (media data root). Relative paths, "..", and absolute paths outside
// root are refused. Comparison is case-insensitive on Windows (NTFS).
func ConfineUnderRoot(root, path string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("media: storage root is required")
	}
	if strings.TrimSpace(path) == "" {
		return "", errors.New("media: path is required")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("media: resolve storage root %q: %w", root, err)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("media: resolve path %q: %w", path, err)
	}
	absRoot = filepath.Clean(absRoot)
	absPath = filepath.Clean(absPath)
	if resolved, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = resolved
	}
	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		absPath = resolved
	}
	if !pathWithinRoot(absRoot, absPath) {
		return "", fmt.Errorf("media: path %q escapes storage root", path)
	}
	return absPath, nil
}

// pathWithinRoot reports whether path is root or a child of root. Both args
// must already be filepath.Clean absolute paths.
func pathWithinRoot(root, path string) bool {
	r, p := root, path
	if isCaseInsensitiveFS {
		r = strings.ToLower(r)
		p = strings.ToLower(p)
	}
	if r == p {
		return true
	}
	sep := string(filepath.Separator)
	return strings.HasPrefix(p, r+sep)
}

// isCaseInsensitiveFS is true on the only target platform (Windows).
const isCaseInsensitiveFS = true

// StagePublic copies src into remotionRoot/public/jobs/<jobID>/<name> and
// returns the path relative to public/ (for Remotion staticFile props).
func StagePublic(remotionRoot, jobID, src, name string) (rel string, abs string, err error) {
	if remotionRoot == "" || jobID == "" || src == "" || name == "" {
		return "", "", errors.New("media stage: remotionRoot, jobID, src, and name are required")
	}
	if jobID != filepath.Base(jobID) || strings.Contains(jobID, "..") {
		return "", "", fmt.Errorf("media stage: unsafe job id %q", jobID)
	}
	name = filepath.Base(name)
	destDir := filepath.Join(remotionRoot, "public", "jobs", jobID)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", "", fmt.Errorf("media stage: mkdir %s: %w", destDir, err)
	}
	dest := filepath.Join(destDir, name)
	if err := copyFile(src, dest); err != nil {
		return "", "", err
	}
	rel = filepath.ToSlash(filepath.Join("jobs", jobID, name))
	return rel, dest, nil
}

// RemoveStagedPublic deletes remotionRoot/public/jobs/<jobID> (best-effort cleanup).
func RemoveStagedPublic(remotionRoot, jobID string) error {
	if remotionRoot == "" || jobID == "" || jobID != filepath.Base(jobID) {
		return nil
	}
	dir := filepath.Join(remotionRoot, "public", "jobs", jobID)
	return os.RemoveAll(dir)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("media copy: open %s: %w", src, err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("media copy: create %s: %w", dst, err)
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("media copy: %s → %s: %w", src, dst, err)
	}
	return out.Close()
}

func truncateBytes(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// FormatFloat is a tiny helper so tests can assert filter strings without
// fighting locale; kept here so LoudnormArgs stays the single source of truth.
func FormatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
