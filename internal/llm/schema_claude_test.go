package llm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"mayank2/internal/config"
)

func TestValidateJSONSchemaOK(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["facts"],
		"properties":{
			"facts":{"type":"array","items":{"type":"string"}}
		}
	}`)
	if err := ValidateJSONSchema(schema, `{"facts":["a"]}`); err != nil {
		t.Fatal(err)
	}
}

func TestValidateJSONSchemaFence(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","required":["x"],"properties":{"x":{"type":"number"}}}`)
	if err := ValidateJSONSchema(schema, "```json\n{\"x\":1}\n```"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateJSONSchemaMissing(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","required":["x"]}`)
	err := ValidateJSONSchema(schema, `{}`)
	if err == nil || !strings.Contains(err.Error(), "missing required") {
		t.Fatalf("err=%v", err)
	}
}

func TestClaudeCLIFixedArgsAndStdin(t *testing.T) {
	cfg := config.LLMConfig{
		Providers: map[string]config.ProviderConfig{
			"claude": {Kind: "claude_cli", Bin: "claude"},
		},
		Routes: map[string][]string{"blog": {"claude"}},
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cp := r.providers["claude"].(*claudeCLIProvider)
	cp.lookPath = func(string) (string, error) { return "claude", nil }

	var gotArgs []string
	var gotStdin string
	cp.run = func(ctx context.Context, bin string, args []string, stdin string) ([]byte, []byte, error) {
		gotArgs = append([]string(nil), args...)
		gotStdin = stdin
		return []byte(`{"result":"blog draft","model":"claude-sonnet-5"}`), nil, nil
	}

	resp, err := r.Complete(context.Background(), TaskBlog, Request{
		System:   "You are Mayank.",
		Messages: []Message{{Role: "user", Content: "Write a post"}},
		Model:    "claude-sonnet-5",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "blog draft" || resp.Provider != "claude" || resp.Model != "claude-sonnet-5" {
		t.Fatalf("%+v", resp)
	}
	wantArgs := []string{"-p", "--output-format", "json", "--model", "claude-sonnet-5"}
	if strings.Join(gotArgs, " ") != strings.Join(wantArgs, " ") {
		t.Fatalf("args=%v", gotArgs)
	}
	if !strings.Contains(gotStdin, "You are Mayank.") || !strings.Contains(gotStdin, "Write a post") {
		t.Fatalf("stdin=%q", gotStdin)
	}
}

func TestClaudeCLIRateLimitMarksUnavailable(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q := NewQuotaTracker()
	q.SetClock(func() time.Time { return now })

	cfg := config.LLMConfig{
		Providers: map[string]config.ProviderConfig{
			"claude": {Kind: "claude_cli", Bin: "claude"},
		},
		Routes: map[string][]string{"builder": {"claude"}},
	}
	r, err := New(cfg, WithQuotas(q), WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	cp := r.providers["claude"].(*claudeCLIProvider)
	cp.lookPath = func(string) (string, error) { return "claude", nil }
	cp.run = func(ctx context.Context, bin string, args []string, stdin string) ([]byte, []byte, error) {
		return nil, []byte("usage limit reached"), context.DeadlineExceeded
	}

	_, err = r.Complete(context.Background(), TaskBuilder, Request{
		Messages: []Message{{Role: "user", Content: "x"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if q.Available("claude") {
		t.Fatal("claude should be marked unavailable after timeout/limit")
	}
}
