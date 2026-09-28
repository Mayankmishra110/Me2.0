package storage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDeleteAfter(t *testing.T) {
	published := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		days int
		want time.Time
	}{
		{"default 7 days", 7, published.AddDate(0, 0, 7)},
		{"zero days", 0, published},
		{"negative days clamped to zero", -3, published},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DeleteAfter(published, tc.days)
			if !got.Equal(tc.want) {
				t.Errorf("DeleteAfter(%v, %d) = %v, want %v", published, tc.days, got, tc.want)
			}
		})
	}
}

// writeFile creates dir/name with content and returns the absolute path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func TestCleanup_DeletesOnlyDueItems(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

	dueFile := writeFile(t, root, "due.mp4", "x")
	notDueFile := writeFile(t, root, "not-due.mp4", "y")
	exactlyDueFile := writeFile(t, root, "exactly-due.mp4", "z")

	items := []CleanupItem{
		{ID: "due", Path: dueFile, DeleteAfter: now.Add(-time.Hour)},
		{ID: "not-due", Path: notDueFile, DeleteAfter: now.Add(time.Hour)},
		{ID: "exactly-due", Path: exactlyDueFile, DeleteAfter: now},
	}

	res := Cleanup(context.Background(), root, items, nil, now)

	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
	assertContains(t, res.DeletedLocal, "due")
	assertContains(t, res.DeletedLocal, "exactly-due")
	assertContains(t, res.Skipped, "not-due")

	if _, err := os.Stat(dueFile); !os.IsNotExist(err) {
		t.Errorf("due.mp4 should have been deleted, stat err = %v", err)
	}
	if _, err := os.Stat(exactlyDueFile); !os.IsNotExist(err) {
		t.Errorf("exactly-due.mp4 should have been deleted, stat err = %v", err)
	}
	if _, err := os.Stat(notDueFile); err != nil {
		t.Errorf("not-due.mp4 should still exist: %v", err)
	}
}

func TestCleanup_RefusesPathOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir() // a sibling temp dir, not under root
	outsideFile := writeFile(t, outside, "escape.mp4", "x")
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

	items := []CleanupItem{
		{ID: "escapee", Path: outsideFile, DeleteAfter: now.Add(-time.Hour)},
	}
	res := Cleanup(context.Background(), root, items, nil, now)

	if err, ok := res.Errors["escapee"]; !ok || err == nil {
		t.Fatalf("want an error for a path outside root, got Errors=%v", res.Errors)
	}
	if !errors.Is(res.Errors["escapee"], ErrPathEscapesRoot) {
		t.Errorf("error = %v, want wrapping ErrPathEscapesRoot", res.Errors["escapee"])
	}
	if _, err := os.Stat(outsideFile); err != nil {
		t.Errorf("file outside root must not be deleted: stat err = %v", err)
	}
	if len(res.DeletedLocal) != 0 {
		t.Errorf("DeletedLocal = %v, want empty", res.DeletedLocal)
	}
}

func TestCleanup_IdempotentOnAlreadyDeletedFile(t *testing.T) {
	root := t.TempDir()
	gone := filepath.Join(root, "already-gone.mp4")
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

	items := []CleanupItem{{ID: "gone", Path: gone, DeleteAfter: now.Add(-time.Hour)}}
	res := Cleanup(context.Background(), root, items, nil, now)

	if len(res.Errors) != 0 {
		t.Fatalf("re-deleting an absent file should not error, got %v", res.Errors)
	}
	assertContains(t, res.DeletedLocal, "gone")
}

func TestCleanup_R2NilClientSkipsR2ButStillDeletesLocal(t *testing.T) {
	root := t.TempDir()
	f := writeFile(t, root, "published.mp4", "x")
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

	items := []CleanupItem{{ID: "a", Path: f, R2Key: "assets/a.mp4", DeleteAfter: now.Add(-time.Minute)}}
	res := Cleanup(context.Background(), root, items, nil, now)

	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors with r2 nil: %v", res.Errors)
	}
	assertContains(t, res.DeletedLocal, "a")
	if len(res.DeletedR2) != 0 {
		t.Errorf("DeletedR2 = %v, want empty when r2 is not configured", res.DeletedR2)
	}
}

func TestCleanup_DeletesR2ObjectWhenConfigured(t *testing.T) {
	root := t.TempDir()
	f := writeFile(t, root, "published.mp4", "x")
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

	var deletedKeys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deletedKeys = append(deletedKeys, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r2 := newTestR2(t, srv.URL)
	items := []CleanupItem{{ID: "a", Path: f, R2Key: "assets/a.mp4", DeleteAfter: now.Add(-time.Minute)}}
	res := Cleanup(context.Background(), root, items, r2, now)

	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
	assertContains(t, res.DeletedLocal, "a")
	assertContains(t, res.DeletedR2, "a")
	if len(deletedKeys) != 1 {
		t.Fatalf("fake server saw %d DELETE calls, want 1", len(deletedKeys))
	}
}

func TestCleanup_PartialFailureDoesNotAbortRun(t *testing.T) {
	root := t.TempDir()
	good := writeFile(t, root, "good.mp4", "x")
	outside := t.TempDir()
	bad := writeFile(t, outside, "bad.mp4", "y")
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

	items := []CleanupItem{
		{ID: "bad", Path: bad, DeleteAfter: now.Add(-time.Hour)},
		{ID: "good", Path: good, DeleteAfter: now.Add(-time.Hour)},
	}
	res := Cleanup(context.Background(), root, items, nil, now)

	assertContains(t, res.DeletedLocal, "good")
	if _, ok := res.Errors["bad"]; !ok {
		t.Fatal("want an error recorded for the bad item")
	}
}

func assertContains(t *testing.T, list []string, want string) {
	t.Helper()
	for _, v := range list {
		if v == want {
			return
		}
	}
	t.Fatalf("%v does not contain %q", list, want)
}
