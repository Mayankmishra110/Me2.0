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

// ResolveListen expands dashboard.listen entries into concrete host:port
// addresses. Allowed hosts are 127.0.0.1 and the machine's Tailscale IP
// (ARCHITECTURE.md §7). The sentinel host "tailscale" is replaced with that
// IP. Anything else (0.0.0.0, LAN IP, public IP) is rejected.
func ResolveListen(addrs []string) ([]string, error) {
	if len(addrs) == 0 {
		return nil, fmt.Errorf("httpapi: dashboard.listen is empty")
	}
	var tsIP string
	needTS := false
	for _, a := range addrs {
		host, _, err := splitHostPort(a)
		if err != nil {
			return nil, err
		}
		if host == "tailscale" {
			needTS = true
			break
		}
	}
	if needTS {
		ip, err := tailscaleIPFn()
		if err != nil {
			return nil, fmt.Errorf("httpapi: resolve tailscale IP: %w", err)
		}
		tsIP = ip
	}

	out := make([]string, 0, len(addrs))
	seen := map[string]bool{}
	for _, a := range addrs {
		host, port, err := splitHostPort(a)
		if err != nil {
			return nil, err
		}
		switch host {
		case "127.0.0.1":
			// ok
		case "tailscale":
			host = tsIP
		default:
			if tsIP != "" && host == tsIP {
				break
			}
			return nil, fmt.Errorf("httpapi: listen host %q not allowed (want 127.0.0.1 or tailscale)", host)
		}
		resolved := net.JoinHostPort(host, port)
		if seen[resolved] {
			continue
		}
		seen[resolved] = true
		out = append(out, resolved)
	}
	return out, nil
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
