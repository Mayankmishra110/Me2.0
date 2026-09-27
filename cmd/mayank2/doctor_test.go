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

	cfg := &config.Config{
		LLM: config.LLMConfig{
			Providers: map[string]config.ProviderConfig{
				"ollama": {
					Kind:       "ollama",
					BaseURL:    srv.URL,
					Model:      "qwen",
					EmbedModel: "nomic-embed-text",
				},
			},
		},
	}
	got := checkOllama(context.Background(), cfg)
	if !got.OK {
		t.Fatalf("want OK, got %+v", got)
	}
}

func TestCheckOllama_refusesNonLoopbackBeforeHTTP(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{
		LLM: config.LLMConfig{
			Providers: map[string]config.ProviderConfig{
				"ollama": {
					Kind:    "ollama",
					BaseURL: "http://example.com:11434",
					Model:   "qwen",
				},
			},
		},
	}
	got := checkOllama(context.Background(), cfg)
	if got.OK {
		t.Fatalf("want fail, got %+v", got)
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

	cfg := &config.Config{
		LLM: config.LLMConfig{
			Providers: map[string]config.ProviderConfig{
				"ollama": {
					Kind:    "ollama",
					BaseURL: srv.URL,
					Model:   "qwen",
				},
			},
		},
	}
	got := checkOllama(context.Background(), cfg)
	if got.OK {
		t.Fatalf("want fail on off-loopback redirect, got %+v", got)
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
