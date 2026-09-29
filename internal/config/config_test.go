package config_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"mayank2/internal/config"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func validConfigYAML(channelsDir string) string {
	return `
data_dir: ../data
timezone: Asia/Kolkata
queue:
  heavy_workers: 1
  light_workers: 4
  net_workers: 2
  heavy_window: "00:00-08:00"
telegram:
  daily_summary_at: "22:30"
dashboard:
  listen: ["127.0.0.1:7070"]
llm:
  providers:
    ollama: { kind: ollama, base_url: "http://127.0.0.1:11434", model: "qwen", embed_model: "nomic-embed-text" }
    groq: { kind: openai_compat, base_url: "https://api.groq.com/openai/v1", key_env: GROQ_API_KEY, model: "x" }
    claude: { kind: claude_cli, bin: "claude" }
  routes:
    research: [groq, ollama]
    blog: [claude]
content:
  channels_dir: ` + channelsDir + `
  ramp_up: true
  retention_days: 7
builder:
  enabled: false
  implementer_model: claude-opus-5-5
  auditor_model: claude-sonnet-5
  max_fix_attempts: 3
  run_timeout: 60m
  limit_pause: 30m
  repos: []
`
}

func validChannelYAML(id string) string {
	return `
id: ` + id + `
platform: youtube
handle: TBD
language: en
niche: money_side_hustles
brand: { primary: "#16A34A", secondary: "#0B1220", font_heading: "Montserrat", font_body: "Inter", caption_style: word-highlight }
voice: { engine: kokoro, voice_id: TBD, speed: 1.0 }
formats: [explained_60s]
windows: { tz: America/New_York, slots: ["12:30", "19:30"] }
disclaimer: "Educational content only."
`
}

func TestLoad_table(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(dir string)
		wantErr string
		check   func(t *testing.T, c *config.Config, dir string)
	}{
		{
			name: "happy path resolves relative paths and loads channels",
			mutate: func(dir string) {
				writeFile(t, filepath.Join(dir, "config", "config.yaml"), validConfigYAML("channels"))
				writeFile(t, filepath.Join(dir, "config", "channels", "a.yaml"), validChannelYAML("yt-a"))
			},
			check: func(t *testing.T, c *config.Config, dir string) {
				wantData := filepath.Clean(filepath.Join(dir, "data"))
				if c.DataDir != wantData {
					t.Fatalf("DataDir=%q want %q", c.DataDir, wantData)
				}
				wantCh := filepath.Clean(filepath.Join(dir, "config", "channels"))
				if c.Content.ChannelsDir != wantCh {
					t.Fatalf("ChannelsDir=%q want %q", c.Content.ChannelsDir, wantCh)
				}
				if len(c.Channels) != 1 || c.Channels[0].ID != "yt-a" {
					t.Fatalf("channels=%v", c.Channels)
				}
				if c.Location == nil || c.Location.String() != "Asia/Kolkata" {
					t.Fatalf("location=%v", c.Location)
				}
			},
		},
		{
			name: "invalid timezone",
			mutate: func(dir string) {
				body := strings.Replace(validConfigYAML("channels"), "Asia/Kolkata", "Not/AZone", 1)
				writeFile(t, filepath.Join(dir, "config", "config.yaml"), body)
			},
			wantErr: "timezone",
		},
		{
			name: "invalid heavy_window",
			mutate: func(dir string) {
				body := strings.Replace(validConfigYAML("channels"), `"00:00-08:00"`, `"25:00-08:00"`, 1)
				writeFile(t, filepath.Join(dir, "config", "config.yaml"), body)
			},
			wantErr: "heavy_window",
		},
		{
			name: "heavy_workers zero",
			mutate: func(dir string) {
				body := strings.Replace(validConfigYAML("channels"), "heavy_workers: 1", "heavy_workers: 0", 1)
				writeFile(t, filepath.Join(dir, "config", "config.yaml"), body)
			},
			wantErr: "heavy_workers",
		},
		{
			name: "unknown route provider",
			mutate: func(dir string) {
				body := strings.Replace(validConfigYAML("channels"), "research: [groq, ollama]", "research: [missing]", 1)
				writeFile(t, filepath.Join(dir, "config", "config.yaml"), body)
			},
			wantErr: "unknown provider",
		},
		{
			name: "bad builder.run_timeout",
			mutate: func(dir string) {
				body := strings.Replace(validConfigYAML("channels"), "run_timeout: 60m", "run_timeout: notaduration", 1)
				writeFile(t, filepath.Join(dir, "config", "config.yaml"), body)
			},
			wantErr: "run_timeout",
		},
		{
			name: "invalid channel missing id",
			mutate: func(dir string) {
				writeFile(t, filepath.Join(dir, "config", "config.yaml"), validConfigYAML("channels"))
				bad := strings.Replace(validChannelYAML("yt-a"), "id: yt-a\n", "id: \"\"\n", 1)
				writeFile(t, filepath.Join(dir, "config", "channels", "bad.yaml"), bad)
			},
			wantErr: "id is required",
		},
		{
			name: "duplicate channel ids",
			mutate: func(dir string) {
				writeFile(t, filepath.Join(dir, "config", "config.yaml"), validConfigYAML("channels"))
				writeFile(t, filepath.Join(dir, "config", "channels", "a.yaml"), validChannelYAML("same"))
				writeFile(t, filepath.Join(dir, "config", "channels", "b.yaml"), validChannelYAML("same"))
			},
			wantErr: "duplicate id",
		},
		{
			name: "ollama base_url not loopback",
			mutate: func(dir string) {
				body := strings.Replace(validConfigYAML("channels"),
					`base_url: "http://127.0.0.1:11434"`,
					`base_url: "http://example.com:11434"`, 1)
				writeFile(t, filepath.Join(dir, "config", "config.yaml"), body)
			},
			wantErr: "not loopback",
		},
		{
			name: "empty dashboard.listen",
			mutate: func(dir string) {
				body := strings.Replace(validConfigYAML("channels"), `listen: ["127.0.0.1:7070"]`, `listen: []`, 1)
				writeFile(t, filepath.Join(dir, "config", "config.yaml"), body)
			},
			wantErr: "dashboard.listen",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.mutate(dir)
			cfgPath := filepath.Join(dir, "config", "config.yaml")
			c, err := config.Load(cfgPath)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("want error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if tt.check != nil {
				tt.check(t, c, dir)
			}
		})
	}
}

