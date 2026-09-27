package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"mayank2/internal/config"
)

type checkResult struct {
	Name   string
	OK     bool
	Detail string
}

func cmdDoctor(ctx context.Context, args []string) int {
	configPath, envPath, envExample, rest, err := parseDoctorFlags(args)
	if err == errHelp {
		fmt.Fprint(os.Stderr, usage)
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "doctor: %v\n", err)
		return 2
	}
	if len(rest) > 0 {
		fmt.Fprintf(os.Stderr, "doctor: unexpected args %v\n", rest)
		return 2
	}

	if err := config.LoadEnvFile(envPath); err != nil {
		fmt.Fprintf(os.Stderr, "doctor: load env: %v\n", err)
		return 1
	}

	var cfg *config.Config
	cfgLoadErr := error(nil)
	if !fileExists(configPath) {
		// Fall back to example so doctor can still check tools without a local copy.
		example := filepath.Join(filepath.Dir(configPath), "config.example.yaml")
		if fileExists(example) {
			configPath = example
		}
	}
	cfg, cfgLoadErr = config.Load(configPath)

	checks := []checkResult{
		checkLookPath(ctx, "ffmpeg", "ffmpeg", "-version"),
		checkLookPath(ctx, "node", "node", "-v"),
		checkPythonUV(ctx),
		checkLookPath(ctx, "claude CLI", "claude", "--version"),
		checkLookPath(ctx, "git", "git", "--version"),
	}

	if cfg != nil {
		checks = append(checks, checkOllama(ctx, cfg))
		checks = append(checks, checkDisk(cfg.DataDir))
		checks = append(checks, checkChannels(cfg))
	} else {
		checks = append(checks,
			checkResult{Name: "ollama", OK: false, Detail: "skipped (config not loaded)"},
			checkResult{Name: "disk free >= 100 GB", OK: false, Detail: "skipped (config not loaded)"},
			checkResult{Name: "channel files", OK: false, Detail: "skipped (config not loaded)"},
		)
	}

	checks = append(checks, checkRAM())
	checks = append(checks, checkEnvKeys(envExample)...)

	if cfgLoadErr != nil {
		checks = append([]checkResult{{
			Name:   "config",
			OK:     false,
			Detail: cfgLoadErr.Error(),
		}}, checks...)
	} else {
		checks = append([]checkResult{{
			Name:   "config",
			OK:     true,
			Detail: configPath,
		}}, checks...)
	}

	failed := 0
	for _, c := range checks {
		mark := "✅"
		if !c.OK {
			mark = "❌"
			failed++
		}
		fmt.Printf("%s %s — %s\n", mark, c.Name, c.Detail)
	}
	if failed > 0 {
		fmt.Printf("\ndoctor: %d check(s) failed\n", failed)
		return 1
	}
	fmt.Println("\ndoctor: all checks passed")
	return 0
}

func checkLookPath(ctx context.Context, label, bin string, versionArgs ...string) checkResult {
	path, err := exec.LookPath(bin)
	if err != nil {
		return checkResult{Name: label, OK: false, Detail: fmt.Sprintf("not found in PATH (%v)", err)}
	}
	detail := path
	if len(versionArgs) > 0 {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(cctx, bin, versionArgs...)
		out, err := cmd.CombinedOutput()
		line := firstLine(string(out))
		if err != nil && line == "" {
			return checkResult{Name: label, OK: false, Detail: fmt.Sprintf("%s: %v", path, err)}
		}
		if line != "" {
			detail = line
		}
	}
	return checkResult{Name: label, OK: true, Detail: detail}
}

func checkPythonUV(ctx context.Context) checkResult {
	if _, err := exec.LookPath("uv"); err == nil {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(cctx, "uv", "--version").CombinedOutput()
		if err == nil {
			return checkResult{Name: "uv/python", OK: true, Detail: firstLine(string(out))}
		}
	}
	if path, err := exec.LookPath("python"); err == nil {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(cctx, "python", "--version").CombinedOutput()
		if err == nil {
			return checkResult{Name: "uv/python", OK: true, Detail: "uv missing; " + firstLine(string(out)) + " at " + path}
		}
		return checkResult{Name: "uv/python", OK: true, Detail: "uv missing; python at " + path}
	}
	if path, err := exec.LookPath("python3"); err == nil {
		return checkResult{Name: "uv/python", OK: true, Detail: "uv missing; python3 at " + path}
	}
	return checkResult{Name: "uv/python", OK: false, Detail: "neither uv nor python found in PATH"}
}

