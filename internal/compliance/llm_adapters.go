package compliance

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"mayank2/internal/llm"
)

// RouterEmbedder embeds via the LLM router's Ollama provider.
type RouterEmbedder struct {
	Router *llm.Router
}

func (e RouterEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if e.Router == nil {
		return nil, fmt.Errorf("compliance: nil Router for embeddings")
	}
	resp, err := e.Router.Embed(ctx, llm.EmbedRequest{Texts: texts})
	if err != nil {
		return nil, err
	}
	return resp.Vectors, nil
}

// Completer is the subset of llm.Router used by LLMClassifier.
type Completer interface {
	Complete(ctx context.Context, task llm.Task, req llm.Request) (llm.Response, error)
}

// LLMClassifier runs TaskClassify and parses {"pass":bool,"detail":string}.
type LLMClassifier struct {
	Completer Completer
}

func (c LLMClassifier) Classify(ctx context.Context, system, user string) (bool, string, error) {
	if c.Completer == nil {
		return false, "", fmt.Errorf("compliance: nil Completer")
	}
	schema := json.RawMessage(`{"type":"object","required":["pass"],"properties":{"pass":{"type":"boolean"},"detail":{"type":"string"}}}`)
	resp, err := c.Completer.Complete(ctx, llm.TaskClassify, llm.Request{
		System:     system,
		Messages:   []llm.Message{{Role: "user", Content: user}},
		MaxTokens:  256,
		JSONSchema: schema,
	})
	if err != nil {
		return false, "", err
	}
	text := strings.TrimSpace(resp.Text)
	if strings.HasPrefix(text, "```") {
		text = stripFence(text)
	}
	var out struct {
		Pass   bool   `json:"pass"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return false, "", fmt.Errorf("compliance: classify json: %w", err)
	}
	return out.Pass, out.Detail, nil
}

func stripFence(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
