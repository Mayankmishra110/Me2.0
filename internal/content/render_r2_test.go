package content

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"mayank2/internal/storage"
)

// testR2 builds a *storage.R2Client pointed at a local httptest server
// instead of real R2 (same pattern internal/storage/r2_test.go's newTestR2
// uses) so Renderer.recordFile's upload path (M2-123) can be exercised
// without real network or credentials.
func testR2(t *testing.T, handler http.HandlerFunc) (*storage.R2Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	r2, err := storage.NewR2(storage.R2Config{
		AccountID:       "test-account",
		AccessKeyID:     "test-key",
		SecretAccessKey: "test-secret",
		Bucket:          "test-bucket",
	}, storage.R2Options{EndpointOverride: srv.URL})
	if err != nil {
		t.Fatalf("storage.NewR2: %v", err)
	}
	return r2, srv
}

// TestRecordFile_UploadsToR2WhenConfigured verifies the eager-upload design
// (tickets/M2-123.md): a render/thumb asset written locally is uploaded to
// R2 right away when Renderer.R2 is configured, and the resulting key is
// both returned in the recordedAsset and persisted in assets.r2_key.
func TestRecordFile_UploadsToR2WhenConfigured(t *testing.T) {
	sqlDB := renderTestDB(t)
	r := testRenderer(t, sqlDB, nil)

	var gotPut bool
	var gotPath string
	r2, _ := testR2(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPut {
			gotPut = true
			gotPath = req.URL.Path
			w.Header().Set("ETag", `"etag"`)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	r.R2 = r2

	dir := t.TempDir()
	finalPath := filepath.Join(dir, "long.mp4")
	if err := os.WriteFile(finalPath, []byte("fake mp4 bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	asset, err := r.recordFile(context.Background(), "c1", assetKindRender, finalPath)
	if err != nil {
		t.Fatalf("recordFile: %v", err)
	}
	if !gotPut {
		t.Fatal("R2 server never saw a PUT — recordFile did not upload")
	}
	wantKey := "renders/c1/long.mp4"
	if gotPath != "/test-bucket/"+wantKey {
		t.Fatalf("PUT path = %q, want suffix for key %q", gotPath, wantKey)
	}
	if asset.R2Key != wantKey {
		t.Fatalf("asset.R2Key = %q, want %q", asset.R2Key, wantKey)
	}

	var r2Key string
	if err := sqlDB.QueryRow(`SELECT r2_key FROM assets WHERE content_id='c1' AND kind='render'`).Scan(&r2Key); err != nil {
		t.Fatalf("query r2_key: %v", err)
	}
	if r2Key != wantKey {
		t.Fatalf("stored r2_key = %q, want %q", r2Key, wantKey)
	}
}

// TestRecordFile_UploadFailureLogsAndContinues verifies CONTEXT D24 / the
// ticket's acceptance criterion: an R2 upload failure while R2 is
// configured must not fail the render job. The asset is still recorded
// (local file already exists), just with no r2_key, so a later publish
// attempt fails cleanly at presign time instead of the whole render job
// failing over a transient R2 outage.
func TestRecordFile_UploadFailureLogsAndContinues(t *testing.T) {
	sqlDB := renderTestDB(t)
	r := testRenderer(t, sqlDB, nil)

	r2, _ := testR2(t, func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	r.R2 = r2

	dir := t.TempDir()
	finalPath := filepath.Join(dir, "short.mp4")
	if err := os.WriteFile(finalPath, []byte("fake short mp4"), 0o644); err != nil {
		t.Fatal(err)
	}

	asset, err := r.recordFile(context.Background(), "c1", assetKindRender, finalPath)
	if err != nil {
		t.Fatalf("recordFile must not fail the render job on upload failure, got: %v", err)
	}
	if asset.R2Key != "" {
		t.Fatalf("asset.R2Key = %q, want empty on upload failure", asset.R2Key)
	}
	if asset.SHA256 == "" || asset.Bytes == 0 {
		t.Fatalf("asset local metadata missing: %+v", asset)
	}

	var r2Key sql.NullString
	if err := sqlDB.QueryRow(`SELECT r2_key FROM assets WHERE content_id='c1' AND kind='render'`).Scan(&r2Key); err != nil {
		t.Fatalf("query r2_key: %v", err)
	}
	if r2Key.Valid {
		t.Fatalf("stored r2_key = %q, want NULL", r2Key.String)
	}
}

// TestRecordFile_NoR2ConfiguredLeavesR2KeyEmpty verifies the D24 degrade
// path: Renderer.R2 == nil (R2 not configured) must leave every asset
// fully local-only, with no upload attempted and r2_key never set — no
// behavior change from before this ticket.
func TestRecordFile_NoR2ConfiguredLeavesR2KeyEmpty(t *testing.T) {
	sqlDB := renderTestDB(t)
	r := testRenderer(t, sqlDB, nil) // r.R2 left nil

	dir := t.TempDir()
	finalPath := filepath.Join(dir, "thumb.png")
	if err := os.WriteFile(finalPath, []byte("fake png"), 0o644); err != nil {
		t.Fatal(err)
	}

	asset, err := r.recordFile(context.Background(), "c1", assetKindThumb, finalPath)
	if err != nil {
		t.Fatalf("recordFile: %v", err)
	}
	if asset.R2Key != "" {
		t.Fatalf("asset.R2Key = %q, want empty when R2 not configured", asset.R2Key)
	}

	var r2Key sql.NullString
	if err := sqlDB.QueryRow(`SELECT r2_key FROM assets WHERE content_id='c1' AND kind='thumb'`).Scan(&r2Key); err != nil {
		t.Fatalf("query r2_key: %v", err)
	}
	if r2Key.Valid {
		t.Fatalf("stored r2_key = %q, want NULL", r2Key.String)
	}
}
