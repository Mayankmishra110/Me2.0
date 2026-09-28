package httpapi

import (
	"strings"
	"testing"
)

func TestResolveListen_allowsLoopbackAndTailscale(t *testing.T) {
	orig := tailscaleIPFn
	tailscaleIPFn = func() (string, error) { return "100.64.1.2", nil }
	t.Cleanup(func() { tailscaleIPFn = orig })

	got, err := ResolveListen([]string{"127.0.0.1:7070", "tailscale:7070"})
	if err != nil {
		t.Fatalf("ResolveListen: %v", err)
	}
	want := []string{"127.0.0.1:7070", "100.64.1.2:7070"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestResolveListen_rejectsWildcardAndLAN(t *testing.T) {
	orig := tailscaleIPFn
	tailscaleIPFn = func() (string, error) { return "100.64.1.2", nil }
	t.Cleanup(func() { tailscaleIPFn = orig })

	cases := []string{
		"0.0.0.0:7070",
		"192.168.1.10:7070",
		":7070",
		"localhost:7070",
	}
	for _, addr := range cases {
		_, err := ResolveListen([]string{addr})
		if err == nil {
			t.Fatalf("%s: expected error", addr)
		}
		if !strings.Contains(err.Error(), "not allowed") && !strings.Contains(err.Error(), "missing host") && !strings.Contains(err.Error(), "listen address") {
			t.Fatalf("%s: unexpected error %v", addr, err)
		}
	}
}

func TestResolveListen_empty(t *testing.T) {
	_, err := ResolveListen(nil)
	if err == nil {
		t.Fatal("expected error for empty listen")
	}
}
