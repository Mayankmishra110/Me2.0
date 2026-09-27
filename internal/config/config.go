// Package config loads config.yaml, channel YAML files, and .env.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the full daemon configuration (schema = config/config.example.yaml).
type Config struct {
	DataDir   string          `yaml:"data_dir"`
	Timezone  string          `yaml:"timezone"`
	Queue     QueueConfig     `yaml:"queue"`
	Telegram  TelegramConfig  `yaml:"telegram"`
	Dashboard DashboardConfig `yaml:"dashboard"`
	LLM       LLMConfig       `yaml:"llm"`
	Content   ContentConfig   `yaml:"content"`
	Builder   BuilderConfig   `yaml:"builder"`

	// Resolved / derived (not from YAML).
	ConfigDir  string         `yaml:"-"`
	Channels   []Channel      `yaml:"-"`
	Location   *time.Location `yaml:"-"`
	RunTimeout time.Duration  `yaml:"-"`
	LimitPause time.Duration  `yaml:"-"`
}

type QueueConfig struct {
	HeavyWorkers int    `yaml:"heavy_workers"`
	LightWorkers int    `yaml:"light_workers"`
	NetWorkers   int    `yaml:"net_workers"`
	HeavyWindow  string `yaml:"heavy_window"`
}

type TelegramConfig struct {
	DailySummaryAt string `yaml:"daily_summary_at"`
}

type DashboardConfig struct {
	Listen []string `yaml:"listen"`
}

type LLMConfig struct {
	Providers map[string]ProviderConfig `yaml:"providers"`
	Routes    map[string][]string       `yaml:"routes"`
}

type ProviderConfig struct {
	Kind       string `yaml:"kind"`
	BaseURL    string `yaml:"base_url"`
	KeyEnv     string `yaml:"key_env"`
	Model      string `yaml:"model"`
	EmbedModel string `yaml:"embed_model"`
	Bin        string `yaml:"bin"`
}

type ContentConfig struct {
	ChannelsDir   string `yaml:"channels_dir"`
	RampUp        bool   `yaml:"ramp_up"`
	RetentionDays int    `yaml:"retention_days"`
}

type BuilderConfig struct {
	Enabled          bool         `yaml:"enabled"`
	ImplementerModel string       `yaml:"implementer_model"`
	AuditorModel     string       `yaml:"auditor_model"`
	MaxFixAttempts   int          `yaml:"max_fix_attempts"`
	RunTimeout       string       `yaml:"run_timeout"`
	LimitPause       string       `yaml:"limit_pause"`
	Repos            []RepoConfig `yaml:"repos"`
}

type RepoConfig struct {
	Name        string   `yaml:"name"`
	Path        string   `yaml:"path"`
	Subdir      string   `yaml:"subdir"`
	BaseBranch  string   `yaml:"base_branch"`
	TicketsDir  string   `yaml:"tickets_dir"`
	Push        bool     `yaml:"push"`
	Setup       []string `yaml:"setup"`
	Checks      []string `yaml:"checks"`
	AllowedBash []string `yaml:"allowed_bash"`
}

