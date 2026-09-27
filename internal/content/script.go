// Script stage (M2-204): native EN/HI scripts from a research brief.
// Format picker lives in formats/; prompts encode CONTENT_STRATEGY §4 craft rules.
package content

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"mayank2/internal/content/formats"
	"mayank2/internal/llm"
	"mayank2/internal/queue"
)

// ScriptOptions configures Script. Zero values mean defaults.
type ScriptOptions struct {
	OutDir    string // required; receives script.json
	Completer Completer
	Logger    *slog.Logger
}

// Script writes original long+short scripts for one channel/language.
type Script struct {
	opts ScriptOptions
	log  *slog.Logger
}

// NewScript returns a Script stage.
func NewScript(opts ScriptOptions) (*Script, error) {
	if opts.Completer == nil {
		return nil, fmt.Errorf("script: Completer is required")
	}
	if strings.TrimSpace(opts.OutDir) == "" {
		return nil, fmt.Errorf("script: OutDir is required")
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Script{opts: opts, log: log}, nil
}

// ScriptInput is one script.write job.
type ScriptInput struct {
	ChannelID    string
	Language     Language // en | hi
	Topic        string
	Brief        Brief
	Allowed      []string               // channel formats
	FormatScores map[string]float64     // optional past format scores
	Recent       []formats.HistoryEntry // G6 history, most recent first
	RedoNotes    string                 // approval redo notes (applied when set)
	// Format/HookStyle force a pick (tests); empty → picker.
	Format    string
	HookStyle string
}

// Beat is one spoken beat with a visual cue.
type Beat struct {
	Text      string `json:"text"`
	VisualCue string `json:"visual_cue"`
}

// ScriptVariant is one length variant (long or short).
type ScriptVariant struct {
	Hook          string   `json:"hook"`
	Beats         []Beat   `json:"beats"`
	CTA           string   `json:"cta"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	Tags          []string `json:"tags"`
	ThumbnailText string   `json:"thumbnail_text"`
}

// ScriptDoc is script.json (ARCHITECTURE §3.1 + M2-204 AC).
type ScriptDoc struct {
	ChannelID string         `json:"channel_id"`
	Topic     string         `json:"topic"`
	Language  string         `json:"language"`
	Format    string         `json:"format"`
	HookStyle string         `json:"hook_style"`
	Long      ScriptVariant  `json:"long"`
	Short     ScriptVariant  `json:"short"`
	RedoNotes string         `json:"redo_notes,omitempty"`
	Provider  string         `json:"provider,omitempty"`
	Model     string         `json:"model,omitempty"`
	Pick      formats.Choice `json:"pick,omitempty"`
}

var scriptSchema = json.RawMessage(`{
  "type": "object",
  "required": ["long", "short"],
  "properties": {
    "long": {
      "type": "object",
      "required": ["hook", "beats", "cta", "title", "description", "tags", "thumbnail_text"],
      "properties": {
        "hook": {"type": "string"},
        "beats": {
          "type": "array",
          "items": {
            "type": "object",
            "required": ["text", "visual_cue"],
            "properties": {
              "text": {"type": "string"},
              "visual_cue": {"type": "string"}
            }
          }
        },
        "cta": {"type": "string"},
        "title": {"type": "string"},
        "description": {"type": "string"},
        "tags": {"type": "array", "items": {"type": "string"}},
        "thumbnail_text": {"type": "string"}
      }
    },
    "short": {
      "type": "object",
      "required": ["hook", "beats", "cta", "title", "description", "tags", "thumbnail_text"],
      "properties": {
        "hook": {"type": "string"},
        "beats": {
          "type": "array",
          "items": {
            "type": "object",
            "required": ["text", "visual_cue"],
            "properties": {
              "text": {"type": "string"},
              "visual_cue": {"type": "string"}
            }
          }
        },
        "cta": {"type": "string"},
        "title": {"type": "string"},
        "description": {"type": "string"},
        "tags": {"type": "array", "items": {"type": "string"}},
        "thumbnail_text": {"type": "string"}
      }
    }
  }
}`)

// Run picks a format (unless forced), completes long+short scripts, writes script.json.
func (s *Script) Run(ctx context.Context, in ScriptInput) (*ScriptDoc, error) {
	topic := strings.TrimSpace(in.Topic)
	if topic == "" {
		topic = strings.TrimSpace(in.Brief.Topic)
	}
	if topic == "" {
		return nil, fmt.Errorf("script: topic is required")
	}
	lang := in.Language
	if lang == "" {
		return nil, fmt.Errorf("script: language is required")
	}
	if lang != LanguageEN && lang != LanguageHI {
		return nil, fmt.Errorf("script: language %q not supported", lang)
	}
	if len(in.Allowed) == 0 {
		return nil, fmt.Errorf("script: Allowed formats required")
	}
	if err := os.MkdirAll(s.opts.OutDir, 0o755); err != nil {
		return nil, fmt.Errorf("script: mkdir: %w", err)
	}

	var pick formats.Choice
	formatID := strings.TrimSpace(in.Format)
	hookStyle := strings.TrimSpace(in.HookStyle)
	if formatID == "" {
		var err error
		pick, err = formats.Pick(formats.PickInput{
			Allowed:         in.Allowed,
			Topic:           topic,
			FormatScores:    in.FormatScores,
			Recent:          in.Recent,
			PreferBoth:      true,
			ExploreFraction: -1, // default 20%
		})
		if err != nil {
			return nil, fmt.Errorf("script: format pick: %w", err)
		}
		formatID = pick.Format
		hookStyle = pick.HookStyle
	} else if hookStyle == "" {
		hookStyle = formats.HookStyles[0]
	}

	doc, err := s.complete(ctx, in, topic, lang, formatID, hookStyle)
	if err != nil {
		return nil, err
	}
	doc.ChannelID = in.ChannelID
	doc.Topic = topic
	doc.Language = string(lang)
	doc.Format = formatID
	doc.HookStyle = hookStyle
	doc.Pick = pick
	if notes := strings.TrimSpace(in.RedoNotes); notes != "" {
		doc.RedoNotes = notes
	}

	path := filepath.Join(s.opts.OutDir, "script.json")
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("script: marshal: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return nil, fmt.Errorf("script: write script.json: %w", err)
	}
	return doc, nil
}

func (s *Script) complete(ctx context.Context, in ScriptInput, topic string, lang Language, formatID, hookStyle string) (*ScriptDoc, error) {
	system := strings.TrimSpace(formats.ScriptPromptEN)
	if lang == LanguageHI {
		system = strings.TrimSpace(formats.ScriptPromptHI)
	}
	if system == "" {
		return nil, fmt.Errorf("script: empty system prompt for language %q", lang)
	}
	user := buildScriptUserPrompt(in, topic, lang, formatID, hookStyle)

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		msgs := []llm.Message{{Role: "user", Content: user}}
		if attempt > 0 && lastErr != nil {
			msgs = append(msgs,
				llm.Message{Role: "assistant", Content: "{}"},
				llm.Message{Role: "user", Content: "Previous JSON was invalid (" + lastErr.Error() + "). Reply with corrected JSON only."},
			)
		}
		resp, err := s.opts.Completer.Complete(ctx, llm.TaskScript, llm.Request{
			System:     system,
			Messages:   msgs,
			MaxTokens:  4096,
			JSONSchema: scriptSchema,
		})
		if err != nil {
			return nil, fmt.Errorf("script: llm: %w", err)
		}
		text := strings.TrimSpace(resp.Text)
		if strings.HasPrefix(text, "```") {
			text = stripMarkdownFence(text)
		}
		if err := llm.ValidateJSONSchema(scriptSchema, text); err != nil {
			lastErr = err
			s.log.Info("script: schema retry", "attempt", attempt+1, "err", err.Error())
			continue
		}
		var parsed struct {
			Long  ScriptVariant `json:"long"`
			Short ScriptVariant `json:"short"`
		}
		if err := json.Unmarshal([]byte(text), &parsed); err != nil {
			lastErr = err
			continue
		}
		doc := &ScriptDoc{
			Long:     parsed.Long,
			Short:    parsed.Short,
			Provider: resp.Provider,
			Model:    resp.Model,
		}
		if err := validateScriptDoc(doc, lang); err != nil {
			lastErr = err
			s.log.Info("script: validation retry", "attempt", attempt+1, "err", err.Error())
			continue
		}
		return doc, nil
	}
	return nil, queue.Permanent(fmt.Errorf("script: invalid llm json after retry: %w", lastErr))
}

