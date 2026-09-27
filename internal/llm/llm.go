// Package llm is the model router (ARCHITECTURE §6, CONTEXT D13).
// Content tasks use free/local providers only; Claude is reserved for blog/builder.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"mayank2/internal/config"
)

// Task selects a route from config.llm.routes.
type Task string

const (
	TaskResearch         Task = "research"
	TaskScript           Task = "script"
	TaskMetadata         Task = "metadata"
	TaskClassify         Task = "classify"
	TaskTranslateCleanup Task = "translate_cleanup"
	TaskBlog             Task = "blog"
	TaskBuilder          Task = "builder"
)

// Message is one chat turn.
type Message struct {
	Role    string `json:"role"` // system | user | assistant
	Content string `json:"content"`
}

// Request is a completion request.
type Request struct {
	System     string
	Messages   []Message
	MaxTokens  int
	JSONSchema json.RawMessage // optional; when set, response text must be valid JSON matching the schema
	Model      string          // optional override of the provider's configured model
}

// Response is a successful completion. Provider and Model are recorded on jobs
// for the dashboard.
type Response struct {
	Text       string
	Provider   string
	Model      string
	LowQuality bool // true when the chain fell through to local Ollama for research/script
}

// EmbedRequest embeds one or more texts (Ollama nomic-embed-text).
type EmbedRequest struct {
	Texts []string
	Model string // optional; defaults to provider embed_model
}

// EmbedResponse holds embedding vectors aligned with EmbedRequest.Texts.
type EmbedResponse struct {
	Vectors  [][]float64
	Provider string
	Model    string
}

// Provider is one backend (ARCHITECTURE §6).
type Provider interface {
	Name() string
	Kind() string
	Complete(ctx context.Context, req Request) (Response, error)
	Available(ctx context.Context) bool
}

// ErrNoProvider means every route entry was skipped or failed.
var ErrNoProvider = errors.New("llm: no available provider")

// ErrUnavailable is returned by a provider for 429 / 5xx / timeout so the
// router can mark it down and fall through.
type ErrUnavailable struct {
	Provider string
	Reason   string
	Until    time.Time
	Err      error
}

func (e *ErrUnavailable) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("llm provider %s unavailable (%s): %v", e.Provider, e.Reason, e.Err)
	}
	return fmt.Sprintf("llm provider %s unavailable (%s)", e.Provider, e.Reason)
}

func (e *ErrUnavailable) Unwrap() error { return e.Err }

// Router picks the first available provider for a task.
type Router struct {
	providers map[string]Provider
	routes    map[string][]string
	quotas    *QuotaTracker
	log       *slog.Logger
	now       func() time.Time
	client    *http.Client // shared by HTTP providers; tests inject via option
}

// Option configures a Router.
type Option func(*Router)

// WithLogger sets the logger (never logs tokens or full prompts).
func WithLogger(log *slog.Logger) Option {
	return func(r *Router) { r.log = log }
}

// WithHTTPClient overrides the HTTP client used by ollama / openai_compat.
func WithHTTPClient(c *http.Client) Option {
	return func(r *Router) { r.client = c }
}

// WithClock overrides the time source (tests).
func WithClock(now func() time.Time) Option {
	return func(r *Router) { r.now = now }
}

// WithQuotas injects a shared quota tracker (tests).
func WithQuotas(q *QuotaTracker) Option {
	return func(r *Router) { r.quotas = q }
}

// New builds a Router from config.LLM. Keys are read from the process env at
// call time (KeyEnv names only — values never logged).
func New(cfg config.LLMConfig, opts ...Option) (*Router, error) {
	r := &Router{
		providers: map[string]Provider{},
		routes:    map[string][]string{},
		quotas:    NewQuotaTracker(),
		log:       slog.Default(),
		now:       time.Now,
		client:    &http.Client{Timeout: 120 * time.Second},
	}
	for _, opt := range opts {
		opt(r)
	}
	if cfg.Routes != nil {
		for k, v := range cfg.Routes {
			cp := append([]string(nil), v...)
			r.routes[k] = cp
		}
	}
	for name, pc := range cfg.Providers {
		p, err := buildProvider(name, pc, r)
		if err != nil {
			return nil, err
		}
		r.providers[name] = p
	}
	return r, nil
}

