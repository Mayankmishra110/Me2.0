package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"mayank2/internal/config"
)

func TestOllamaCheckRedirect_refusesNonLoopback(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://example.com/api/tags", nil)
	if err != nil {
		t.Fatal(err)
	}
	err = ollamaCheckRedirect(req, []*http.Request{req})
	if err == nil || !strings.Contains(err.Error(), "not loopback") {
		t.Fatalf("want not loopback error, got %v", err)
	}
}

func TestOllamaCheckRedirect_allowsLoopback(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:9/api/tags", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := ollamaCheckRedirect(req, nil); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestCheckOllama_okOnLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"qwen:latest"},{"name":"nomic-embed-text:latest"}]}`))
	}))
	defer srv.Close()

	pc := config.ProviderConfig{
		Kind:       "ollama",
		BaseURL:    srv.URL,
		Model:      "qwen",
		EmbedModel: "nomic-embed-text",
	}
	got := checkOllamaProvider(context.Background(), "ollama", pc)
	if got.Status != statusOK {
		t.Fatalf("want statusOK, got %+v", got)
	}
}

func TestCheckOllama_refusesNonLoopbackBeforeHTTP(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	pc := config.ProviderConfig{
		Kind:    "ollama",
		BaseURL: "http://example.com:11434",
		Model:   "qwen",
	}
	got := checkOllamaProvider(context.Background(), "ollama", pc)
	if got.Status != statusBroken {
		t.Fatalf("want statusBroken, got %+v", got)
	}
	if !strings.Contains(got.Detail, "not loopback") {
		t.Fatalf("detail=%q", got.Detail)
	}
	if called {
		t.Fatal("must not HTTP to server when base_url is non-loopback")
	}
}

func TestCheckOllama_refusesRedirectOffLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/api/tags", http.StatusFound)
	}))
	defer srv.Close()

	pc := config.ProviderConfig{
		Kind:    "ollama",
		BaseURL: srv.URL,
		Model:   "qwen",
	}
	got := checkOllamaProvider(context.Background(), "ollama", pc)
	if got.Status != statusBroken {
		t.Fatalf("want statusBroken on off-loopback redirect, got %+v", got)
	}
	if !strings.Contains(got.Detail, "not loopback") && !strings.Contains(got.Detail, "redirect") {
		t.Fatalf("detail=%q", got.Detail)
	}
	// Confirm httptest URL itself is loopback so failure is from redirect policy.
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.RequireLoopbackURL(srv.URL); err != nil {
		t.Fatalf("test server should be loopback (%s): %v", u.Hostname(), err)
	}
}

// TestCheckOllama_offWhenUnreachable: a local Ollama that simply isn't running
// is ⚪ (feature off), not ❌ — it's optional local infra, not a broken
// integration (M2-114 AC1/AC2).
func TestCheckOllama_offWhenUnreachable(t *testing.T) {
	pc := config.ProviderConfig{
		Kind:    "ollama",
		BaseURL: "http://127.0.0.1:1", // loopback, nothing listening: connection refused
		Model:   "qwen",
	}
	got := checkOllamaProvider(context.Background(), "ollama", pc)
	if got.Status != statusOff {
		t.Fatalf("want statusOff, got %+v", got)
	}
}

