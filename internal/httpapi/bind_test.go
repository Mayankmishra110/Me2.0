package httpapi

import (
	"errors"
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
	if len(got.Skipped) != 0 {
		t.Fatalf("unexpected skips: %+v", got.Skipped)
	}
	want := []string{"127.0.0.1:7070", "100.64.1.2:7070"}
	if len(got.Addrs) != len(want) {
		t.Fatalf("got %v want %v", got.Addrs, want)
	}
	for i := range want {
		if got.Addrs[i] != want[i] {
			t.Fatalf("got %v want %v", got.Addrs, want)
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
		got, err := ResolveListen([]string{addr})
		if err == nil {
			t.Fatalf("%s: expected error", addr)
		}
		if len(got.Addrs) != 0 {
			t.Fatalf("%s: expected no bindable addresses, got %v", addr, got.Addrs)
		}
		if len(got.Skipped) != 1 {
			t.Fatalf("%s: expected one skipped entry, got %+v", addr, got.Skipped)
		}
		reason := got.Skipped[0].Reason
		if !strings.Contains(reason, "not allowed") && !strings.Contains(reason, "missing host") && !strings.Contains(reason, "listen address") {
			t.Fatalf("%s: unexpected skip reason %v", addr, reason)
		}
	}
}

func TestResolveListen_empty(t *testing.T) {
	_, err := ResolveListen(nil)
	if err == nil {
		t.Fatal("expected error for empty listen")
	}
}

// TestResolveListen_tailscaleMissingStillBindsLoopback is the core M2-118
// defect-1 regression test: on a machine without the tailscale binary (the
// example config ships ["127.0.0.1:7070", "tailscale:7070"]), the loopback
// address must still resolve — resolution must not be all-or-nothing.
func TestResolveListen_tailscaleMissingStillBindsLoopback(t *testing.T) {
	orig := tailscaleIPFn
	tailscaleIPFn = func() (string, error) {
		return "", errors.New("exec: \"tailscale\": executable file not found in %PATH%")
	}
	t.Cleanup(func() { tailscaleIPFn = orig })

	got, err := ResolveListen([]string{"127.0.0.1:7070", "tailscale:7070"})
	if err != nil {
		t.Fatalf("ResolveListen: unexpected error %v (loopback should still bind)", err)
	}
	if len(got.Addrs) != 1 || got.Addrs[0] != "127.0.0.1:7070" {
		t.Fatalf("got addrs %v, want only 127.0.0.1:7070", got.Addrs)
	}
	if len(got.Skipped) != 1 {
		t.Fatalf("got skipped %+v, want exactly one skipped (tailscale)", got.Skipped)
	}
	if !isTailscaleSentinel(got.Skipped[0].Input) {
		t.Fatalf("skipped entry %+v is not the tailscale sentinel", got.Skipped[0])
	}
	if !got.NeedsTailscaleRetry() {
		t.Fatal("NeedsTailscaleRetry() = false, want true")
	}
}

// TestResolveListen_allAddressesBadIsError proves the "only an error when
// zero addresses resolve" contract from the ticket.
func TestResolveListen_allAddressesBadIsError(t *testing.T) {
	orig := tailscaleIPFn
	tailscaleIPFn = func() (string, error) { return "", errors.New("tailscale not installed") }
	t.Cleanup(func() { tailscaleIPFn = orig })

	got, err := ResolveListen([]string{"tailscale:7070", "192.168.1.5:7070", "0.0.0.0:7070"})
	if err == nil {
		t.Fatal("expected error when every address is unresolvable")
	}
	if len(got.Addrs) != 0 {
		t.Fatalf("got addrs %v, want none", got.Addrs)
	}
	if len(got.Skipped) != 3 {
		t.Fatalf("got %d skipped, want 3: %+v", len(got.Skipped), got.Skipped)
	}
}

// TestResolveListen_neverBindsWildcard is a table-driven sweep proving 0.0.0.0
// (and other wildcard spellings) never appear in Addrs, tailscale available
// or not.
func TestResolveListen_neverBindsWildcard(t *testing.T) {
	cases := []struct {
		name  string
		tsIP  string
		tsErr error
		addrs []string
	}{
		{"tailscale up", "100.64.1.2", nil, []string{"0.0.0.0:7070", "tailscale:7070", "127.0.0.1:7070"}},
		{"tailscale down", "", errors.New("no tailscale"), []string{"0.0.0.0:7070", "tailscale:7070", "127.0.0.1:7070"}},
		{"double colon wildcard", "100.64.1.2", nil, []string{"[::]:7070", "127.0.0.1:7070"}},
	}
	orig := tailscaleIPFn
	t.Cleanup(func() { tailscaleIPFn = orig })

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tailscaleIPFn = func() (string, error) { return tc.tsIP, tc.tsErr }
			got, err := ResolveListen(tc.addrs)
			if err != nil && len(got.Addrs) == 0 {
				// fine: nothing bindable this run, still must not contain a wildcard
			}
			for _, a := range got.Addrs {
				if strings.HasPrefix(a, "0.0.0.0") || strings.HasPrefix(a, "[::]") || strings.HasPrefix(a, "::") {
					t.Fatalf("ResolveListen returned wildcard address %q in %v", a, got.Addrs)
				}
			}
		})
	}
}
