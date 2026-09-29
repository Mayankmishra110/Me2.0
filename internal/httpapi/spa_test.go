package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

// fakeWeb is a small fake built dashboard: an index.html, a hashed JS asset,
// and nothing at "/approvals" (a client-side route the SPA router owns).
func fakeWeb() fstest.MapFS {
	return fstest.MapFS{
		"index.html":            &fstest.MapFile{Data: []byte("<html>dashboard</html>")},
		"assets/app-abc123.js":  &fstest.MapFile{Data: []byte("console.log('app')")},
		"assets/app-abc123.css": &fstest.MapFile{Data: []byte("body{}")},
	}
}

func TestSPA_servesIndexAtRoot(t *testing.T) {
	h := spaHandler(fakeWeb())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if rr.Body.String() != "<html>dashboard</html>" {
		t.Fatalf("body = %q", rr.Body.String())
	}
}

func TestSPA_servesRealStaticAsset(t *testing.T) {
	h := spaHandler(fakeWeb())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/assets/app-abc123.js", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if rr.Body.String() != "console.log('app')" {
		t.Fatalf("body = %q", rr.Body.String())
	}
}

// TestSPA_fallsBackToIndexForClientRoute proves a client-side route with no
// matching file (e.g. /approvals) still gets index.html so React Router can
// take over, instead of a 404.
func TestSPA_fallsBackToIndexForClientRoute(t *testing.T) {
	h := spaHandler(fakeWeb())

	cases := []string{"/approvals", "/jobs/123", "/builder"}
	for _, path := range cases {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Accept", "text/html,application/xhtml+xml")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", path, rr.Code)
		}
		if rr.Body.String() != "<html>dashboard</html>" {
			t.Fatalf("%s: body = %q, want index.html fallback", path, rr.Body.String())
		}
	}
}

// TestSPA_missingAssetIsNotFound proves a request that looks like a static
// asset (has a file extension) but doesn't exist is a real 404, not silently
// rewritten to index.html.
func TestSPA_missingAssetIsNotFound(t *testing.T) {
	h := spaHandler(fakeWeb())
	req := httptest.NewRequest(http.MethodGet, "/assets/does-not-exist.js", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

// TestSPA_neverShadowsAPIOrMedia proves the SPA handler itself refuses
// /api/* and /media/* defensively (routes.go also mounts those ahead of the
// SPA handler in the real mux — this guards the handler in isolation).
func TestSPA_neverShadowsAPIOrMedia(t *testing.T) {
	h := spaHandler(fakeWeb())
	for _, path := range []string{"/api/health", "/api/approvals", "/media/asset1"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Accept", "text/html")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404 (SPA must never shadow this)", path, rr.Code)
		}
	}
}

// TestRoutes_healthNotShadowedByBuiltDashboard is the end-to-end version of
// the same guarantee through the real mux (routes.go), using a fake "built"
// web FS the way scripts/build.ps1 would populate it, proving GET
// /api/health (and other API routes) resolve to the API, never to
// index.html, even though both are mounted on the same mux.
func TestRoutes_healthNotShadowedByBuiltDashboard(t *testing.T) {
	sqlDB := testDB(t)
	s, err := New(Options{
		DashboardToken: testToken,
		DB:             sqlDB,
		Version:        "test",
		WebFS:          fakeWeb(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := s.Handler()

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/api/health status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct == "" || rr.Body.String() == "<html>dashboard</html>" {
		t.Fatalf("/api/health was shadowed by the SPA index.html: content-type=%q body=%q", ct, rr.Body.String())
	}

	// A client route still gets the dashboard shell.
	rr2 := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/approvals", nil)
	req.Header.Set("Accept", "text/html")
	h.ServeHTTP(rr2, req)
	if rr2.Code != http.StatusOK || rr2.Body.String() != "<html>dashboard</html>" {
		t.Fatalf("/approvals status=%d body=%q, want 200 + dashboard shell", rr2.Code, rr2.Body.String())
	}
}

// TestDistSubFS_fallsBackWhenNoRealBuild proves the runtime embed selection:
// the repo's committed internal/httpapi/dist only has .gitkeep (no real
// build has been copied in during this test run), so distSubFS must return
// the committed placeholder, not an empty/broken filesystem.
func TestDistSubFS_fallsBackWhenNoRealBuild(t *testing.T) {
	web, source, err := distSubFS()
	if err != nil {
		t.Fatalf("distSubFS: %v", err)
	}
	f, err := web.Open("index.html")
	if err != nil {
		t.Fatalf("index.html missing from resolved dashboard FS: %v", err)
	}
	_ = f.Close()
	if source == "" {
		t.Fatal("distSubFS did not report which dashboard source it picked")
	}
}
