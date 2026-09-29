package httpapi

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
)

// embeddedDist holds internal/httpapi/dist, which scripts/build.ps1 populates
// by copying the built web/dist into this package (go:embed cannot reach
// outside the package directory, so the copy is a build step, not a symlink).
// The directory is gitignored except for a .gitkeep, so a plain `go build`
// without running the web build first still compiles — it just serves the
// fallback below.
//
//go:embed all:dist
var embeddedDist embed.FS

// embeddedFallback holds the committed placeholder page, served only when
// dist/ has not been populated by a real web build.
//
//go:embed all:distfallback
var embeddedFallback embed.FS

// distSubFS returns the filesystem to serve at "/": the real built dashboard
// when scripts/build.ps1 has copied it into dist/, otherwise the committed
// placeholder. The returned string names which one, for a startup log line.
func distSubFS() (fs.FS, string, error) {
	if sub, err := fs.Sub(embeddedDist, "dist"); err == nil {
		if hasIndex(sub) {
			return sub, "built web/dist (internal/httpapi/dist, copied by scripts/build.ps1)", nil
		}
	}
	fallback, err := fs.Sub(embeddedFallback, "distfallback")
	if err != nil {
		return nil, "", fmt.Errorf("httpapi: embed fallback dashboard: %w", err)
	}
	return fallback, "placeholder page (web/dist not built — run scripts/build.ps1 or npm run build in web/)", nil
}

func hasIndex(web fs.FS) bool {
	f, err := web.Open("index.html")
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// spaHandler serves static files from web and falls back to index.html for
// client-side routes (SPEC §4 dashboard embed). It never intercepts a path
// under "/api/" or "/media/" — routes.go mounts those separately and ahead
// of this handler, but spaHandler also refuses to serve them defensively in
// case it is ever reached directly (e.g. from a test using Handler() on a
// sub-path).
func spaHandler(web fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(web))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/media/") {
			http.NotFound(w, r)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if f, err := web.Open(path); err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA fallback: only for navigations that look like HTML routes.
		if strings.Contains(r.Header.Get("Accept"), "text/html") || !strings.Contains(path, ".") {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			http.ServeFileFS(w, r2, web, "index.html")
			return
		}
		http.NotFound(w, r)
	})
}
