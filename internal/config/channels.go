package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Channel is one entry under config/channels/*.yaml.
type Channel struct {
	ID         string       `yaml:"id"`
	Platform   string       `yaml:"platform"`
	Handle     string       `yaml:"handle"`
	Language   string       `yaml:"language"`
	Niche      string       `yaml:"niche"`
	Brand      BrandConfig  `yaml:"brand"`
	Voice      VoiceConfig  `yaml:"voice"`
	Formats    []string     `yaml:"formats"`
	Windows    WindowConfig `yaml:"windows"`
	Disclaimer *string      `yaml:"disclaimer"`
	SourceFile string       `yaml:"-"`
}

type BrandConfig struct {
	Primary      string `yaml:"primary"`
	Secondary    string `yaml:"secondary"`
	FontHeading  string `yaml:"font_heading"`
	FontBody     string `yaml:"font_body"`
	CaptionStyle string `yaml:"caption_style"`
}

type VoiceConfig struct {
	Engine  string  `yaml:"engine"`
	VoiceID string  `yaml:"voice_id"`
	Speed   float64 `yaml:"speed"`
}

type WindowConfig struct {
	TZ    string   `yaml:"tz"`
	Slots []string `yaml:"slots"`
}

func (c *Config) loadChannels() error {
	dir := c.Content.ChannelsDir
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			c.Channels = nil
			return nil
		}
		return fmt.Errorf("read channels dir %s: %w", dir, err)
	}
	var channels []Channel
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		path := filepath.Join(dir, name)
		ch, err := LoadChannel(path)
		if err != nil {
			return err
		}
		channels = append(channels, *ch)
	}
	c.Channels = channels
	return nil
}

// LoadChannel reads and validates a single channel YAML file.
func LoadChannel(path string) (*Channel, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read channel %s: %w", path, err)
	}
	ch := &Channel{}
	if err := yaml.Unmarshal(raw, ch); err != nil {
		return nil, fmt.Errorf("parse channel %s: %w", path, err)
	}
	ch.SourceFile = path
	if err := ValidateChannel(*ch); err != nil {
		return nil, fmt.Errorf("channel %s: %w", path, err)
	}
	return ch, nil
}

// ValidateChannel checks required channel fields.
func ValidateChannel(ch Channel) error {
	if ch.ID == "" {
		return fmt.Errorf("id is required")
	}
	if ch.Platform == "" {
		return fmt.Errorf("platform is required")
	}
	if ch.Language == "" {
		return fmt.Errorf("language is required")
	}
	if ch.Niche == "" {
		return fmt.Errorf("niche is required")
	}
	if ch.Brand.Primary == "" || ch.Brand.Secondary == "" {
		return fmt.Errorf("brand.primary and brand.secondary are required")
	}
	if ch.Voice.Engine == "" {
		return fmt.Errorf("voice.engine is required")
	}
	if ch.Voice.Speed <= 0 {
		return fmt.Errorf("voice.speed must be > 0, got %v", ch.Voice.Speed)
	}
	if len(ch.Formats) == 0 {
		return fmt.Errorf("formats must list at least one format")
	}
	if ch.Windows.TZ == "" {
		return fmt.Errorf("windows.tz is required")
	}
	if _, err := time.LoadLocation(ch.Windows.TZ); err != nil {
		return fmt.Errorf("windows.tz %q: %w", ch.Windows.TZ, err)
	}
	if len(ch.Windows.Slots) == 0 {
		return fmt.Errorf("windows.slots must list at least one slot")
	}
	for i, slot := range ch.Windows.Slots {
		if !hhmmRe.MatchString(slot) {
			return fmt.Errorf("windows.slots[%d] %q: want HH:MM", i, slot)
		}
	}
	return nil
}
