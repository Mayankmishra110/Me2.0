package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"mayank2/internal/config"
)

type ollamaProvider struct {
	name       string
	baseURL    string
	model      string
	embedModel string
	client     *http.Client
	quotas     *QuotaTracker
	now        func() time.Time
}

func newOllama(name string, pc config.ProviderConfig, client *http.Client, quotas *QuotaTracker, now func() time.Time) *ollamaProvider {
	return &ollamaProvider{
		name:       name,
		baseURL:    strings.TrimRight(pc.BaseURL, "/"),
		model:      pc.Model,
		embedModel: pc.EmbedModel,
		client:     client,
		quotas:     quotas,
		now:        now,
	}
}

func (p *ollamaProvider) Name() string { return p.name }
func (p *ollamaProvider) Kind() string { return "ollama" }

func (p *ollamaProvider) Available(ctx context.Context) bool {
	_ = ctx
	return p.quotas.Available(p.name)
}

func (p *ollamaProvider) Complete(ctx context.Context, req Request) (Response, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}
	msgs := make([]map[string]string, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, map[string]string{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, map[string]string{"role": m.Role, "content": m.Content})
	}
	body := map[string]any{
		"model":    model,
		"messages": msgs,
		"stream":   false,
	}
	if req.MaxTokens > 0 {
		body["options"] = map[string]any{"num_predict": req.MaxTokens}
	}
	if len(req.JSONSchema) > 0 {
		body["format"] = "json"
	}

	raw, status, err := p.post(ctx, "/api/chat", body)
	if err != nil {
		return Response{}, err
	}
	if err := classifyHTTP(p.name, status, raw, p.now); err != nil {
		return Response{}, err
	}

	var out struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Response{}, fmt.Errorf("ollama: decode response: %w", err)
	}
	return Response{
		Text:     out.Message.Content,
		Provider: p.name,
		Model:    firstNonEmpty(out.Model, model),
	}, nil
}

func (p *ollamaProvider) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	model := req.Model
	if model == "" {
		model = p.embedModel
	}
	if model == "" {
		return EmbedResponse{}, fmt.Errorf("ollama: embed_model not configured")
	}
	vectors := make([][]float64, 0, len(req.Texts))
	for _, text := range req.Texts {
		body := map[string]any{
			"model":  model,
			"prompt": text,
		}
		raw, status, err := p.post(ctx, "/api/embeddings", body)
		if err != nil {
			return EmbedResponse{}, err
		}
		if err := classifyHTTP(p.name, status, raw, p.now); err != nil {
			return EmbedResponse{}, err
		}
		var out struct {
			Embedding []float64 `json:"embedding"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return EmbedResponse{}, fmt.Errorf("ollama: decode embeddings: %w", err)
		}
		vectors = append(vectors, out.Embedding)
	}
	return EmbedResponse{Vectors: vectors, Provider: p.name, Model: model}, nil
}

func (p *ollamaProvider) post(ctx context.Context, path string, body any) ([]byte, int, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, 0, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, &ErrUnavailable{Provider: p.name, Reason: "timeout", Err: ctx.Err(), Until: p.now().Add(defaultBackoff("timeout"))}
		}
		return nil, 0, &ErrUnavailable{Provider: p.name, Reason: "timeout", Err: err, Until: p.now().Add(defaultBackoff("timeout"))}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return raw, resp.StatusCode, nil
}
