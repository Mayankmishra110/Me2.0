package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"

	"mayank2/internal/config"
	"mayank2/internal/db"
)

const setPinUsage = `mayank2 set-pin — set the Telegram / dashboard resume PIN

Usage:
  mayank2 set-pin [flags]

The PIN is never accepted as a command-line argument (it would land in shell
history and the process list). On a real terminal it is read twice with echo
disabled and must match; when stdin is not a terminal (piped input, scripts,
tests) a single line is read from stdin instead.

Flags:
  -config path   config YAML (default: config/config.yaml)
  -env path      .env file (default: .env)
  -db path       sqlite file (default: <data_dir>/mayank2.db)
`

// settingPINHash mirrors internal/telegram's settings key for the bcrypt PIN
// hash and internal/httpapi's checkPIN lookup — all three must agree.
const settingPINHash = "pin_hash"

func cmdSetPin(ctx context.Context, args []string) int {
	configPath, envPath, dbPath, err := parseSetPinFlags(args)
	if err == errHelp {
		fmt.Fprint(os.Stderr, setPinUsage)
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "set-pin: %v\n", err)
		return 2
	}

	if err := config.LoadEnvFile(envPath); err != nil {
		fmt.Fprintf(os.Stderr, "set-pin: load env: %v\n", err)
		return 1
	}

	if dbPath == "" {
		cfgPath := configPath
		if !fileExists(cfgPath) {
			example := cfgPath + ".example"
			if fileExists(example) {
				cfgPath = example
			}
		}
		cfg, err := config.Load(cfgPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "set-pin: load config: %v\n", err)
			return 1
		}
		dbPath = db.DefaultPath(cfg.DataDir)
	}

	sqlDB, err := db.Open(ctx, dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "set-pin: open db: %v\n", err)
		return 1
	}
	defer sqlDB.Close()
	if _, err := db.Migrate(ctx, sqlDB); err != nil {
		fmt.Fprintf(os.Stderr, "set-pin: migrate: %v\n", err)
		return 1
	}

	pin, err := readPIN(os.Stdin, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "set-pin: %v\n", err)
		return 1
	}
	if strings.TrimSpace(pin) == "" {
		fmt.Fprintln(os.Stderr, "set-pin: PIN must not be empty")
		return 2
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(pin), bcrypt.DefaultCost)
	if err != nil {
		// Never include the PIN itself in the error.
		fmt.Fprintln(os.Stderr, "set-pin: hash pin failed")
		return 1
	}

	if _, err := sqlDB.ExecContext(ctx, `
INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		settingPINHash, string(hash),
	); err != nil {
		fmt.Fprintf(os.Stderr, "set-pin: store hash: %v\n", err)
		return 1
	}

	fmt.Println("set-pin: PIN updated (stored as bcrypt hash; not printed)")
	return 0
}

// readPIN reads a PIN from in. When in is a real terminal, it prompts twice
// with echo disabled (golang.org/x/term) and requires both entries to match.
// Otherwise (piped stdin, tests, CI) it reads a single line — set-pin stays
// scriptable without ever taking the PIN as a CLI argument. The raw PIN is
// never logged or echoed back.
func readPIN(in *os.File, out *os.File) (string, error) {
	fd := int(in.Fd())
	if term.IsTerminal(fd) {
		fmt.Fprint(out, "Enter new PIN: ")
		first, err := term.ReadPassword(fd)
		fmt.Fprintln(out)
		if err != nil {
			return "", fmt.Errorf("read pin: %w", err)
		}
		fmt.Fprint(out, "Confirm PIN: ")
		second, err := term.ReadPassword(fd)
		fmt.Fprintln(out)
		if err != nil {
			return "", fmt.Errorf("read pin confirmation: %w", err)
		}
		if string(first) != string(second) {
			return "", fmt.Errorf("PINs did not match")
		}
		return string(first), nil
	}

	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read pin: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func parseSetPinFlags(args []string) (configPath, envPath, dbPath string, err error) {
	configPath, envPath, _ = defaultPaths()
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "-config" || a == "--config":
			if i+1 >= len(args) {
				return "", "", "", fmt.Errorf("%s needs a value", a)
			}
			configPath = args[i+1]
			i += 2
		case strings.HasPrefix(a, "-config="):
			configPath = strings.TrimPrefix(a, "-config=")
			i++
		case a == "-env" || a == "--env":
			if i+1 >= len(args) {
				return "", "", "", fmt.Errorf("%s needs a value", a)
			}
			envPath = args[i+1]
			i += 2
		case strings.HasPrefix(a, "-env="):
			envPath = strings.TrimPrefix(a, "-env=")
			i++
		case a == "-db" || a == "--db":
			if i+1 >= len(args) {
				return "", "", "", fmt.Errorf("%s needs a value", a)
			}
			dbPath = args[i+1]
			i += 2
		case strings.HasPrefix(a, "-db="):
			dbPath = strings.TrimPrefix(a, "-db=")
			i++
		case a == "-h" || a == "--help":
			return "", "", "", errHelp
		default:
			return "", "", "", fmt.Errorf("set-pin takes no PIN argument (enter it at the prompt); unknown arg %q", a)
		}
	}
	return configPath, envPath, dbPath, nil
}
