package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"
)

// tailscaleIPFn resolves this machine's Tailscale IPv4. Tests replace it.
var tailscaleIPFn = lookupTailscaleIP

// SetTailscaleIPForTest replaces the Tailscale IP lookup. Pass nil to restore
// the real lookup. For tests only.
func SetTailscaleIPForTest(fn func() (string, error)) {
	if fn == nil {
		tailscaleIPFn = lookupTailscaleIP
		return
	}
	tailscaleIPFn = fn
}

// SkippedAddr is one configured dashboard.listen entry that could not be
// resolved to a bindable address on this attempt. It is not by itself fatal
// (CONTEXT D24 "run with only the keys/tools you have") — ResolveListen only
// errors when every entry is skipped.
type SkippedAddr struct {
	Input  string // the raw config entry, e.g. "tailscale:7070"
	Reason string
}

// ResolveResult is the outcome of resolving dashboard.listen. Addrs are ready
// to bind now; Skipped entries could not be resolved this time.
type ResolveResult struct {
	Addrs   []string
	Skipped []SkippedAddr
}

// NeedsTailscaleRetry reports whether any skipped entry was a "tailscale"
// sentinel, meaning a background retry could add it once Tailscale comes up.
func (r ResolveResult) NeedsTailscaleRetry() bool {
	for _, s := range r.Skipped {
		if isTailscaleSentinel(s.Input) {
			return true
		}
	}
	return false
}

func isTailscaleSentinel(addr string) bool {
	host, _, err := splitHostPort(addr)
	return err == nil && host == "tailscale"
}

// ResolveListen expands dashboard.listen entries into concrete host:port
// addresses, resolving each one independently. Allowed hosts are 127.0.0.1
// and the machine's Tailscale IP (ARCHITECTURE.md §7). The sentinel host
// "tailscale" is replaced with that IP; if Tailscale is not installed or not
// running, that one entry is skipped (not fatal) rather than failing every
// address in the list. Anything else (0.0.0.0, LAN IP, public IP, malformed
// address) is also skipped with a reason. It is an error only when every
// entry is skipped — ResolveListen never falls back to a wildcard or a
// non-loopback/non-Tailscale address (the loopback lock from M2-101/M2-106
// is unchanged).
func ResolveListen(addrs []string) (ResolveResult, error) {
	if len(addrs) == 0 {
		return ResolveResult{}, fmt.Errorf("httpapi: dashboard.listen is empty")
	}

	needTS := false
	for _, a := range addrs {
		if isTailscaleSentinel(a) {
			needTS = true
			break
		}
	}
	var tsIP string
	var tsErr error
	if needTS {
		tsIP, tsErr = tailscaleIPFn()
	}

	var result ResolveResult
	seen := map[string]bool{}
	for _, a := range addrs {
		resolved, skipReason := resolveOne(a, tsIP, tsErr)
		if skipReason != "" {
			result.Skipped = append(result.Skipped, SkippedAddr{Input: a, Reason: skipReason})
			continue
		}
		if seen[resolved] {
			continue
		}
		seen[resolved] = true
		result.Addrs = append(result.Addrs, resolved)
	}

	if len(result.Addrs) == 0 {
		return result, fmt.Errorf("httpapi: no dashboard.listen address could be resolved: %s", summarizeSkipped(result.Skipped))
	}
	return result, nil
}

// resolveOne resolves a single configured address. It returns either a
// bindable "host:port" or a non-empty skip reason, never both.
func resolveOne(addr, tsIP string, tsErr error) (resolved, skipReason string) {
	host, port, err := splitHostPort(addr)
	if err != nil {
		return "", err.Error()
	}
	switch host {
	case "127.0.0.1":
		// ok
	case "tailscale":
		if tsErr != nil {
			return "", fmt.Sprintf("tailscale not available: %s", tsErr)
		}
		host = tsIP
	default:
		if tsIP == "" || host != tsIP {
			return "", fmt.Sprintf("listen host %q not allowed (want 127.0.0.1 or tailscale)", host)
		}
		// explicit literal that happens to equal the resolved Tailscale IP: allowed
	}
	return net.JoinHostPort(host, port), ""
}

func summarizeSkipped(skipped []SkippedAddr) string {
	parts := make([]string, len(skipped))
	for i, s := range skipped {
		parts[i] = fmt.Sprintf("%s (%s)", s.Input, s.Reason)
	}
	return strings.Join(parts, "; ")
}

func splitHostPort(addr string) (host, port string, err error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", "", fmt.Errorf("httpapi: empty listen address")
	}
	host, port, err = net.SplitHostPort(addr)
	if err != nil {
		return "", "", fmt.Errorf("httpapi: listen address %q: %w", addr, err)
	}
	if port == "" {
		return "", "", fmt.Errorf("httpapi: listen address %q: missing port", addr)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return "", "", fmt.Errorf("httpapi: listen address %q: missing host (refusing wildcard)", addr)
	}
	if host == "0.0.0.0" || host == "::" || host == "[::]" {
		return "", "", fmt.Errorf("httpapi: listen host %q not allowed (want 127.0.0.1 or tailscale)", host)
	}
	return host, port, nil
}

func lookupTailscaleIP() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tailscale", "ip", "-4")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("tailscale ip -4: %s", msg)
	}
	ip := strings.TrimSpace(stdout.String())
	if net.ParseIP(ip) == nil {
		return "", fmt.Errorf("tailscale ip -4 returned non-IP %q", ip)
	}
	return ip, nil
}
