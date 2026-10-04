package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mayank2/internal/config"
	"mayank2/internal/media"
)

// status is the outcome of one doctor check (M2-114 / CONTEXT D24):
// a disabled integration is ⚪, never ❌. Only ❌ fails the run.
type status int

const (
	statusOK status = iota
	statusOff
	statusBroken
)

func (s status) symbol() string {
	switch s {
	case statusOK:
		return "✅"
	case statusOff:
		return "⚪"
	default:
		return "❌"
	}
}

type checkResult struct {
	Name   string
	Status status
	Detail string
}

func okResult(name, detail string) checkResult {
	return checkResult{Name: name, Status: statusOK, Detail: detail}
}
func offResult(name, detail string) checkResult {
	return checkResult{Name: name, Status: statusOff, Detail: detail}
}
func brokenResult(name, detail string) checkResult {
	return checkResult{Name: name, Status: statusBroken, Detail: detail}
}

// docHTTPTimeout bounds every doctor-initiated network probe.
const docHTTPTimeout = 10 * time.Second

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
		checkTailscale(ctx),
	}

	if cfg != nil {
		checks = append(checks, checkLLMProviders(ctx, cfg)...)
		checks = append(checks, checkEmbedRoute(ctx, cfg))
		checks = append(checks, checkDisk(cfg.DataDir))
		checks = append(checks, checkChannels(cfg))
	} else {
		checks = append(checks,
			brokenResult("ollama", "skipped (config not loaded)"),
			brokenResult("disk free >= 100 GB", "skipped (config not loaded)"),
			brokenResult("channel files", "skipped (config not loaded)"),
		)
	}

	checks = append(checks, checkRAM())
	checks = append(checks, checkStockMedia())
	checks = append(checks, checkPublishPlatforms()...)
	checks = append(checks, checkR2())
	checks = append(checks, checkTelegram())
	checks = append(checks, checkOtherEnvKeys(envExample, cfg)...)

	if cfgLoadErr != nil {
		checks = append([]checkResult{brokenResult("config", cfgLoadErr.Error())}, checks...)
	} else {
		checks = append([]checkResult{okResult("config", configPath)}, checks...)
	}

	broken, off := 0, 0
	for _, c := range checks {
		fmt.Printf("%s %s — %s\n", c.Status.symbol(), c.Name, c.Detail)
		switch c.Status {
		case statusBroken:
			broken++
		case statusOff:
			off++
		}
	}
	if broken > 0 {
		fmt.Printf("\ndoctor: %d check(s) broken, %d not configured\n", broken, off)
		return 1
	}
	if off > 0 {
		fmt.Printf("\ndoctor: all checks passed (%d not configured — feature off)\n", off)
		return 0
	}
	fmt.Println("\ndoctor: all checks passed")
	return 0
}

// checkTailscale reports the dashboard's optional Tailscale listener
// (M2-118): the binary not being on PATH is a feature switched off (⚪), not
// broken — ResolveListen skips the "tailscale" sentinel address cleanly and
// the dashboard still serves on 127.0.0.1. Only an installed binary that
// exists but never resolves an IP (e.g. not logged in / not running yet) is
// also reported ⚪ for the same reason; the daemon retries it periodically
// once running. Never ❌ for a plain "not installed".
func checkTailscale(ctx context.Context) checkResult {
	path, err := exec.LookPath("tailscale")
	if err != nil {
		return offResult("tailscale", "not configured (feature off): binary not found in PATH; dashboard still binds 127.0.0.1")
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "tailscale", "ip", "-4").CombinedOutput()
	if err != nil {
		return offResult("tailscale", fmt.Sprintf("not configured (feature off): %s present but `tailscale ip -4` failed (%v) — not logged in / not running?", path, err))
	}
	ip := firstLine(string(out))
	if ip == "" {
		return offResult("tailscale", fmt.Sprintf("not configured (feature off): %s present but returned no IP", path))
	}
	return okResult("tailscale", fmt.Sprintf("%s — ip %s", path, ip))
}

