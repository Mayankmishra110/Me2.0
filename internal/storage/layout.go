// Package storage manages local media files under data_dir/media and their
// temporary public copies in Cloudflare R2 (CONTEXT.md D20, ARCHITECTURE §2/§8/§9).
//
// Three parts, kept in separate files:
//   - layout.go: local path helpers for the raw/work/renders/published tree,
//     with strict traversal protection (never resolve a path outside its root).
//   - r2.go: an S3-compatible uploader for Cloudflare R2 that produces
//     presigned GET URLs; enabled only when all four R2 env keys are set (D24).
//   - cleanup.go / diskspace.go: the retention job and the low-disk check.
package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Kind is one of the four media folders under data_dir/media (ARCHITECTURE §9).
type Kind string

const (
	KindRaw       Kind = "raw"       // untouched source input (uploaded/fetched)
	KindWork      Kind = "work"      // per-job scratch dirs, data/media/work/<job-id>/
	KindRenders   Kind = "renders"   // finished renders awaiting/after approval
	KindPublished Kind = "published" // published assets, retained content.retention_days then cleaned up
)

// kinds lists every valid Kind, in the fixed order ARCHITECTURE §9 documents them.
var kinds = []Kind{KindRaw, KindWork, KindRenders, KindPublished}

// Valid reports whether k is one of the four known media kinds.
func (k Kind) Valid() bool {
	for _, v := range kinds {
		if k == v {
			return true
		}
	}
	return false
}

// ErrPathEscapesRoot is wrapped by the error Layout.Path returns when the
// resolved path would fall outside its kind's root directory.
var ErrPathEscapesRoot = errors.New("path escapes storage root")

// isCaseInsensitiveFS is true on the only target platform (Windows, CONTEXT
// §3 hardware) where NTFS treats "Foo" and "foo" as the same path.
const isCaseInsensitiveFS = true

// Layout resolves media paths under <data_dir>/media, and refuses to resolve
// any path outside the relevant kind's root (path traversal via "..", a
// rooted/absolute segment, or a drive-letter change on Windows).
type Layout struct {
	root string // absolute: <data_dir>/media
}

// NewLayout builds a Layout rooted at <dataDir>/media. dataDir is made
// absolute (relative to the current working directory) if it is not already;
// callers normally pass config.Config.DataDir, which config.Load already
// resolves to an absolute path.
func NewLayout(dataDir string) (*Layout, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("storage: data dir is required")
	}
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("storage: resolve data dir %q: %w", dataDir, err)
	}
	return &Layout{root: filepath.Join(abs, "media")}, nil
}

// Root returns the absolute media root (<data_dir>/media).
func (l *Layout) Root() string { return l.root }

// KindRoot returns the absolute root directory for one kind (<data_dir>/media/<kind>).
func (l *Layout) KindRoot(kind Kind) (string, error) {
	if !kind.Valid() {
		return "", fmt.Errorf("storage: kind %q: want one of raw|work|renders|published", kind)
	}
	return filepath.Join(l.root, string(kind)), nil
}

// Path resolves parts under kind's root and guarantees the result stays
// inside it. Each part is treated as a path segment, not as raw user input to
// a shell or another tool, but callers must still not accept "parts" built
// from unvalidated AI/job output without going through this check.
//
// Traversal is rejected by cleaning the joined path and requiring it to be
// exactly the kind root, or to fall inside it (share a path separator
// boundary) — so "..", too many "../", and absolute-looking segments that
// would otherwise walk out of the root are all refused, regardless of how
// many parts are supplied.
func (l *Layout) Path(kind Kind, parts ...string) (string, error) {
	kindRoot, err := l.KindRoot(kind)
	if err != nil {
		return "", err
	}
	joined := filepath.Join(append([]string{kindRoot}, parts...)...)
	joined = filepath.Clean(joined)
	if !withinRoot(kindRoot, joined) {
		return "", fmt.Errorf("storage: %v: %q under %s", ErrPathEscapesRoot, filepath.Join(parts...), kind)
	}
	return joined, nil
}

// JobDir resolves the per-job scratch directory data/media/work/<job-id>/
// (ARCHITECTURE §2 child-process contract). jobID must not itself contain
// path separators or "..".
func (l *Layout) JobDir(jobID string) (string, error) {
	if strings.TrimSpace(jobID) == "" {
		return "", errors.New("storage: job id is required")
	}
	if jobID != filepath.Base(jobID) {
		return "", fmt.Errorf("storage: job id %q must not contain path separators", jobID)
	}
	return l.Path(KindWork, jobID)
}

// Ensure creates the media root and all four kind directories if missing.
func (l *Layout) Ensure() error {
	if err := os.MkdirAll(l.root, 0o755); err != nil {
		return fmt.Errorf("storage: create media root %s: %w", l.root, err)
	}
	for _, k := range kinds {
		dir := filepath.Join(l.root, string(k))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("storage: create %s dir %s: %w", k, dir, err)
		}
	}
	return nil
}

// withinRoot reports whether path is root itself, or lies inside root
// (root plus a path separator plus at least one more character). Both
// arguments must already be filepath.Clean-ed absolute paths. Comparison is
// case-insensitive on Windows, where the filesystem is case-insensitive and
// a case-only difference must not be treated as an escape.
func withinRoot(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
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