func buildScriptUserPrompt(in ScriptInput, topic string, lang Language, formatID, hookStyle string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Topic: %s\n", topic)
	fmt.Fprintf(&b, "Language: %s\n", lang)
	fmt.Fprintf(&b, "Format: %s\n", formatID)
	fmt.Fprintf(&b, "Hook style: %s\n", hookStyle)
	if info, ok := formats.Lookup(formatID); ok {
		fmt.Fprintf(&b, "Format label: %s\n", info.Label)
	}
	fmt.Fprintf(&b, "\nBrief angle: %s\n", in.Brief.Angle)
	b.WriteString("Facts:\n")
	for i, f := range in.Brief.Facts {
		fmt.Fprintf(&b, "- [%d] %s (sources: %s)\n", i+1, f.Claim, strings.Join(f.Sources, ", "))
	}
	if len(in.Brief.KeyNumbers) > 0 {
		b.WriteString("Key numbers:\n")
		for _, n := range in.Brief.KeyNumbers {
			fmt.Fprintf(&b, "- %s: %s (%s)\n", n.Label, n.Value, n.Source)
		}
	}
	if len(in.Brief.OpenQuestions) > 0 {
		b.WriteString("Open questions (do not invent answers):\n")
		for _, q := range in.Brief.OpenQuestions {
			fmt.Fprintf(&b, "- %s\n", q)
		}
	}
	if notes := strings.TrimSpace(in.RedoNotes); notes != "" {
		fmt.Fprintf(&b, "\nREDO NOTES from approval (must apply):\n%s\n", notes)
	}
	if lang == LanguageHI {
		b.WriteString("\nपूरी स्क्रिप्ट देवनागरी हिंदी में लिखें। JSON अभी दें।\n")
	} else {
		b.WriteString("\nProduce the script JSON now.\n")
	}
	return b.String()
}

