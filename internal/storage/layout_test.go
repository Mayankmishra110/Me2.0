package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewLayout(t *testing.T) {
	t.Run("empty data dir rejected", func(t *testing.T) {
		if _, err := NewLayout(""); err == nil {
			t.Fatal("want error for empty data dir")
		}
	})
	t.Run("relative data dir resolved to absolute", func(t *testing.T) {
		l, err := NewLayout("relative-data")
		if err != nil {
			t.Fatalf("NewLayout: %v", err)
		}
		if !filepath.IsAbs(l.Root()) {
			t.Fatalf("Root() = %q, want absolute path", l.Root())
		}
		if !strings.HasSuffix(l.Root(), filepath.Join("relative-data", "media")) {
			t.Fatalf("Root() = %q, want to end with relative-data/media", l.Root())
		}
	})
}

func TestLayout_KindRoot(t *testing.T) {
	l, err := NewLayout(t.TempDir())
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	for _, k := range []Kind{KindRaw, KindWork, KindRenders, KindPublished} {
		root, err := l.KindRoot(k)
		if err != nil {
			t.Fatalf("KindRoot(%s): %v", k, err)
		}
		want := filepath.Join(l.Root(), string(k))
		if root != want {
			t.Fatalf("KindRoot(%s) = %q, want %q", k, root, want)
		}
	}
	if _, err := l.KindRoot(Kind("bogus")); err == nil {
		t.Fatal("want error for invalid kind")
	}
}

func TestLayout_Path(t *testing.T) {
	l, err := NewLayout(t.TempDir())
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}

	cases := []struct {
		name    string
		kind    Kind
		parts   []string
		wantErr bool
	}{
		{"simple file", KindRaw, []string{"video.mp4"}, false},
		{"nested job dir", KindWork, []string{"job-123", "input.json"}, false},
		{"empty parts is kind root", KindPublished, nil, false},
		{"single dotdot escapes", KindRaw, []string{".."}, true},
		{"dotdot then walk out", KindRaw, []string{"..", "..", "etc", "passwd"}, true},
		{"deep dotdot escapes", KindWork, []string{"a", "..", "..", "b"}, true},
		{"dotdot inside a legit-looking name is fine", KindRaw, []string{"job..name", "f.mp4"}, false},
		{"leading slash segment does not escape", KindRaw, []string{"/etc/passwd"}, false},
		{"invalid kind", Kind("bogus"), []string{"x"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := l.Path(tc.kind, tc.parts...)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Path(%s, %v) = %q, want error", tc.kind, tc.parts, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Path(%s, %v): %v", tc.kind, tc.parts, err)
			}
			kindRoot, _ := l.KindRoot(tc.kind)
			if !withinRoot(kindRoot, got) {
				t.Fatalf("Path(%s, %v) = %q, escapes root %q", tc.kind, tc.parts, got, kindRoot)
			}
		})
	}
}

func TestLayout_JobDir(t *testing.T) {
	l, err := NewLayout(t.TempDir())
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	dir, err := l.JobDir("job-abc123")
	if err != nil {
		t.Fatalf("JobDir: %v", err)
	}
	want, _ := l.Path(KindWork, "job-abc123")
	if dir != want {
		t.Fatalf("JobDir = %q, want %q", dir, want)
	}

	badIDs := []string{"", "../escape", "sub/dir", "..\\escape"}
	for _, id := range badIDs {
		if _, err := l.JobDir(id); err == nil {
			t.Fatalf("JobDir(%q) = nil error, want error", id)
		}
	}
}

func TestLayout_Ensure(t *testing.T) {
	l, err := NewLayout(t.TempDir())
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	if err := l.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	for _, k := range []Kind{KindRaw, KindWork, KindRenders, KindPublished} {
		root, _ := l.KindRoot(k)
		info, err := os.Stat(root)
		if err != nil {
			t.Fatalf("stat %s: %v", root, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s was not created as a directory", root)
		}
	}
}

func TestKind_Valid(t *testing.T) {
	for _, k := range []Kind{KindRaw, KindWork, KindRenders, KindPublished} {
		if !k.Valid() {
			t.Fatalf("%s should be valid", k)
		}
	}
	if Kind("nope").Valid() {
		t.Fatal("bogus kind should not be valid")
	}
}

func TestWithinRoot(t *testing.T) {
	root := filepath.Join("C:", "data", "media", "raw")
	cases := []struct {
		path string
		want bool
	}{
		{root, true},
		{filepath.Join(root, "a.mp4"), true},
		{filepath.Join(root, "sub", "a.mp4"), true},
		{filepath.Join("C:", "data", "media", "rawother", "a.mp4"), false},
		{filepath.Join("C:", "data", "media"), false},
		{filepath.Join("C:", "etc", "passwd"), false},
		{strings.ToUpper(root), true}, // case-insensitive NTFS
	}
	for _, tc := range cases {
		if got := withinRoot(root, tc.path); got != tc.want {
			t.Errorf("withinRoot(%q, %q) = %v, want %v", root, tc.path, got, tc.want)
		}
	}
}
