package httpapi

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var embeddedDist embed.FS

func distSubFS() (fs.FS, error) {
	return fs.Sub(embeddedDist, "dist")
}

// spaHandler serves static files from web and falls back to index.html for
// client-side routes (SPEC §4 dashboard embed).
func spaHandler(web fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(web))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
