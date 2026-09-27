package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mayank2/internal/config"
	"mayank2/internal/llm"
)

// defaultAskTask is used when `llm ask` is called without --task.
const defaultAskTask = "script"

func cmdLLM(ctx context.Context, args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, usage)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	switch args[0] {
	case "ask":
		return cmdLLMAsk(ctx, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "llm: unknown subcommand %q (want: ask)\n\n%s", args[0], usage)
		return 2
	}
}

// cmdLLMAsk implements `mayank2 llm ask [--task script] "<prompt>"` (M2-114 AC3):
// it goes through internal/llm.Router.Complete and prints provider, model, answer.
func cmdLLMAsk(ctx context.Context, args []string) int {
	configPath, envPath, _ := defaultPaths()
	task := defaultAskTask
	var rest []string

	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "-task" || a == "--task":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "llm ask: %s needs a value\n", a)
				return 2
			}
			task = args[i+1]
			i += 2
		case strings.HasPrefix(a, "-task="):
			task = strings.TrimPrefix(a, "-task=")
			i++
		case strings.HasPrefix(a, "--task="):
			task = strings.TrimPrefix(a, "--task=")
			i++
		case a == "-config" || a == "--config":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "llm ask: %s needs a value\n", a)
				return 2
			}
			configPath = args[i+1]
			i += 2
		case strings.HasPrefix(a, "-config="):
			configPath = strings.TrimPrefix(a, "-config=")
			i++
		case a == "-env" || a == "--env":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "llm ask: %s needs a value\n", a)
				return 2
			}
			envPath = args[i+1]
			i += 2
		case strings.HasPrefix(a, "-env="):
			envPath = strings.TrimPrefix(a, "-env=")
			i++
		case a == "-h" || a == "--help":
			fmt.Fprint(os.Stderr, usage)
			return 0
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "llm ask: unknown flag %q\n", a)
			return 2
		default:
			rest = append(rest, a)
			i++
		}
	}
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "llm ask: a prompt is required, e.g. mayank2 llm ask \"hello\"")
		return 2
	}
	prompt := strings.Join(rest, " ")

	if err := config.LoadEnvFile(envPath); err != nil {
		fmt.Fprintf(os.Stderr, "llm ask: load env: %v\n", err)
		return 1
	}
	if !fileExists(configPath) {
		example := filepath.Join(filepath.Dir(configPath), "config.example.yaml")
		if fileExists(example) {
			configPath = example
		}
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "llm ask: load config: %v\n", err)
		return 1
	}

	resp, err := askOnce(ctx, cfg, task, prompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "llm ask: %v\n", err)
		return 1
	}
	fmt.Printf("provider: %s\nmodel: %s\n\n%s\n", resp.Provider, resp.Model, resp.Text)
	return 0
}

// askOnce builds a router from cfg and runs one completion for task/prompt.
// Factored out of cmdLLMAsk so tests can inject a config.Config pointing at an
// httptest server instead of touching the network or the filesystem.
func askOnce(ctx context.Context, cfg *config.Config, task, prompt string) (llm.Response, error) {
	router, err := llm.New(cfg.LLM)
	if err != nil {
		return llm.Response{}, fmt.Errorf("build router: %w", err)
	}
	return router.Complete(ctx, llm.Task(task), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: prompt}},
	})
}
