package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"mayank2/internal/config"
)

type openaiCompatProvider struct {
	name    string
	baseURL string
	keyEnv  string
	model   string
	client  *http.Client
	quotas  *QuotaTracker
	now     func() time.Time
}

func newOpenAICompat(name string, pc config.ProviderConfig, client *http.Client, quotas *QuotaTracker, now func() time.Time) *openaiCompatProvider {
	return &openaiCompatProvider{
		name:    name,
		baseURL: strings.TrimRight(pc.BaseURL, "/"),
		keyEnv:  pc.KeyEnv,
		model:   pc.Model,
		client:  client,
		quotas:  quotas,
		now:     now,
	}
}

func (p *openaiCompatProvider) Name() string { return p.name }
func (p *openaiCompatProvider) Kind() string { return "openai_compat" }

func (p *openaiCompatProvider) Available(ctx context.Context) bool {
	_ = ctx
	if !p.quotas.Available(p.name) {
		return false
	}
	// Missing key → skip without marking quota (config/env not ready).
	return strings.TrimSpace(os.Getenv(p.keyEnv)) != ""
}

func (p *openaiCompatProvider) Complete(ctx context.Context, req Request) (Response, error) {
	key := strings.TrimSpace(os.Getenv(p.keyEnv))
	if key == "" {
		return Response{}, fmt.Errorf("openai_compat %s: env %s is empty", p.name, p.keyEnv)
	}
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
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if len(req.JSONSchema) > 0 {
		body["response_format"] = map[string]any{"type": "json_object"}
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return Response{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+key)
	// Never log Authorization or the key.

	resp, err := p.client.Do(httpReq)
	if err != nil {
		reason := "timeout"
		until := p.now().Add(defaultBackoff(reason))
		return Response{}, &ErrUnavailable{Provider: p.name, Reason: reason, Err: err, Until: until}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return Response{}, err
	}
	if err := classifyHTTP(p.name, resp.StatusCode, raw, p.now); err != nil {
		if u, ok := err.(*ErrUnavailable); ok {
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if secs, e := strconv.Atoi(ra); e == nil && secs > 0 {
					u.Until = p.now().Add(time.Duration(secs) * time.Second)
				}
			}
		}
		return Response{}, err
	}

	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Response{}, fmt.Errorf("openai_compat %s: decode: %w", p.name, err)
	}
	if len(out.Choices) == 0 {
		return Response{}, fmt.Errorf("openai_compat %s: empty choices", p.name)
	}
	return Response{
		Text:     out.Choices[0].Message.Content,
		Provider: p.name,
		Model:    firstNonEmpty(out.Model, model),
	}, nil
}
