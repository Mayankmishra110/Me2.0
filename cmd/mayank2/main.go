// Command mayank2 is the Mayank 2.0 daemon CLI.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const usage = `mayank2 — Mayank 2.0 daemon

Usage:
  mayank2 <command> [flags]

Commands:
  run       Start the daemon (not implemented yet)
  status    Show daemon / queue status (not implemented yet)
  doctor    Check tools, secrets presence, disk, RAM, channels
  migrate   Apply database migrations (idempotent)
  llm ask   Run one prompt through the model router (internal/llm)
  set-pin   Set the Telegram / dashboard PIN (not implemented yet)
  auth      OAuth sign-in for a platform account (not implemented yet)

Global flags (doctor / migrate / llm ask):
  -config path   config YAML (default: config/config.yaml)
  -env path      .env file (default: .env)
  -env-example   .env.example for key-name checks (default: .env.example; doctor only)
  -db path       sqlite file for migrate (default: <data_dir>/mayank2.db)

llm ask flags:
  -task name     route from config llm.routes (default: script)

Example:
  mayank2 llm ask --task script "Write a 3-beat hook about compound interest"
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(os.Stderr, usage)
		if len(args) == 0 {
			return 2
		}
		return 0
	}

	cmd := args[0]
	rest := args[1:]
	ctx := context.Background()

	switch cmd {
	case "doctor":
		return cmdDoctor(ctx, rest)
	case "migrate":
		return cmdMigrate(ctx, rest)
	case "llm":
		return cmdLLM(ctx, rest)
	case "run", "status", "set-pin", "auth":
		return cmdStub(cmd, rest)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}
}

func cmdStub(cmd string, args []string) int {
	_ = args
	fmt.Printf("mayank2 %s: not implemented yet (later ticket)\n", cmd)
	return 0
}

// defaultPaths resolves config/.env relative to cwd, falling back to the
// directory that contains go.mod when cwd is elsewhere (e.g. tests).
func defaultPaths() (configPath, envPath, envExample string) {
	configPath = "config/config.yaml"
	envPath = ".env"
	envExample = ".env.example"
	if fileExists(configPath) {
		return configPath, envPath, envExample
	}
	if root, err := findGoModRoot("."); err == nil {
		return filepath.Join(root, "config", "config.yaml"),
			filepath.Join(root, ".env"),
			filepath.Join(root, ".env.example")
	}
	return configPath, envPath, envExample
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func findGoModRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found from %s", start)
		}
		dir = parent
	}
}

func parseDoctorFlags(args []string) (configPath, envPath, envExample string, rest []string, err error) {
	configPath, envPath, envExample = defaultPaths()
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "-config" || a == "--config":
			if i+1 >= len(args) {
				return "", "", "", nil, fmt.Errorf("%s needs a value", a)
			}
			configPath = args[i+1]
			i += 2
		case strings.HasPrefix(a, "-config="):
			configPath = strings.TrimPrefix(a, "-config=")
			i++
		case a == "-env" || a == "--env":
			if i+1 >= len(args) {
				return "", "", "", nil, fmt.Errorf("%s needs a value", a)
			}
			envPath = args[i+1]
			i += 2
		case strings.HasPrefix(a, "-env="):
			envPath = strings.TrimPrefix(a, "-env=")
			i++
		case a == "-env-example" || a == "--env-example":
			if i+1 >= len(args) {
				return "", "", "", nil, fmt.Errorf("%s needs a value", a)
			}
			envExample = args[i+1]
			i += 2
		case strings.HasPrefix(a, "-env-example="):
			envExample = strings.TrimPrefix(a, "-env-example=")
			i++
		case a == "-h" || a == "--help":
			return "", "", "", nil, errHelp
		default:
			rest = append(rest, a)
			i++
		}
	}
	return configPath, envPath, envExample, rest, nil
}

var errHelp = fmt.Errorf("help")