func TestValidateChannel_table(t *testing.T) {
	base := config.Channel{
		ID:       "yt-x",
		Platform: "youtube",
		Language: "en",
		Niche:    "money",
		Brand:    config.BrandConfig{Primary: "#fff", Secondary: "#000"},
		Voice:    config.VoiceConfig{Engine: "kokoro", Speed: 1},
		Formats:  []string{"explained_60s"},
		Windows:  config.WindowConfig{TZ: "UTC", Slots: []string{"12:00"}},
	}
	tests := []struct {
		name    string
		mutate  func(*config.Channel)
		wantErr string
	}{
		{name: "ok", mutate: func(*config.Channel) {}},
		{
			name:    "bad slot",
			mutate:  func(c *config.Channel) { c.Windows.Slots = []string{"9am"} },
			wantErr: "HH:MM",
		},
		{
			name:    "zero speed",
			mutate:  func(c *config.Channel) { c.Voice.Speed = 0 },
			wantErr: "voice.speed",
		},
		{
			name:    "bad tz",
			mutate:  func(c *config.Channel) { c.Windows.TZ = "Nope/Zone" },
			wantErr: "windows.tz",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := base
			tt.mutate(&ch)
			err := config.ValidateChannel(ch)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("want %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestLoadEnvFile_and_RequiredEnvKeys(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	exPath := filepath.Join(dir, ".env.example")
	writeFile(t, envPath, "FOO=secret-value\nBAR=other\n")
	writeFile(t, exPath, "# comment\nFOO=\nBAZ=\nFOO=\n")

	t.Setenv("BAR", "already")
	if err := config.LoadEnvFile(envPath); err != nil {
		t.Fatalf("LoadEnvFile: %v", err)
	}
	if got := os.Getenv("FOO"); got != "secret-value" {
		t.Fatalf("FOO=%q", got)
	}
	if got := os.Getenv("BAR"); got != "already" {
		t.Fatalf("BAR should keep existing env, got %q", got)
	}

	keys, err := config.RequiredEnvKeys(exPath)
	if err != nil {
		t.Fatalf("RequiredEnvKeys: %v", err)
	}
	if len(keys) != 2 || keys[0] != "FOO" || keys[1] != "BAZ" {
		t.Fatalf("keys=%v", keys)
	}
	if !config.EnvKeyPresent("FOO") {
		t.Fatal("FOO should be present")
	}
	if config.EnvKeyPresent("BAZ") {
		t.Fatal("BAZ should be absent")
	}
}

func TestRequireLoopbackURL_table(t *testing.T) {
	tests := []struct {
		raw     string
		wantErr string
	}{
		{raw: "http://127.0.0.1:11434"},
		{raw: "http://localhost:11434"},
		{raw: "http://[::1]:11434"},
		{raw: "https://127.0.0.1"},
		{raw: "http://example.com:11434", wantErr: "not loopback"},
		{raw: "http://192.168.1.1:11434", wantErr: "not loopback"},
		{raw: "http://10.0.0.1", wantErr: "not loopback"},
		{raw: "ftp://127.0.0.1:11434", wantErr: "scheme"},
		{raw: "not a url", wantErr: "scheme"},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			err := config.RequireLoopbackURL(tt.raw)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("want %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestLoad_exampleConfig(t *testing.T) {
	// Load the repo's example + real channel files when present (worktree / main checkout).
	root := findRepoRoot(t)
	example := filepath.Join(root, "config", "config.example.yaml")
	if _, err := os.Stat(example); err != nil {
		t.Skip("config.example.yaml not found")
	}
	// Copy example into a temp config.yaml that points at the real channels dir via abs path.
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "config")
	channelsSrc := filepath.Join(root, "config", "channels")
	body, err := os.ReadFile(example)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	quoted := strconv.Quote(filepath.ToSlash(channelsSrc))
	text = strings.Replace(text, "channels_dir: channels", "channels_dir: "+quoted, 1)
	writeFile(t, filepath.Join(cfgDir, "config.yaml"), text)

	c, err := config.Load(filepath.Join(cfgDir, "config.yaml"))
	if err != nil {
		t.Fatalf("Load example: %v", err)
	}
	if len(c.Channels) < 4 {
		t.Fatalf("expected >=4 channels from repo, got %d", len(c.Channels))
	}
}

// TestLoad_blogSectionOptional covers M2-117's Blog config: absent/empty
// repo_path must leave the pipeline cleanly off (Enabled() false, no
// error), and a configured repo_path must resolve to an absolute path
// relative to the config file's directory, same as content.channels_dir.
func TestLoad_blogSectionOptional(t *testing.T) {
	dir := t.TempDir()
	channelsDir := filepath.Join(dir, "config", "channels")
	writeFile(t, filepath.Join(channelsDir, "ch1.yaml"), validChannelYAML("ch1"))

	cfgPath := filepath.Join(dir, "config", "config.yaml")
	writeFile(t, cfgPath, validConfigYAML("channels"))
	c, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load (no blog section): %v", err)
	}
	if c.Blog.Enabled() {
		t.Fatalf("Blog.Enabled() = true with no blog section, want false")
	}

	withBlog := validConfigYAML("channels") + "blog:\n  repo_path: ../Mayankbuilt\n  posts_dir: content/blog\n"
	writeFile(t, cfgPath, withBlog)
	c, err = config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load (with blog section): %v", err)
	}
	if !c.Blog.Enabled() {
		t.Fatal("Blog.Enabled() = false with repo_path set, want true")
	}
	if !filepath.IsAbs(c.Blog.RepoPath) {
		t.Fatalf("Blog.RepoPath = %q, want an absolute path", c.Blog.RepoPath)
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := wd
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "config", "config.example.yaml")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Skip("repo root with config.example.yaml not found")
	return ""
}
