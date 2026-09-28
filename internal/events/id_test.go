package events

import (
	"strings"
	"testing"
)

func TestNewID_FormatAndCharset(t *testing.T) {
	id := newID()
	if len(id) != 26 {
		t.Fatalf("len(newID())=%d want 26 (id=%q)", len(id), id)
	}
	for _, c := range id {
		if !strings.ContainsRune(crockford, c) {
			t.Fatalf("newID() contains char %q outside Crockford base32 alphabet: %q", c, id)
		}
	}
}

func TestNewID_MonotonicWithinTightLoop(t *testing.T) {
	const n = 500
	ids := make([]string, n)
	for i := range ids {
		ids[i] = newID()
	}
	for i := 1; i < n; i++ {
		if ids[i] <= ids[i-1] {
			t.Fatalf("ids not strictly increasing at index %d: %q <= %q", i, ids[i], ids[i-1])
		}
	}
}

func TestNewID_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := newID()
		if seen[id] {
			t.Fatalf("duplicate id generated: %q", id)
		}
		seen[id] = true
	}
}
