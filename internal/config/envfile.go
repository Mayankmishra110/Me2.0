package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// LoadEnvFile sets KEY=VALUE pairs from a .env file. Variables already set in
// the environment win. A missing file is not an error.
func LoadEnvFile(path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open env file %s: %w", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("%s:%d: expected KEY=VALUE", path, lineNo)
		}
		key = strings.TrimSpace(strings.TrimPrefix(key, "export "))
		if key == "" {
			return fmt.Errorf("%s:%d: empty key", path, lineNo)
		}
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		if _, set := os.LookupEnv(key); !set {
			if err := os.Setenv(key, val); err != nil {
				return fmt.Errorf("setenv %s: %w", key, err)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read env file %s: %w", path, err)
	}
	return nil
}

// RequiredEnvKeys returns the key names listed in an .env.example-style file.
// Values are ignored; this is for doctor presence checks only.
func RequiredEnvKeys(examplePath string) ([]string, error) {
	f, err := os.Open(examplePath)
	if err != nil {
		return nil, fmt.Errorf("open env example %s: %w", examplePath, err)
	}
	defer f.Close()

	var keys []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, _, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.TrimPrefix(key, "export "))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		keys = append(keys, key)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read env example %s: %w", examplePath, err)
	}
	return keys, nil
}

// EnvKeyPresent reports whether key is set in the environment (even to empty).
// Never returns or logs the value.
func EnvKeyPresent(key string) bool {
	_, ok := os.LookupEnv(key)
	return ok
}
