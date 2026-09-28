package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"mayank2/internal/config"
)

type claudeCLIProvider struct {
	name   string
	bin    string
	quotas *QuotaTracker
	now    func() time.Time
	// lookPath is replaceable in tests.
	lookPath func(file string) (string, error)
	// run is replaceable in tests: (ctx, bin, args, stdin) -> stdout, stderr, err
	run func(ctx context.Context, bin string, args []string, stdin string) ([]byte, []byte, error)
}

func newClaudeCLI(name string, pc config.ProviderConfig, quotas *QuotaTracker, now func() time.Time) *claudeCLIProvider {
	p := &claudeCLIProvider{
		name:     name,
		bin:      pc.Bin,
		quotas:   quotas,
		now:      now,
		lookPath: exec.LookPath,
	}
	p.run = p.defaultRun
	return p
}

func (p *claudeCLIProvider) Name() string { return p.name }
func (p *claudeCLIProvider) Kind() string { return "claude_cli" }

func (p *claudeCLIProvider) Available(ctx context.Context) bool {
	_ = ctx
	if !p.quotas.Available(p.name) {
		return false
	}
	_, err := p.lookPath(p.bin)
	return err == nil
}

func (p *claudeCLIProvider) Complete(ctx context.Context, req Request) (Response, error) {
	prompt := buildClaudePrompt(req)
	// Fixed argument list — never interpolate model output into a shell string.
	args := []string{"-p", "--output-format", "json"}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}

	stdout, stderr, err := p.run(ctx, p.bin, args, prompt)
	if err != nil {
		reason := "timeout"
		if ctx.Err() == nil && !isTimeout(err) {
			// Non-timeout failures: treat rate-limit-ish stderr as 429.
			low := strings.ToLower(string(stderr) + err.Error())
			if strings.Contains(low, "rate") || strings.Contains(low, "429") || strings.Contains(low, "usage limit") {
				reason = "429"
			} else {
				return Response{}, fmt.Errorf("claude_cli %s: %w (%s)", p.name, err, truncate(string(stderr), 200))
			}
		}
		return Response{}, &ErrUnavailable{
			Provider: p.name,
			Reason:   reason,
			Err:      err,
			Until:    p.now().Add(defaultBackoff(reason)),
		}
	}

	text, model := parseClaudeOutput(stdout, req.Model)
	return Response{
		Text:     text,
		Provider: p.name,
		Model:    model,
	}, nil
}

func (p *claudeCLIProvider) defaultRun(ctx context.Context, bin string, args []string, stdin string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func buildClaudePrompt(req Request) string {
	var b strings.Builder
	if req.System != "" {
		b.WriteString(req.System)
		b.WriteString("\n\n")
	}
	for _, m := range req.Messages {
		b.WriteString(strings.ToUpper(m.Role))
		b.WriteString(": ")
		b.WriteString(m.Content)
		b.WriteString("\n\n")
	}
	if len(req.JSONSchema) > 0 {
		b.WriteString("Respond with JSON only matching this schema:\n")
		b.Write(req.JSONSchema)
		b.WriteString("\n")
	}
	return b.String()
}

func parseClaudeOutput(stdout []byte, fallbackModel string) (text, model string) {
	model = fallbackModel
	var obj map[string]any
	if err := json.Unmarshal(stdout, &obj); err != nil {
		return strings.TrimSpace(string(stdout)), model
	}
	if m, ok := obj["model"].(string); ok && m != "" {
		model = m
	}
	// Common Claude Code JSON shapes: result / content / text
	for _, key := range []string{"result", "content", "text"} {
		if v, ok := obj[key].(string); ok && v != "" {
			return v, model
		}
	}
	// Pretty-print whole object if no known field.
	return strings.TrimSpace(string(stdout)), model
}

func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	if err == context.DeadlineExceeded || err == context.Canceled {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "signal: killed") ||
		strings.Contains(strings.ToLower(err.Error()), "context deadline")
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