func checkLookPath(ctx context.Context, label, bin string, versionArgs ...string) checkResult {
	path, err := exec.LookPath(bin)
	if err != nil {
		return brokenResult(label, fmt.Sprintf("not found in PATH (%v)", err))
	}
	detail := path
	if len(versionArgs) > 0 {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(cctx, bin, versionArgs...)
		out, err := cmd.CombinedOutput()
		line := firstLine(string(out))
		if err != nil && line == "" {
			return brokenResult(label, fmt.Sprintf("%s: %v", path, err))
		}
		if line != "" {
			detail = line
		}
	}
	return okResult(label, detail)
}

func checkPythonUV(ctx context.Context) checkResult {
	if _, err := exec.LookPath("uv"); err == nil {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(cctx, "uv", "--version").CombinedOutput()
		if err == nil {
			return okResult("uv/python", firstLine(string(out)))
		}
	}
	if path, err := exec.LookPath("python"); err == nil {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(cctx, "python", "--version").CombinedOutput()
		if err == nil {
			return okResult("uv/python", "uv missing; "+firstLine(string(out))+" at "+path)
		}
		return okResult("uv/python", "uv missing; python at "+path)
	}
	if path, err := exec.LookPath("python3"); err == nil {
		return okResult("uv/python", "uv missing; python3 at "+path)
	}
	return brokenResult("uv/python", "neither uv nor python found in PATH")
}

