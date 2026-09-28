// Package config loads config.yaml and resolves its relative paths.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	DataDir      string         `yaml:"data_dir"`
	PollInterval string         `yaml:"poll_interval"`
	Telegram     TelegramConfig `yaml:"telegram"`
	Builder      BuilderConfig  `yaml:"builder"`

	Poll time.Duration `yaml:"-"`
}

type TelegramConfig struct {
	BotTokenEnv string `yaml:"bot_token_env"`
	ChatIDEnv   string `yaml:"chat_id_env"`
}

type BuilderConfig struct {
	Enabled        bool         `yaml:"enabled"`
	ClaudeBin      string       `yaml:"claude_bin"`
	Model          string       `yaml:"model"`
	Effort         string       `yaml:"effort"`
	MaxFixAttempts int          `yaml:"max_fix_attempts"`
	RunTimeout     string       `yaml:"run_timeout"`
	LimitPause     string       `yaml:"limit_pause"`
	WorktreesDir   string       `yaml:"worktrees_dir"`
	Repos          []RepoConfig `yaml:"repos"`

	RunTimeoutD time.Duration `yaml:"-"`
	LimitPauseD time.Duration `yaml:"-"`
}

type RepoConfig struct {
	Name       string `yaml:"name"`
	Path       string `yaml:"path"`        // git repository root
	Subdir     string `yaml:"subdir"`      // project folder inside the repo, "" for the root
	BaseBranch string `yaml:"base_branch"` // branch new work starts from
	TicketsDir string `yaml:"tickets_dir"` // relative to Subdir
	Push       bool   `yaml:"push"`        // push the branch and open a PR when done

	Setup       []string `yaml:"setup"`        // run once in a fresh worktree, e.g. "npm ci"
	Checks      []string `yaml:"checks"`       // must all pass before a ticket goes to review
	AllowedBash []string `yaml:"allowed_bash"` // extra Bash(...) patterns Claude may run
}

// Load reads the YAML file at path. Relative paths inside it resolve against
// the file's own directory so the daemon behaves the same from any cwd.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := &Config{}
	if err := yaml.Unmarshal(raw, c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	if err := c.resolve(base); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

func (c *Config) resolve(base string) error {
	abs := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(base, p)
	}
	dur := func(s, def string) (time.Duration, error) {
		if s == "" {
			s = def
		}
		return time.ParseDuration(s)
	}

	var err error
	c.DataDir = abs(orDefault(c.DataDir, "data"))
	if c.Poll, err = dur(c.PollInterval, "5m"); err != nil {
		return fmt.Errorf("poll_interval: %w", err)
	}
	c.Telegram.BotTokenEnv = orDefault(c.Telegram.BotTokenEnv, "TELEGRAM_BOT_TOKEN")
	c.Telegram.ChatIDEnv = orDefault(c.Telegram.ChatIDEnv, "TELEGRAM_CHAT_ID")

	b := &c.Builder
	b.Model = orDefault(b.Model, "claude-opus-5-5")
	if b.MaxFixAttempts <= 0 {
		b.MaxFixAttempts = 3
	}
	if b.RunTimeoutD, err = dur(b.RunTimeout, "60m"); err != nil {
		return fmt.Errorf("builder.run_timeout: %w", err)
	}
	if b.LimitPauseD, err = dur(b.LimitPause, "30m"); err != nil {
		return fmt.Errorf("builder.limit_pause: %w", err)
	}
	b.WorktreesDir = abs(orDefault(b.WorktreesDir, filepath.Join(c.DataDir, "worktrees")))

	seen := map[string]bool{}
	for i := range b.Repos {
		r := &b.Repos[i]
		if r.Name == "" || r.Path == "" {
			return fmt.Errorf("builder.repos[%d]: name and path are required", i)
		}
		if seen[r.Name] {
			return fmt.Errorf("builder.repos: duplicate name %q", r.Name)
		}
		seen[r.Name] = true
		r.Path = abs(r.Path)
		r.BaseBranch = orDefault(r.BaseBranch, "main")
		r.TicketsDir = orDefault(r.TicketsDir, "tickets")
	}
	return nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
