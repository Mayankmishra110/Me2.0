package httpapi

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// TestListenAndServe_tailscaleMissingStillServesLoopback is the end-to-end
// M2-118 defect-1 regression: with the example config's
// ["127.0.0.1:PORT", "tailscale:PORT"] and no tailscale binary, the
// dashboard must still come up on loopback instead of dying entirely
// (previously ListenAndServe returned an error before binding anything).
func TestListenAndServe_tailscaleMissingStillServesLoopback(t *testing.T) {
	orig := tailscaleIPFn
	tailscaleIPFn = func() (string, error) { return "", errors.New("tailscale: not found") }
	t.Cleanup(func() { tailscaleIPFn = orig })

	sqlDB := testDB(t)
	s := testServer(t, sqlDB, nil)

	port := freePort(t)
	addr := "127.0.0.1:" + port
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- s.ListenAndServe(ctx, []string{addr, "tailscale:" + port}) }()

	waitForServer(t, addr)

	resp, err := http.Get("http://" + addr + "/api/health")
	if err != nil {
		t.Fatalf("GET /api/health: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("ListenAndServe returned %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ListenAndServe did not return after ctx cancel")
	}
}

// TestListenAndServe_allAddressesBadErrors proves ListenAndServe still fails
// loudly when there is truly nothing bindable — it must not silently no-op.
func TestListenAndServe_allAddressesBadErrors(t *testing.T) {
	orig := tailscaleIPFn
	tailscaleIPFn = func() (string, error) { return "", errors.New("tailscale: not found") }
	t.Cleanup(func() { tailscaleIPFn = orig })

	sqlDB := testDB(t)
	s := testServer(t, sqlDB, nil)

	err := s.ListenAndServe(context.Background(), []string{"tailscale:7070", "0.0.0.0:7070"})
	if err == nil {
		t.Fatal("expected error when no address can be bound")
	}
}