// checkLLMProviders classifies every provider in cfg.LLM.Providers (M2-114 AC1/AC2).
// ollama and openai_compat providers are checked here; claude_cli is left to the
// "claude CLI" core tool check above (its availability is not gated by an env key).
func checkLLMProviders(ctx context.Context, cfg *config.Config) []checkResult {
	names := make([]string, 0, len(cfg.LLM.Providers))
	for name := range cfg.LLM.Providers {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []checkResult
	for _, name := range names {
		pc := cfg.LLM.Providers[name]
		switch pc.Kind {
		case "ollama":
			out = append(out, checkOllamaProvider(ctx, name, pc))
		case "openai_compat":
			out = append(out, checkOpenAICompatProvider(ctx, name, pc))
		case "claude_cli":
			// covered by the "claude CLI" core tool check; not gated by a key.
		}
	}
	return out
}

// checkEmbedRoute reports which provider llm.routes.embed actually resolves
// to (M2-124 / CONTEXT D25: the G2 compliance originality gate needs a real
// embedding provider, and with Ollama not installed it must fall through to
// Gemini's openai_compat /embeddings endpoint). A missing/empty route is a
// real misconfiguration (G2 will fail closed on every script — see
// internal/compliance/g2.go) so it's ❌. Ollama simply not running, or a
// provider with no embed_model / no key, is a candidate being skipped, not
// broken — so a chain with no ready candidate at all is ⚪ "not configured",
// matching D24 ("missing keys switch it off cleanly"), never ❌.
func checkEmbedRoute(ctx context.Context, cfg *config.Config) checkResult {
	const label = "llm embeddings (route)"
	chain := cfg.LLM.Routes["embed"]
	if len(chain) == 0 {
		return brokenResult(label, "llm.routes.embed is missing/empty — the G2 originality gate fails closed on every script without it")
	}

	var notes []string
	for _, name := range chain {
		pc, ok := cfg.LLM.Providers[name]
		if !ok {
			notes = append(notes, name+": unknown provider")
			continue
		}
		if strings.TrimSpace(pc.EmbedModel) == "" {
			notes = append(notes, name+": no embed_model configured")
			continue
		}
		switch pc.Kind {
		case "ollama":
			base := strings.TrimRight(pc.BaseURL, "/")
			if err := config.RequireLoopbackURL(base); err != nil {
				notes = append(notes, name+": "+err.Error())
				continue
			}
			cctx, cancel := context.WithTimeout(ctx, docHTTPTimeout)
			req, err := http.NewRequestWithContext(cctx, http.MethodGet, base+"/api/tags", nil)
			if err != nil {
				cancel()
				notes = append(notes, name+": "+err.Error())
				continue
			}
			resp, err := ollamaHTTPClient().Do(req)
			cancel()
			if err != nil {
				notes = append(notes, name+": not running")
				continue
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				notes = append(notes, fmt.Sprintf("%s: http %d", name, resp.StatusCode))
				continue
			}
			return okResult(label, fmt.Sprintf("resolves to %s (embed_model=%s); chain=%v", name, pc.EmbedModel, chain))
		case "openai_compat":
			if strings.TrimSpace(os.Getenv(pc.KeyEnv)) == "" {
				notes = append(notes, name+": "+pc.KeyEnv+" not set")
				continue
			}
			return okResult(label, fmt.Sprintf("resolves to %s (embed_model=%s); chain=%v", name, pc.EmbedModel, chain))
		default:
			notes = append(notes, name+": kind "+pc.Kind+" does not support embeddings")
		}
	}
	return offResult(label, fmt.Sprintf("not configured (feature off): no provider in chain %v is ready yet (%s) — G2 originality gate fails closed until one is", chain, strings.Join(notes, "; ")))
}

// checkOllamaProvider probes a local Ollama server. Unreachable is ⚪ (optional
// local model, may simply not be running); a security-policy refusal (base_url
// or a redirect off loopback) or a server that's up but missing its configured
// models stays ❌.
func checkOllamaProvider(ctx context.Context, name string, pc config.ProviderConfig) checkResult {
	label := "llm " + name
	base := strings.TrimRight(pc.BaseURL, "/")
	if err := config.RequireLoopbackURL(base); err != nil {
		return brokenResult(label, err.Error())
	}
	cctx, cancel := context.WithTimeout(ctx, docHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, base+"/api/tags", nil)
	if err != nil {
		return brokenResult(label, err.Error())
	}
	resp, err := ollamaHTTPClient().Do(req)
	if err != nil {
		if strings.Contains(err.Error(), "not loopback") {
			return brokenResult(label, fmt.Sprintf("refused: %v", err))
		}
		return offResult(label, fmt.Sprintf("not configured (feature off): unreachable at %s (start Ollama to enable local fallback): %v", base, err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return brokenResult(label, fmt.Sprintf("%s: HTTP %d", base, resp.StatusCode))
	}
	var body struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return brokenResult(label, fmt.Sprintf("decode tags: %v", err))
	}
	pulled := map[string]bool{}
	var names []string
	for _, m := range body.Models {
		pulled[m.Name] = true
		if i := strings.IndexByte(m.Name, ':'); i > 0 {
			pulled[m.Name[:i]] = true
		}
		names = append(names, m.Name)
	}
	var missing []string
	if pc.Model != "" && !modelPulled(pulled, pc.Model) {
		missing = append(missing, pc.Model)
	}
	if pc.EmbedModel != "" && !modelPulled(pulled, pc.EmbedModel) {
		missing = append(missing, pc.EmbedModel)
	}
	if len(missing) > 0 {
		return brokenResult(label, fmt.Sprintf("reachable but missing models %v (pulled: %v)", missing, names))
	}
	return okResult(label, fmt.Sprintf("reachable; %s and %s present", pc.Model, pc.EmbedModel))
}

// checkOpenAICompatProvider is ⚪ when its key_env is empty, else it makes a
// lightweight GET {base_url}/models with the key to prove it actually works
// (never logs the key itself). 401/403 -> ❌ "key rejected"; any other error
// or non-2xx -> ❌ "broken" (configured but not working).
func checkOpenAICompatProvider(ctx context.Context, name string, pc config.ProviderConfig) checkResult {
	label := "llm " + name
	key := strings.TrimSpace(os.Getenv(pc.KeyEnv))
	if key == "" {
		return offResult(label, fmt.Sprintf("not configured (feature off): set %s to enable", pc.KeyEnv))
	}
	cctx, cancel := context.WithTimeout(ctx, docHTTPTimeout)
	defer cancel()
	reqURL := strings.TrimRight(pc.BaseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return brokenResult(label, fmt.Sprintf("build request: %v", err))
	}
	req.Header.Set("Authorization", "Bearer "+key)
	// Never log Authorization or the key.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return brokenResult(label, fmt.Sprintf("unreachable: %v", stripKey(err.Error(), key)))
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return okResult(label, fmt.Sprintf("key ok; model=%s", pc.Model))
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return brokenResult(label, fmt.Sprintf("key rejected: http %d", resp.StatusCode))
	default:
		return brokenResult(label, fmt.Sprintf("http %d", resp.StatusCode))
	}
}

// stripKey defends against a key value leaking into a wrapped *url.Error (which
// includes the request URL; our URLs never carry the key, but headers/errors
// from custom transports might echo it back).
func stripKey(msg, key string) string {
	if key == "" {
		return msg
	}
	return strings.ReplaceAll(msg, key, "<redacted>")
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
		return brokenResult("disk free >= 100 GB", err.Error())
	}
	const need = 100 * 1024 * 1024 * 1024
	detail := fmt.Sprintf("%s free / %s total on volume for %s", formatBytes(free), formatBytes(total), dataDir)
	if free < need {
		return brokenResult("disk free >= 100 GB", detail)
	}
	return okResult("disk free >= 100 GB", detail)
}