func buildProvider(name string, pc config.ProviderConfig, r *Router) (Provider, error) {
	switch pc.Kind {
	case "ollama":
		return newOllama(name, pc, r.client, r.quotas, r.now), nil
	case "openai_compat":
		return newOpenAICompat(name, pc, r.client, r.quotas, r.now), nil
	case "claude_cli":
		return newClaudeCLI(name, pc, r.quotas, r.now), nil
	default:
		return nil, fmt.Errorf("llm: unknown provider kind %q for %s", pc.Kind, name)
	}
}

// Complete runs the route for task: first Available provider that succeeds.
func (r *Router) Complete(ctx context.Context, task Task, req Request) (Response, error) {
	chain, ok := r.routes[string(task)]
	if !ok || len(chain) == 0 {
		return Response{}, fmt.Errorf("%w: no route for task %q", ErrNoProvider, task)
	}

	var errs []error
	for i, name := range chain {
		p, ok := r.providers[name]
		if !ok {
			errs = append(errs, fmt.Errorf("unknown provider %q", name))
			continue
		}
		if isContentTask(task) && p.Kind() == "claude_cli" {
			r.log.Warn("llm: skipping claude_cli on content task", "task", task, "provider", name)
			errs = append(errs, fmt.Errorf("claude_cli forbidden for content task %q", task))
			continue
		}
		if !p.Available(ctx) {
			errs = append(errs, fmt.Errorf("%s: marked unavailable", name))
			continue
		}

		resp, err := r.completeWithSchema(ctx, p, req)
		if err != nil {
			var u *ErrUnavailable
			if errors.As(err, &u) {
				until := u.Until
				if until.IsZero() {
					until = r.now().Add(defaultBackoff(u.Reason))
				}
				r.quotas.MarkUnavailable(name, until, u.Reason)
				r.log.Info("llm: provider unavailable, falling through",
					"provider", name, "reason", u.Reason, "until", until.UTC().Format(time.RFC3339))
			} else {
				r.log.Info("llm: provider failed, falling through", "provider", name, "err", err.Error())
			}
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}

		resp.Provider = p.Name()
		if resp.Model == "" {
			resp.Model = req.Model
		}
		if p.Kind() == "ollama" && (task == TaskResearch || task == TaskScript) && i > 0 {
			resp.LowQuality = true
		}
		return resp, nil
	}

	return Response{}, fmt.Errorf("%w for task %q: %w", ErrNoProvider, task, errors.Join(errs...))
}

func (r *Router) completeWithSchema(ctx context.Context, p Provider, req Request) (Response, error) {
	resp, err := p.Complete(ctx, req)
	if err != nil {
		return Response{}, err
	}
	if len(req.JSONSchema) == 0 {
		return resp, nil
	}
	if err := ValidateJSONSchema(req.JSONSchema, resp.Text); err == nil {
		return resp, nil
	} else {
		// One repair retry.
		repair := req
		repair.Messages = append(append([]Message{}, req.Messages...), Message{
			Role:    "user",
			Content: "Your previous reply was not valid JSON for the required schema. Error: " + err.Error() + "\nReply with JSON only.",
		})
		resp2, err2 := p.Complete(ctx, repair)
		if err2 != nil {
			return Response{}, err2
		}
		if err3 := ValidateJSONSchema(req.JSONSchema, resp2.Text); err3 != nil {
			return Response{}, fmt.Errorf("llm: json schema validation failed after repair: %w", err3)
		}
		return resp2, nil
	}
}

// Embed runs embeddings via the ollama provider (nomic-embed-text by default).
func (r *Router) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	var ollama *ollamaProvider
	for _, p := range r.providers {
		if op, ok := p.(*ollamaProvider); ok {
			ollama = op
			break
		}
	}
	if ollama == nil {
		return EmbedResponse{}, fmt.Errorf("%w: no ollama provider configured for embeddings", ErrNoProvider)
	}
	if !ollama.Available(ctx) {
		return EmbedResponse{}, fmt.Errorf("%w: ollama unavailable", ErrNoProvider)
	}
	return ollama.Embed(ctx, req)
}

func isContentTask(t Task) bool {
	switch t {
	case TaskBlog, TaskBuilder:
		return false
	default:
		return true
	}
}

func defaultBackoff(reason string) time.Duration {
	switch strings.ToLower(reason) {
	case "429", "rate_limit":
		return time.Hour
	case "5xx", "server_error":
		return time.Minute
	case "timeout":
		return 30 * time.Second
	default:
		return 5 * time.Minute
	}
}