var (
	hhmmRe        = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
	windowRe      = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d-([01]\d|2[0-3]):[0-5]\d$`)
	providerKinds = map[string]bool{
		"ollama":        true,
		"openai_compat": true,
		"claude_cli":    true,
	}
)

// Load reads config YAML at path, resolves relative paths against that file's
// directory, loads channel files, and validates. Call LoadEnvFile first if
// secrets should be available in the process environment.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	c := &Config{}
	if err := yaml.Unmarshal(raw, c); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("abs config path %s: %w", path, err)
	}
	c.ConfigDir = filepath.Dir(abs)
	if err := c.resolve(); err != nil {
		return nil, fmt.Errorf("%s: %w", abs, err)
	}
	if err := c.loadChannels(); err != nil {
		return nil, fmt.Errorf("%s: %w", abs, err)
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", abs, err)
	}
	return c, nil
}

func (c *Config) resolve() error {
	absPath := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Clean(filepath.Join(c.ConfigDir, p))
	}

	c.DataDir = absPath(orDefault(c.DataDir, "../data"))
	c.Content.ChannelsDir = absPath(orDefault(c.Content.ChannelsDir, "channels"))

	var err error
	if c.Timezone == "" {
		c.Timezone = "UTC"
	}
	if c.Location, err = time.LoadLocation(c.Timezone); err != nil {
		return fmt.Errorf("timezone %q: %w", c.Timezone, err)
	}

	if c.RunTimeout, err = parseDuration(c.Builder.RunTimeout, "60m"); err != nil {
		return fmt.Errorf("builder.run_timeout: %w", err)
	}
	if c.LimitPause, err = parseDuration(c.Builder.LimitPause, "30m"); err != nil {
		return fmt.Errorf("builder.limit_pause: %w", err)
	}

	for i := range c.Builder.Repos {
		r := &c.Builder.Repos[i]
		r.Path = absPath(r.Path)
		r.BaseBranch = orDefault(r.BaseBranch, "main")
		r.TicketsDir = orDefault(r.TicketsDir, "tickets")
	}
	return nil
}

func (c *Config) validate() error {
	if c.DataDir == "" {
		return fmt.Errorf("data_dir is required")
	}
	if c.Queue.HeavyWorkers < 1 {
		return fmt.Errorf("queue.heavy_workers must be >= 1, got %d", c.Queue.HeavyWorkers)
	}
	if c.Queue.LightWorkers < 1 {
		return fmt.Errorf("queue.light_workers must be >= 1, got %d", c.Queue.LightWorkers)
	}
	if c.Queue.NetWorkers < 1 {
		return fmt.Errorf("queue.net_workers must be >= 1, got %d", c.Queue.NetWorkers)
	}
	if c.Queue.HeavyWindow == "" {
		return fmt.Errorf("queue.heavy_window is required")
	}
	if !windowRe.MatchString(c.Queue.HeavyWindow) {
		return fmt.Errorf("queue.heavy_window %q: want HH:MM-HH:MM", c.Queue.HeavyWindow)
	}
	if c.Telegram.DailySummaryAt == "" {
		return fmt.Errorf("telegram.daily_summary_at is required")
	}
	if !hhmmRe.MatchString(c.Telegram.DailySummaryAt) {
		return fmt.Errorf("telegram.daily_summary_at %q: want HH:MM", c.Telegram.DailySummaryAt)
	}
	if len(c.Dashboard.Listen) == 0 {
		return fmt.Errorf("dashboard.listen must list at least one address")
	}
	for i, addr := range c.Dashboard.Listen {
		if strings.TrimSpace(addr) == "" {
			return fmt.Errorf("dashboard.listen[%d] is empty", i)
		}
	}
	if c.Content.RetentionDays < 0 {
		return fmt.Errorf("content.retention_days must be >= 0, got %d", c.Content.RetentionDays)
	}
	if c.Builder.MaxFixAttempts < 0 {
		return fmt.Errorf("builder.max_fix_attempts must be >= 0, got %d", c.Builder.MaxFixAttempts)
	}
	if err := c.validateLLM(); err != nil {
		return err
	}
	seen := map[string]bool{}
	for i, ch := range c.Channels {
		if err := ValidateChannel(ch); err != nil {
			return fmt.Errorf("channels[%d] (%s): %w", i, ch.ID, err)
		}
		if seen[ch.ID] {
			return fmt.Errorf("channels: duplicate id %q", ch.ID)
		}
		seen[ch.ID] = true
	}
	seenRepo := map[string]bool{}
	for i, r := range c.Builder.Repos {
		if r.Name == "" || r.Path == "" {
			return fmt.Errorf("builder.repos[%d]: name and path are required", i)
		}
		if seenRepo[r.Name] {
			return fmt.Errorf("builder.repos: duplicate name %q", r.Name)
		}
		seenRepo[r.Name] = true
	}
	return nil
}

func (c *Config) validateLLM() error {
	if c.LLM.Providers == nil {
		c.LLM.Providers = map[string]ProviderConfig{}
	}
	if c.LLM.Routes == nil {
		c.LLM.Routes = map[string][]string{}
	}
	for name, p := range c.LLM.Providers {
		if !providerKinds[p.Kind] {
			return fmt.Errorf("llm.providers.%s.kind %q: want ollama|openai_compat|claude_cli", name, p.Kind)
		}
		switch p.Kind {
		case "ollama":
			if p.BaseURL == "" {
				return fmt.Errorf("llm.providers.%s: base_url is required for ollama", name)
			}
		case "openai_compat":
			if p.BaseURL == "" {
				return fmt.Errorf("llm.providers.%s: base_url is required for openai_compat", name)
			}
			if p.KeyEnv == "" {
				return fmt.Errorf("llm.providers.%s: key_env is required for openai_compat", name)
			}
		case "claude_cli":
			if p.Bin == "" {
				return fmt.Errorf("llm.providers.%s: bin is required for claude_cli", name)
			}
		}
	}
	for route, providers := range c.LLM.Routes {
		if len(providers) == 0 {
			return fmt.Errorf("llm.routes.%s: at least one provider required", route)
		}
		for _, name := range providers {
			if _, ok := c.LLM.Providers[name]; !ok {
				return fmt.Errorf("llm.routes.%s: unknown provider %q", route, name)
			}
		}
	}
	return nil
}

// OllamaModels returns the instruct and embed model names configured for the
// ollama provider, if present.
func (c *Config) OllamaModels() (model, embed string, ok bool) {
	p, ok := c.LLM.Providers["ollama"]
	if !ok || p.Kind != "ollama" {
		return "", "", false
	}
	return p.Model, p.EmbedModel, true
}

// OllamaBaseURL returns the ollama provider base URL, or the default local URL.
func (c *Config) OllamaBaseURL() string {
	if p, ok := c.LLM.Providers["ollama"]; ok && p.BaseURL != "" {
		return strings.TrimRight(p.BaseURL, "/")
	}
	return "http://127.0.0.1:11434"
}

func parseDuration(s, def string) (time.Duration, error) {
	if s == "" {
		s = def
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("%q must be > 0", s)
	}
	return d, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