func checkRAM() checkResult {
	total, err := totalRAMBytes()
	if err != nil {
		return brokenResult("RAM", err.Error())
	}
	return okResult("RAM", formatBytes(total)+" total")
}

func checkChannels(cfg *config.Config) checkResult {
	n := len(cfg.Channels)
	if n == 0 {
		return brokenResult("channel files", fmt.Sprintf("no channels in %s", cfg.Content.ChannelsDir))
	}
	ids := make([]string, 0, n)
	for _, ch := range cfg.Channels {
		ids = append(ids, ch.ID)
	}
	return okResult("channel files", fmt.Sprintf("%d valid (%s)", n, strings.Join(ids, ", ")))
}

// checkStockMedia reports Pexels/Pixabay as one integration (D24): either key
// enables it; neither is ⚪, not a crash (internal/media.NewStockFromEnv already
// returns a typed *NotConfiguredError in that case — this just surfaces it early).
func checkStockMedia() checkResult {
	pexels := envSet(media.PexelsKeyEnv)
	pixabay := envSet(media.PixabayKeyEnv)
	switch {
	case pexels && pixabay:
		return okResult("stock media", fmt.Sprintf("%s and %s set", media.PexelsKeyEnv, media.PixabayKeyEnv))
	case pexels:
		return okResult("stock media", fmt.Sprintf("%s set (%s not set)", media.PexelsKeyEnv, media.PixabayKeyEnv))
	case pixabay:
		return okResult("stock media", fmt.Sprintf("%s set (%s not set)", media.PixabayKeyEnv, media.PexelsKeyEnv))
	default:
		return offResult("stock media", fmt.Sprintf("not configured (feature off): set %s or %s", media.PexelsKeyEnv, media.PixabayKeyEnv))
	}
}

// platformKeyPair is one publish platform's OAuth client id/secret env names.
type platformKeyPair struct {
	label, idEnv, secretEnv string
}

var publishPlatforms = []platformKeyPair{
	{"publish youtube (google oauth)", "GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET"},
	{"publish meta (instagram/facebook)", "META_APP_ID", "META_APP_SECRET"},
	{"publish x", "X_CLIENT_ID", "X_CLIENT_SECRET"},
	{"publish pinterest", "PINTEREST_APP_ID", "PINTEREST_APP_SECRET"},
	{"publish linkedin", "LINKEDIN_CLIENT_ID", "LINKEDIN_CLIENT_SECRET"},
}

func checkPublishPlatforms() []checkResult {
	out := make([]checkResult, 0, len(publishPlatforms))
	for _, p := range publishPlatforms {
		out = append(out, checkKeyPair(p.label, p.idEnv, p.secretEnv))
	}
	return out
}