// TestDoctor_table covers M2-114 AC5/AC6 with the classification functions
// directly (no real ffmpeg/node/git/claude/ollama dependency, so the result is
// deterministic regardless of what's installed on the machine running the test).
func TestDoctor_table(t *testing.T) {
	llmCfg := func(geminiBaseURL string) *config.Config {
		return &config.Config{
			LLM: config.LLMConfig{
				Providers: map[string]config.ProviderConfig{
					"ollama": {Kind: "ollama", BaseURL: "http://127.0.0.1:1", Model: "qwen", EmbedModel: "nomic-embed-text"},
					"groq":   {Kind: "openai_compat", BaseURL: "http://127.0.0.1:1", KeyEnv: "GROQ_API_KEY", Model: "g"},
					"gemini": {Kind: "openai_compat", BaseURL: geminiBaseURL, KeyEnv: "GEMINI_API_KEY", Model: "gemini-x"},
					"claude": {Kind: "claude_cli", Bin: "claude"},
				},
			},
		}
	}

	t.Run("no keys -> all off, exit 0", func(t *testing.T) {
		checks := checkLLMProviders(context.Background(), llmCfg("http://127.0.0.1:1"))
		checks = append(checks, checkStockMedia(), checkR2(), checkTelegram())
		checks = append(checks, checkPublishPlatforms()...)
		for _, c := range checks {
			if c.Status != statusOff {
				t.Fatalf("check %q: want statusOff, got %+v", c.Name, c)
			}
		}
		if got := doctorExitCode(checks); got != 0 {
			t.Fatalf("exit code = %d, want 0", got)
		}
	})

	t.Run("gemini only -> gemini enabled, others off", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":[{"id":"gemini-x"}]}`))
		}))
		defer srv.Close()
		t.Setenv("GEMINI_API_KEY", "fake-test-key-not-real")

		checks := checkLLMProviders(context.Background(), llmCfg(srv.URL))
		var gemini, groq, ollama checkResult
		for _, c := range checks {
			switch c.Name {
			case "llm gemini":
				gemini = c
			case "llm groq":
				groq = c
			case "llm ollama":
				ollama = c
			}
		}
		if gemini.Status != statusOK {
			t.Fatalf("gemini: want statusOK, got %+v", gemini)
		}
		if groq.Status != statusOff {
			t.Fatalf("groq: want statusOff (no key), got %+v", groq)
		}
		if ollama.Status != statusOff {
			t.Fatalf("ollama: want statusOff (unreachable), got %+v", ollama)
		}
		if got := doctorExitCode(checks); got != 0 {
			t.Fatalf("exit code = %d, want 0", got)
		}
	})

	t.Run("gemini key rejected (401) -> broken, exit 1", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
		}))
		defer srv.Close()
		t.Setenv("GEMINI_API_KEY", "fake-rejected-key-not-real")

		checks := checkLLMProviders(context.Background(), llmCfg(srv.URL))
		var gemini checkResult
		for _, c := range checks {
			if c.Name == "llm gemini" {
				gemini = c
			}
		}
		if gemini.Status != statusBroken {
			t.Fatalf("gemini: want statusBroken, got %+v", gemini)
		}
		if !strings.Contains(gemini.Detail, "401") {
			t.Fatalf("detail should mention the rejected status, got %q", gemini.Detail)
		}
		if got := doctorExitCode(checks); got != 1 {
			t.Fatalf("exit code = %d, want 1", got)
		}
	})

	t.Run("telegram missing -> off with dashboard note", func(t *testing.T) {
		got := checkTelegram()
		if got.Status != statusOff {
			t.Fatalf("want statusOff, got %+v", got)
		}
		if !strings.Contains(got.Detail, "approvals only on dashboard") {
			t.Fatalf("detail should note dashboard fallback, got %q", got.Detail)
		}
	})

	t.Run("telegram partial -> broken", func(t *testing.T) {
		t.Setenv("TELEGRAM_BOT_TOKEN", "fake-token-not-real")
		got := checkTelegram()
		if got.Status != statusBroken {
			t.Fatalf("want statusBroken, got %+v", got)
		}
	})
}