func validateScriptDoc(doc *ScriptDoc, lang Language) error {
	for _, pair := range []struct {
		name string
		v    ScriptVariant
	}{
		{"long", doc.Long},
		{"short", doc.Short},
	} {
		if err := validateVariant(pair.name, pair.v, lang); err != nil {
			return err
		}
	}
	return nil
}

func validateVariant(name string, v ScriptVariant, lang Language) error {
	if strings.TrimSpace(v.Hook) == "" {
		return fmt.Errorf("%s.hook empty", name)
	}
	if len(v.Beats) == 0 {
		return fmt.Errorf("%s.beats empty", name)
	}
	for i, beat := range v.Beats {
		if strings.TrimSpace(beat.Text) == "" {
			return fmt.Errorf("%s.beats[%d].text empty", name, i)
		}
		if strings.TrimSpace(beat.VisualCue) == "" {
			return fmt.Errorf("%s.beats[%d].visual_cue empty", name, i)
		}
	}
	if strings.TrimSpace(v.CTA) == "" {
		return fmt.Errorf("%s.cta empty", name)
	}
	if strings.TrimSpace(v.Title) == "" {
		return fmt.Errorf("%s.title empty", name)
	}
	if utf8.RuneCountInString(v.Title) > 60 {
		return fmt.Errorf("%s.title exceeds 60 characters", name)
	}
	if strings.TrimSpace(v.Description) == "" {
		return fmt.Errorf("%s.description empty", name)
	}
	if len(v.Tags) > 15 {
		return fmt.Errorf("%s.tags has %d (>15)", name, len(v.Tags))
	}
	words := countWords(v.ThumbnailText)
	if words == 0 {
		return fmt.Errorf("%s.thumbnail_text empty", name)
	}
	if words > 4 {
		return fmt.Errorf("%s.thumbnail_text has %d words (>4)", name, words)
	}
	if lang == LanguageHI {
		if !hasDevanagari(v.Hook) && !hasDevanagari(v.Beats[0].Text) {
			return fmt.Errorf("%s: Hindi script missing Devanagari", name)
		}
	}
	return nil
}

func countWords(s string) int {
	return len(strings.Fields(strings.TrimSpace(s)))
}

func hasDevanagari(s string) bool {
	for _, r := range s {
		if unicode.In(r, unicode.Devanagari) {
			return true
		}
	}
	return false
}
