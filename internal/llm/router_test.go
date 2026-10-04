package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mayank2/internal/config"
)

func TestCompleteFallthroughOn429(t *testing.T) {
	var groqHits, cerebrasHits atomic.Int32
	groq := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		groqHits.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate"}`))
	}))
	defer groq.Close()

	cerebras := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cerebrasHits.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "cerebras-test",
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": "ok-from-cerebras"}},
			},
		})
	}))
	defer cerebras.Close()

	t.Setenv("GROQ_API_KEY", "test-groq-key-not-real")
	t.Setenv("CEREBRAS_API_KEY", "test-cerebras-key-not-real")

	cfg := config.LLMConfig{
		Providers: map[string]config.ProviderConfig{
			"groq":     {Kind: "openai_compat", BaseURL: groq.URL, KeyEnv: "GROQ_API_KEY", Model: "g"},
			"cerebras": {Kind: "openai_compat", BaseURL: cerebras.URL, KeyEnv: "CEREBRAS_API_KEY", Model: "c"},
		},
		Routes: map[string][]string{
			"research": {"groq", "cerebras"},
		},
	}
	r, err := New(cfg, WithHTTPClient(http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}

	resp, err := r.Complete(context.Background(), TaskResearch, Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Text != "ok-from-cerebras" {
		t.Fatalf("text=%q", resp.Text)
	}
	if resp.Provider != "cerebras" || resp.Model != "cerebras-test" {
		t.Fatalf("provider/model = %s/%s", resp.Provider, resp.Model)
	}
	if groqHits.Load() != 1 || cerebrasHits.Load() != 1 {
		t.Fatalf("hits groq=%d cerebras=%d", groqHits.Load(), cerebrasHits.Load())
	}

	// Groq should now be marked unavailable; second call skips it.
	resp2, err := r.Complete(context.Background(), TaskResearch, Request{
		Messages: []Message{{Role: "user", Content: "again"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp2.Provider != "cerebras" {
		t.Fatalf("expected cerebras, got %s", resp2.Provider)
	}
	if groqHits.Load() != 1 {
		t.Fatalf("groq should stay skipped, hits=%d", groqHits.Load())
	}
	if cerebrasHits.Load() != 2 {
		t.Fatalf("cerebras hits=%d", cerebrasHits.Load())
	}
}

func TestQuotaResetsAfterWindow(t *testing.T) {
	now := time.Date(2026, 9, 28, 1, 0, 0, 0, time.UTC)
	q := NewQuotaTracker()
	q.SetClock(func() time.Time { return now })

	q.MarkUnavailable("groq", now.Add(time.Hour), "429")
	if q.Available("groq") {
		t.Fatal("expected unavailable")
	}
	now = now.Add(time.Hour)
	if !q.Available("groq") {
		t.Fatal("expected available after window")
	}
}

func TestOllamaLowQualityFlag(t *testing.T) {
	var hostHits atomic.Int32
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hostHits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer host.Close()

	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "local-8b",
			"message": map[string]string{"role": "assistant", "content": "local-ok"},
		})
	}))
	defer ollama.Close()

	t.Setenv("GROQ_API_KEY", "test-key")
	cfg := config.LLMConfig{
		Providers: map[string]config.ProviderConfig{
			"groq":   {Kind: "openai_compat", BaseURL: host.URL, KeyEnv: "GROQ_API_KEY", Model: "g"},
			"ollama": {Kind: "ollama", BaseURL: ollama.URL, Model: "local-8b"},
		},
		Routes: map[string][]string{"research": {"groq", "ollama"}},
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := r.Complete(context.Background(), TaskResearch, Request{
		Messages: []Message{{Role: "user", Content: "x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.LowQuality {
		t.Fatal("expected LowQuality when falling through to ollama")
	}
	if resp.Provider != "ollama" {
		t.Fatalf("provider=%s", resp.Provider)
	}
}

func TestContentTaskSkipsClaude(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "local",
			"message": map[string]string{"content": "from-ollama"},
		})
	}))
	defer ollama.Close()

	cfg := config.LLMConfig{
		Providers: map[string]config.ProviderConfig{
			"claude": {Kind: "claude_cli", Bin: "claude"},
			"ollama": {Kind: "ollama", BaseURL: ollama.URL, Model: "m"},
		},
		// Misconfigured content route that lists claude first — must skip it.
		Routes: map[string][]string{"research": {"claude", "ollama"}},
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Make claude look available so we prove the content guard, not LookPath.
	cp := r.providers["claude"].(*claudeCLIProvider)
	cp.lookPath = func(string) (string, error) { return "/fake/claude", nil }
	cp.run = func(ctx context.Context, bin string, args []string, stdin string) ([]byte, []byte, error) {
		t.Fatal("claude_cli must not run for content tasks")
		return nil, nil, nil
	}

	resp, err := r.Complete(context.Background(), TaskResearch, Request{
		Messages: []Message{{Role: "user", Content: "x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Provider != "ollama" || resp.Text != "from-ollama" {
		t.Fatalf("got %+v", resp)
	}
}

func TestJSONSchemaRepairRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		_ = body
		content := `not-json`
		if n >= 2 {
			content = `{"ok":true}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "m",
			"choices": []map[string]any{
				{"message": map[string]string{"content": content}},
			},
		})
	}))
	defer srv.Close()

	t.Setenv("GROQ_API_KEY", "k")
	cfg := config.LLMConfig{
		Providers: map[string]config.ProviderConfig{
			"groq": {Kind: "openai_compat", BaseURL: srv.URL, KeyEnv: "GROQ_API_KEY", Model: "m"},
		},
		Routes: map[string][]string{"classify": {"groq"}},
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	schema := json.RawMessage(`{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}}}`)
	resp, err := r.Complete(context.Background(), TaskClassify, Request{
		Messages:   []Message{{Role: "user", Content: "x"}},
		JSONSchema: schema,
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 calls (initial+repair), got %d", calls.Load())
	}
	if resp.Text != `{"ok":true}` {
		t.Fatalf("text=%q", resp.Text)
	}
}

func TestOllamaEmbed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embeddings" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embedding": []float64{0.1, 0.2, 0.3},
		})
	}))
	defer srv.Close()

	cfg := config.LLMConfig{
		Providers: map[string]config.ProviderConfig{
			"ollama": {Kind: "ollama", BaseURL: srv.URL, Model: "m", EmbedModel: "nomic-embed-text"},
		},
		Routes: map[string][]string{"embed": {"ollama"}},
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Embed(context.Background(), EmbedRequest{Texts: []string{"hello"}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Provider != "ollama" || out.Model != "nomic-embed-text" {
		t.Fatalf("%+v", out)
	}
	if len(out.Vectors) != 1 || len(out.Vectors[0]) != 3 {
		t.Fatalf("vectors=%v", out.Vectors)
	}
}

// TestGeminiOnlyEmbed proves embeddings resolve to the openai_compat provider
// (Gemini) against a fake /embeddings endpoint when no Ollama is configured —
// CONTEXT D25 / M2-124: the machine runs on GEMINI_API_KEY alone.
func TestGeminiOnlyEmbed(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "gemini-embedding-001",
			"data": []map[string]any{
				{"index": 0, "embedding": []float64{0.1, 0.2, 0.3}},
				{"index": 1, "embedding": []float64{0.4, 0.5, 0.6}},
			},
		})
	}))
	defer srv.Close()

	t.Setenv("GEMINI_API_KEY", "test-key")
	cfg := config.LLMConfig{
		Providers: map[string]config.ProviderConfig{
			"gemini": {Kind: "openai_compat", BaseURL: srv.URL, KeyEnv: "GEMINI_API_KEY", Model: "gemini-3.8-flash", EmbedModel: "gemini-embedding-001"},
		},
		Routes: map[string][]string{"embed": {"gemini"}},
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Embed(context.Background(), EmbedRequest{Texts: []string{"first", "second"}})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/embeddings" {
		t.Fatalf("path=%s, want /embeddings", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("auth=%q", gotAuth)
	}
	if gotBody["model"] != "gemini-embedding-001" {
		t.Fatalf("body model=%v", gotBody["model"])
	}
	if out.Provider != "gemini" || out.Model != "gemini-embedding-001" {
		t.Fatalf("%+v", out)
	}
	if len(out.Vectors) != 2 || len(out.Vectors[0]) != 3 || len(out.Vectors[1]) != 3 {
		t.Fatalf("vectors=%v", out.Vectors)
	}
	if out.Vectors[0][0] != 0.1 || out.Vectors[1][0] != 0.4 {
		t.Fatalf("vector order wrong: %v", out.Vectors)
	}
}

// TestEmbedNoProviderAvailable proves Router.Embed returns ErrNoProvider
// (so G2 fails closed — see TestG2_priorsWithoutEmbedFailsClosed) when no
// embedding-capable provider is available, e.g. no Ollama and no Gemini key
// set (CONTEXT D25 / M2-124).
func TestEmbedNoProviderAvailable(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "") // empty → unavailable
	cfg := config.LLMConfig{
		Providers: map[string]config.ProviderConfig{
			"ollama": {Kind: "ollama", BaseURL: "http://127.0.0.1:1", Model: "m", EmbedModel: "nomic-embed-text"},
			"gemini": {Kind: "openai_compat", BaseURL: "https://example.invalid", KeyEnv: "GEMINI_API_KEY", Model: "g", EmbedModel: "gemini-embedding-001"},
		},
		Routes: map[string][]string{"embed": {"ollama", "gemini"}},
	}
	r, err := New(cfg, WithQuotas(NewQuotaTracker())) // ollama's Available() only checks quotas, so it "passes" availability but fails the call
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Embed(context.Background(), EmbedRequest{Texts: []string{"x"}})
	if err == nil {
		t.Fatal("expected error when no embedding provider is available")
	}
	if !errors.Is(err, ErrNoProvider) {
		t.Fatalf("expected ErrNoProvider, got %v", err)
	}
}

// TestEmbedRouteMissing proves an empty/missing "embed" route fails closed
// with ErrNoProvider rather than silently falling back to any provider.
func TestEmbedRouteMissing(t *testing.T) {
	cfg := config.LLMConfig{
		Providers: map[string]config.ProviderConfig{
			"ollama": {Kind: "ollama", BaseURL: "http://127.0.0.1:1", Model: "m", EmbedModel: "nomic-embed-text"},
		},
		Routes: map[string][]string{},
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Embed(context.Background(), EmbedRequest{Texts: []string{"x"}})
	if !errors.Is(err, ErrNoProvider) {
		t.Fatalf("expected ErrNoProvider, got %v", err)
	}
}

// TestEmbedSkipsNonEmbeddingProvider proves a provider kind that doesn't
// implement embeddings (claude_cli) is skipped with a clear error, not a
// panic or a silent pass, when listed on the embed route by mistake.
func TestEmbedSkipsNonEmbeddingProvider(t *testing.T) {
	cfg := config.LLMConfig{
		Providers: map[string]config.ProviderConfig{
			"claude": {Kind: "claude_cli", Bin: "claude"},
		},
		Routes: map[string][]string{"embed": {"claude"}},
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Embed(context.Background(), EmbedRequest{Texts: []string{"x"}})
	if !errors.Is(err, ErrNoProvider) {
		t.Fatalf("expected ErrNoProvider, got %v", err)
	}
	if !strings.Contains(err.Error(), "does not support embeddings") {
		t.Fatalf("expected 'does not support embeddings' detail, got %v", err)
	}
}

func TestMissingKeySkipsProvider(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "m",
			"message": map[string]string{"content": "ok"},
		})
	}))
	defer ollama.Close()

	t.Setenv("GROQ_API_KEY", "") // empty → unavailable
	cfg := config.LLMConfig{
		Providers: map[string]config.ProviderConfig{
			"groq":   {Kind: "openai_compat", BaseURL: "http://127.0.0.1:9", KeyEnv: "GROQ_API_KEY", Model: "g"},
			"ollama": {Kind: "ollama", BaseURL: ollama.URL, Model: "m"},
		},
		Routes: map[string][]string{"metadata": {"groq", "ollama"}},
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := r.Complete(context.Background(), TaskMetadata, Request{
		Messages: []Message{{Role: "user", Content: "x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Provider != "ollama" {
		t.Fatalf("got %s", resp.Provider)
	}
}

// TestGeminiOnlyEveryRouteResolves proves every task route in
// config/config.example.yaml (research, script, metadata, classify,
// translate_cleanup, plus the embed route) resolves to a working provider
// when GEMINI_API_KEY is the only key set and no Ollama is running — CONTEXT
// D25 / M2-124 ("run fully on one Gemini key, no Ollama"). This mirrors the
// example config's routes exactly (kept in sync manually; a drift here would
// mean the example config no longer matches what's tested).
func TestGeminiOnlyEveryRouteResolves(t *testing.T) {
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "gemini-3.8-flash",
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": "ok"}},
			},
		})
	}))
	defer chatSrv.Close()

	embedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "gemini-embedding-001",
			"data":  []map[string]any{{"index": 0, "embedding": []float64{0.1, 0.2}}},
		})
	}))
	defer embedSrv.Close()

	// Only GEMINI_API_KEY is set; every other provider's key stays empty so
	// it is skipped (D24), and ollama points at an address nothing listens
	// on so it is skipped too (connection refused -> ErrUnavailable).
	for _, k := range []string{"GROQ_API_KEY", "CEREBRAS_API_KEY", "OPENROUTER_API_KEY"} {
		t.Setenv(k, "")
	}
	t.Setenv("GEMINI_API_KEY", "test-key")

	cfg := config.LLMConfig{
		Providers: map[string]config.ProviderConfig{
			"ollama":     {Kind: "ollama", BaseURL: "http://127.0.0.1:1", Model: "TBD-8b-instruct-q4", EmbedModel: "nomic-embed-text"},
			"groq":       {Kind: "openai_compat", BaseURL: "http://127.0.0.1:1", KeyEnv: "GROQ_API_KEY", Model: "TBD"},
			"cerebras":   {Kind: "openai_compat", BaseURL: "http://127.0.0.1:1", KeyEnv: "CEREBRAS_API_KEY", Model: "TBD"},
			"openrouter": {Kind: "openai_compat", BaseURL: "http://127.0.0.1:1", KeyEnv: "OPENROUTER_API_KEY", Model: "TBD:free"},
			"gemini":     {Kind: "openai_compat", BaseURL: chatSrv.URL, KeyEnv: "GEMINI_API_KEY", Model: "gemini-3.8-flash", EmbedModel: "gemini-embedding-001"},
		},
		// Same shape as config/config.example.yaml's routes (blog/builder
		// excluded: claude_cli is content-task-forbidden by design, not
		// something a Gemini-only setup needs to resolve).
		Routes: map[string][]string{
			"research":          {"groq", "cerebras", "openrouter", "gemini", "ollama"},
			"script":            {"groq", "cerebras", "openrouter", "gemini", "ollama"},
			"metadata":          {"ollama", "groq", "gemini"},
			"classify":          {"ollama", "groq", "gemini"},
			"translate_cleanup": {"ollama", "gemini"},
		},
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	for _, task := range []Task{TaskResearch, TaskScript, TaskMetadata, TaskClassify, TaskTranslateCleanup} {
		t.Run(string(task), func(t *testing.T) {
			resp, err := r.Complete(context.Background(), task, Request{
				Messages: []Message{{Role: "user", Content: "x"}},
			})
			if err != nil {
				t.Fatalf("task %s: %v", task, err)
			}
			if resp.Provider != "gemini" {
				t.Fatalf("task %s: provider=%s, want gemini", task, resp.Provider)
			}
		})
	}

	// embed is its own route/method, tested against the embeddings endpoint.
	t.Run("embed", func(t *testing.T) {
		embedCfg := cfg
		embedCfg.Providers = map[string]config.ProviderConfig{
			"ollama": cfg.Providers["ollama"],
			"gemini": {Kind: "openai_compat", BaseURL: embedSrv.URL, KeyEnv: "GEMINI_API_KEY", Model: "gemini-3.8-flash", EmbedModel: "gemini-embedding-001"},
		}
		embedCfg.Routes = map[string][]string{"embed": {"ollama", "gemini"}}
		er, err := New(embedCfg)
		if err != nil {
			t.Fatal(err)
		}
		out, err := er.Embed(context.Background(), EmbedRequest{Texts: []string{"x"}})
		if err != nil {
			t.Fatalf("embed: %v", err)
		}
		if out.Provider != "gemini" || out.Model != "gemini-embedding-001" {
			t.Fatalf("embed: %+v", out)
		}
	})
}