// TestCheckEmbedRoute covers M2-124 / CONTEXT D25: with no Ollama installed,
// doctor must show the embedding provider status, never fail with ❌ just
// because Ollama isn't running, and fail loudly (❌) only when the route
// itself is missing (which would make G2 fail closed on every script).
func TestCheckEmbedRoute(t *testing.T) {
	baseProviders := func(geminiBaseURL, geminiEmbedModel string) map[string]config.ProviderConfig {
		return map[string]config.ProviderConfig{
			"ollama": {Kind: "ollama", BaseURL: "http://127.0.0.1:1", Model: "qwen", EmbedModel: "nomic-embed-text"},
			"gemini": {Kind: "openai_compat", BaseURL: geminiBaseURL, KeyEnv: "GEMINI_API_KEY", Model: "gemini-x", EmbedModel: geminiEmbedModel},
		}
	}

	t.Run("missing route -> broken", func(t *testing.T) {
		cfg := &config.Config{LLM: config.LLMConfig{
			Providers: baseProviders("http://127.0.0.1:1", "gemini-embedding-001"),
			Routes:    map[string][]string{},
		}}
		got := checkEmbedRoute(context.Background(), cfg)
		if got.Status != statusBroken {
			t.Fatalf("want statusBroken, got %+v", got)
		}
	})

	t.Run("no ollama running, no gemini key -> off, not broken", func(t *testing.T) {
		t.Setenv("GEMINI_API_KEY", "")
		cfg := &config.Config{LLM: config.LLMConfig{
			Providers: baseProviders("http://127.0.0.1:1", "gemini-embedding-001"),
			Routes:    map[string][]string{"embed": {"ollama", "gemini"}},
		}}
		got := checkEmbedRoute(context.Background(), cfg)
		if got.Status != statusOff {
			t.Fatalf("want statusOff (Ollama not installed is a feature-off, not broken), got %+v", got)
		}
	})

	t.Run("gemini key set, no ollama -> ok, resolves to gemini", func(t *testing.T) {
		t.Setenv("GEMINI_API_KEY", "fake-test-key-not-real")
		cfg := &config.Config{LLM: config.LLMConfig{
			Providers: baseProviders("http://127.0.0.1:1", "gemini-embedding-001"),
			Routes:    map[string][]string{"embed": {"ollama", "gemini"}},
		}}
		got := checkEmbedRoute(context.Background(), cfg)
		if got.Status != statusOK {
			t.Fatalf("want statusOK, got %+v", got)
		}
		if !strings.Contains(got.Detail, "gemini") || !strings.Contains(got.Detail, "gemini-embedding-001") {
			t.Fatalf("detail should name the resolved provider and model, got %q", got.Detail)
		}
	})

	t.Run("ollama reachable -> ok, resolves to ollama first", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[{"name":"nomic-embed-text:latest"}]}`))
		}))
		defer srv.Close()
		t.Setenv("GEMINI_API_KEY", "fake-test-key-not-real")

		cfg := &config.Config{LLM: config.LLMConfig{
			Providers: map[string]config.ProviderConfig{
				"ollama": {Kind: "ollama", BaseURL: srv.URL, Model: "qwen", EmbedModel: "nomic-embed-text"},
				"gemini": {Kind: "openai_compat", BaseURL: "http://127.0.0.1:1", KeyEnv: "GEMINI_API_KEY", Model: "gemini-x", EmbedModel: "gemini-embedding-001"},
			},
			Routes: map[string][]string{"embed": {"ollama", "gemini"}},
		}}
		got := checkEmbedRoute(context.Background(), cfg)
		if got.Status != statusOK {
			t.Fatalf("want statusOK, got %+v", got)
		}
		if !strings.Contains(got.Detail, "resolves to ollama") {
			t.Fatalf("detail should resolve to ollama (first in chain and reachable), got %q", got.Detail)
		}
	})

	t.Run("no embed_model anywhere -> off", func(t *testing.T) {
		t.Setenv("GEMINI_API_KEY", "fake-test-key-not-real")
		cfg := &config.Config{LLM: config.LLMConfig{
			Providers: baseProviders("http://127.0.0.1:1", ""), // gemini configured but no embed_model
			Routes:    map[string][]string{"embed": {"ollama", "gemini"}},
		}}
		got := checkEmbedRoute(context.Background(), cfg)
		if got.Status != statusOff {
			t.Fatalf("want statusOff, got %+v", got)
		}
	})
}

// doctorExitCode mirrors cmdDoctor's exit-code policy: non-zero iff any check
// is broken; "off" never fails the run.
func doctorExitCode(checks []checkResult) int {
	for _, c := range checks {
		if c.Status == statusBroken {
			return 1
		}
	}
	return 0
}
