package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mayank2/internal/config"
)

// TestAskOnce_geminiOnly proves the `mayank2 llm ask` code path end to end
// against a fake Gemini-shaped OpenAI-compatible server: only GEMINI_API_KEY
// is set, and the router must pick gemini and return its answer (M2-114 AC3).
// This does not hit the real Gemini API — see the ticket notes for whether a
// live call was exercised in this environment.
func TestAskOnce_geminiOnly(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "gemini-test-model",
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": "hello from gemini"}},
			},
		})
	}))
	defer srv.Close()

	t.Setenv("GEMINI_API_KEY", "fake-gemini-key-not-real")

	cfg := &config.Config{
		LLM: config.LLMConfig{
			Providers: map[string]config.ProviderConfig{
				"gemini": {Kind: "openai_compat", BaseURL: srv.URL, KeyEnv: "GEMINI_API_KEY", Model: "gemini-x"},
			},
			Routes: map[string][]string{
				"script": {"gemini"},
			},
		},
	}

	resp, err := askOnce(context.Background(), cfg, "script", "say hello")
	if err != nil {
		t.Fatalf("askOnce: %v", err)
	}
	if resp.Provider != "gemini" {
		t.Fatalf("provider = %q, want gemini", resp.Provider)
	}
	if resp.Model != "gemini-test-model" {
		t.Fatalf("model = %q", resp.Model)
	}
	if resp.Text != "hello from gemini" {
		t.Fatalf("text = %q", resp.Text)
	}
	if gotAuth != "Bearer fake-gemini-key-not-real" {
		t.Fatalf("authorization header not sent correctly")
	}
}

// TestAskOnce_noProvider proves the router (not this CLI) is what skips an
// unconfigured task/route — no keys, no crash, just a clear error (AC1).
func TestAskOnce_noProvider(t *testing.T) {
	cfg := &config.Config{
		LLM: config.LLMConfig{
			Providers: map[string]config.ProviderConfig{
				"gemini": {Kind: "openai_compat", BaseURL: "http://127.0.0.1:1", KeyEnv: "GEMINI_API_KEY", Model: "gemini-x"},
			},
			Routes: map[string][]string{
				"script": {"gemini"},
			},
		},
	}
	_, err := askOnce(context.Background(), cfg, "script", "say hello")
	if err == nil {
		t.Fatal("want error when no key is set, got nil")
	}
	if !strings.Contains(err.Error(), "no available provider") {
		t.Fatalf("err = %v, want it to mention no available provider", err)
	}
}

func TestCmdLLMAsk_parsesFlagsAndRequiresPrompt(t *testing.T) {
	// No prompt at all -> usage error, exit 2. This exercises the flag parser
	// in cmdLLMAsk without touching the network or a real config file.
	got := cmdLLMAsk(context.Background(), []string{"-task", "script"})
	if got != 2 {
		t.Fatalf("exit code = %d, want 2 (missing prompt)", got)
	}
}