// checkKeyPair reports a two-key integration: both set -> ✅, neither -> ⚪
// (feature off), exactly one -> ❌ (a key was entered but the pair is
// incomplete, so the integration cannot actually be used).
func checkKeyPair(label string, envKeys ...string) checkResult {
	present := make([]bool, len(envKeys))
	anySet, allSet := false, true
	for i, k := range envKeys {
		present[i] = envSet(k)
		if present[i] {
			anySet = true
		} else {
			allSet = false
		}
	}
	switch {
	case allSet:
		return okResult(label, fmt.Sprintf("%s set", strings.Join(envKeys, ", ")))
	case !anySet:
		return offResult(label, fmt.Sprintf("not configured (feature off): set %s", strings.Join(envKeys, ", ")))
	default:
		var missing []string
		for i, k := range envKeys {
			if !present[i] {
				missing = append(missing, k)
			}
		}
		return brokenResult(label, fmt.Sprintf("incomplete: missing %s", strings.Join(missing, ", ")))
	}
}

func checkR2() checkResult {
	return checkKeyPair("storage r2", "R2_ACCOUNT_ID", "R2_ACCESS_KEY_ID", "R2_SECRET_ACCESS_KEY", "R2_BUCKET")
}

// checkTelegram: missing is ⚪ with the note that approvals still work on the
// dashboard (M2-114 AC6; default behavior, Mayank may change it).
func checkTelegram() checkResult {
	keys := []string{"TELEGRAM_BOT_TOKEN", "TELEGRAM_USER_ID", "TELEGRAM_CHAT_ID"}
	allSet, anySet := true, false
	for _, k := range keys {
		if envSet(k) {
			anySet = true
		} else {
			allSet = false
		}
	}
	switch {
	case allSet:
		return okResult("telegram", strings.Join(keys, ", ")+" set")
	case !anySet:
		return offResult("telegram", "not configured (feature off): approvals only on dashboard; set "+strings.Join(keys, ", ")+" to enable")
	default:
		var missing []string
		for _, k := range keys {
			if !envSet(k) {
				missing = append(missing, k)
			}
		}
		return brokenResult("telegram", "incomplete: missing "+strings.Join(missing, ", "))
	}
}

// coveredEnvKeys lists every env key already reported by a dedicated check
// above, so the generic fallback below doesn't double-report them.
func coveredEnvKeys(cfg *config.Config) map[string]bool {
	covered := map[string]bool{
		media.PexelsKeyEnv:  true,
		media.PixabayKeyEnv: true,
		"R2_ACCOUNT_ID":     true, "R2_ACCESS_KEY_ID": true, "R2_SECRET_ACCESS_KEY": true, "R2_BUCKET": true,
		"TELEGRAM_BOT_TOKEN": true, "TELEGRAM_USER_ID": true, "TELEGRAM_CHAT_ID": true,
	}
	for _, p := range publishPlatforms {
		covered[p.idEnv] = true
		covered[p.secretEnv] = true
	}
	if cfg != nil {
		for _, pc := range cfg.LLM.Providers {
			if pc.KeyEnv != "" {
				covered[pc.KeyEnv] = true
			}
		}
	}
	return covered
}

// checkOtherEnvKeys is a fallback presence check for any .env.example key this
// ticket doesn't special-case (e.g. DASHBOARD_TOKEN): present -> ✅, absent ->
// ⚪ (never ❌ — a missing optional key is a feature switched off, not broken).
func checkOtherEnvKeys(examplePath string, cfg *config.Config) []checkResult {
	keys, err := config.RequiredEnvKeys(examplePath)
	if err != nil {
		return []checkResult{brokenResult(".env keys", err.Error())}
	}
	covered := coveredEnvKeys(cfg)
	out := make([]checkResult, 0, len(keys))
	for _, key := range keys {
		if covered[key] {
			continue
		}
		if envSet(key) {
			out = append(out, okResult("env "+key, "present"))
		} else {
			out = append(out, offResult("env "+key, "not configured (feature off)"))
		}
	}
	return out
}

// envSet reports whether key is set to a non-empty value. Never returns or
// logs the value itself.
func envSet(key string) bool {
	return strings.TrimSpace(os.Getenv(key)) != ""
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