func checkOllama(ctx context.Context, cfg *config.Config) checkResult {
	base := cfg.OllamaBaseURL()
	if err := config.RequireLoopbackURL(base); err != nil {
		return checkResult{Name: "ollama", OK: false, Detail: err.Error()}
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, base+"/api/tags", nil)
	if err != nil {
		return checkResult{Name: "ollama", OK: false, Detail: err.Error()}
	}
	resp, err := ollamaHTTPClient().Do(req)
	if err != nil {
		return checkResult{Name: "ollama", OK: false, Detail: fmt.Sprintf("unreachable at %s: %v", base, err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return checkResult{Name: "ollama", OK: false, Detail: fmt.Sprintf("%s: HTTP %d", base, resp.StatusCode)}
	}
	var body struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return checkResult{Name: "ollama", OK: false, Detail: fmt.Sprintf("decode tags: %v", err)}
	}
	pulled := map[string]bool{}
	var names []string
	for _, m := range body.Models {
		pulled[m.Name] = true
		// also index without tag
		if i := strings.IndexByte(m.Name, ':'); i > 0 {
			pulled[m.Name[:i]] = true
		}
		names = append(names, m.Name)
	}
	model, embed, ok := cfg.OllamaModels()
	if !ok {
		return checkResult{Name: "ollama", OK: true, Detail: fmt.Sprintf("reachable; models=%v (no ollama provider in config)", names)}
	}
	var missing []string
	if model != "" && !modelPulled(pulled, model) {
		missing = append(missing, model)
	}
	if embed != "" && !modelPulled(pulled, embed) {
		missing = append(missing, embed)
	}
	if len(missing) > 0 {
		return checkResult{
			Name:   "ollama",
			OK:     false,
			Detail: fmt.Sprintf("reachable but missing models %v (pulled: %v)", missing, names),
		}
	}
	return checkResult{Name: "ollama", OK: true, Detail: fmt.Sprintf("reachable; %s and %s present", model, embed)}
}

// ollamaHTTPClient refuses redirects whose target host is not loopback.
func ollamaHTTPClient() *http.Client {
	return &http.Client{
		CheckRedirect: ollamaCheckRedirect,
	}
}

func ollamaCheckRedirect(req *http.Request, via []*http.Request) error {
	if err := config.RequireLoopbackURL(req.URL.String()); err != nil {
		return fmt.Errorf("redirect refused: %w", err)
	}
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	return nil
}

func modelPulled(pulled map[string]bool, want string) bool {
	if pulled[want] {
		return true
	}
	// Config may use "name:tag"; tags list may use either form.
	if i := strings.IndexByte(want, ':'); i > 0 && pulled[want[:i]] {
		return true
	}
	for name := range pulled {
		if name == want || strings.HasPrefix(name, want+":") {
			return true
		}
	}
	return false
}

func checkDisk(dataDir string) checkResult {
	free, total, err := diskFreeBytes(dataDir)
	if err != nil {
		return checkResult{Name: "disk free >= 100 GB", OK: false, Detail: err.Error()}
	}
	const need = 100 * 1024 * 1024 * 1024
	detail := fmt.Sprintf("%s free / %s total on volume for %s", formatBytes(free), formatBytes(total), dataDir)
	if free < need {
		return checkResult{Name: "disk free >= 100 GB", OK: false, Detail: detail}
	}
	return checkResult{Name: "disk free >= 100 GB", OK: true, Detail: detail}
}

func checkRAM() checkResult {
	total, err := totalRAMBytes()
	if err != nil {
		return checkResult{Name: "RAM", OK: false, Detail: err.Error()}
	}
	return checkResult{Name: "RAM", OK: true, Detail: formatBytes(total) + " total"}
}

func checkChannels(cfg *config.Config) checkResult {
	n := len(cfg.Channels)
	if n == 0 {
		return checkResult{Name: "channel files", OK: false, Detail: fmt.Sprintf("no channels in %s", cfg.Content.ChannelsDir)}
	}
	ids := make([]string, 0, n)
	for _, ch := range cfg.Channels {
		ids = append(ids, ch.ID)
	}
	return checkResult{Name: "channel files", OK: true, Detail: fmt.Sprintf("%d valid (%s)", n, strings.Join(ids, ", "))}
}

func checkEnvKeys(examplePath string) []checkResult {
	keys, err := config.RequiredEnvKeys(examplePath)
	if err != nil {
		return []checkResult{{Name: ".env keys", OK: false, Detail: err.Error()}}
	}
	out := make([]checkResult, 0, len(keys))
	for _, key := range keys {
		ok := config.EnvKeyPresent(key)
		detail := "present"
		if !ok {
			detail = "missing"
		}
		// Never print secret values — only the key name and presence.
		out = append(out, checkResult{Name: "env " + key, OK: ok, Detail: detail})
	}
	return out
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func formatBytes(n uint64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)
	switch {
	case n >= gb:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(gb))
	case n >= mb:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(mb))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
