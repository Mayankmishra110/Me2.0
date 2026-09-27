package content

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mayank2/internal/content/formats"
	"mayank2/internal/llm"
	"mayank2/internal/queue"
)

type scriptFakeCompleter struct {
	responses []llm.Response
	err       error
	calls     int
	last      llm.Request
	lastTask  llm.Task
}

func (f *scriptFakeCompleter) Complete(_ context.Context, task llm.Task, req llm.Request) (llm.Response, error) {
	f.lastTask = task
	f.last = req
	f.calls++
	if f.err != nil {
		return llm.Response{}, f.err
	}
	if task != llm.TaskScript {
		return llm.Response{}, errors.New("unexpected task")
	}
	i := f.calls - 1
	if i >= len(f.responses) {
		i = len(f.responses) - 1
	}
	if i < 0 {
		return llm.Response{}, errors.New("no fake responses")
	}
	return f.responses[i], nil
}

func sampleBrief() Brief {
	return Brief{
		Topic: "Freelancer income myths",
		Angle: "Debunk the 'easy $10k month' myth with real averages",
		Facts: []Fact{
			{Claim: "US freelancers average about $52,000 a year", Sources: []string{"https://example.com/ok"}},
			{Claim: "Side hustles take consistent hours, not luck", Sources: []string{"https://example.com/ai"}},
		},
		KeyNumbers: []KeyNumber{
			{Label: "avg income", Value: "$52,000", Source: "https://example.com/ok"},
		},
		OpenQuestions: []string{"Tax treatment varies by state"},
	}
}

func validENScriptJSON() string {
	return `{
  "long": {
    "hook": "Most freelancers never hit that viral income number.",
    "beats": [
      {"text": "Surveys put average US freelancer income near fifty-two thousand a year.", "visual_cue": "chart overlay"},
      {"text": "The gap is hours and niche, not a secret hack.", "visual_cue": "calendar montage"},
      {"text": "Treat it like a job: skills, clients, and boring consistency.", "visual_cue": "desk B-roll"}
    ],
    "cta": "Subscribe for more money myths, busted.",
    "title": "Freelancer Income Myth vs Reality",
    "description": "What the averages actually say about side hustle income.",
    "tags": ["freelance", "side hustle", "income", "money myths"],
    "thumbnail_text": "Myth Bust Income"
  },
  "short": {
    "hook": "That easy ten-k month? Rare.",
    "beats": [
      {"text": "Average US freelancer income sits near fifty-two thousand a year.", "visual_cue": "big number card"},
      {"text": "Consistency beats hacks every time.", "visual_cue": "loop end card"}
    ],
    "cta": "Follow for the long version.",
    "title": "Freelancer Income Reality Check",
    "description": "Quick myth check on freelancer averages.",
    "tags": ["freelance", "shorts", "money"],
    "thumbnail_text": "Not Easy Money"
  }
}`
}

func validHIScriptJSON() string {
	return `{
  "long": {
    "hook": "ज़्यादातर फ्रीलांसर वायरल इनकम नंबर तक नहीं पहुँचते।",
    "beats": [
      {"text": "सर्वे के अनुसार अमेरिकी फ्रीलांसरों की औसत कमाई करीब बावन हज़ार डॉलर सालाना है।", "visual_cue": "चार्ट"},
      {"text": "अंतर घंटों और निच में है, किसी सीक्रेट हैक में नहीं।", "visual_cue": "कैलेंडर"},
      {"text": "इसे नौकरी जैसा समझें: स्किल, क्लाइंट, और नियमित मेहनत।", "visual_cue": "डेस्क"}
    ],
    "cta": "और मिथक तोड़ने के लिए सब्सक्राइब करें।",
    "title": "फ्रीलांसर इनकम मिथक बनाम सच",
    "description": "औसत आंकड़े साइड हसल इनकम के बारे में क्या कहते हैं।",
    "tags": ["फ्रीलांस", "साइड हसल", "पैसे"],
    "thumbnail_text": "मिथक तोड़ो"
  },
  "short": {
    "hook": "आसान दस हज़ार डॉलर महीना? दुर्लभ।",
    "beats": [
      {"text": "औसत अमेरिकी फ्रीलांसर इनकम करीब बावन हज़ार डॉलर सालाना है।", "visual_cue": "बड़ा नंबर"},
      {"text": "नियमितता हैक से बड़ी है।", "visual_cue": "लूप कार्ड"}
    ],
    "cta": "लंबी वर्शन के लिए फॉलो करें।",
    "title": "फ्रीलांसर इनकम रियलिटी",
    "description": "फ्रीलांसर औसत पर तेज़ मिथक चेक।",
    "tags": ["फ्रीलांस", "शॉर्ट्स"],
    "thumbnail_text": "आसान नहीं"
  }
}`
}

func TestNewScript_requiresFields(t *testing.T) {
	if _, err := NewScript(ScriptOptions{}); err == nil {
		t.Fatal("expected error for empty options")
	}
	if _, err := NewScript(ScriptOptions{Completer: &scriptFakeCompleter{}}); err == nil {
		t.Fatal("expected OutDir required")
	}
}

func TestScriptRun_englishWritesScriptJSON(t *testing.T) {
	dir := t.TempDir()
	fc := &scriptFakeCompleter{
		responses: []llm.Response{{Text: validENScriptJSON(), Provider: "fake", Model: "test"}},
	}
	s, err := NewScript(ScriptOptions{OutDir: dir, Completer: fc})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := s.Run(context.Background(), ScriptInput{
		ChannelID: "yt-money-en",
		Language:  LanguageEN,
		Topic:     "Freelancer income myths",
		Brief:     sampleBrief(),
		Allowed:   []string{"myth_vs_fact", "how_to", "top_n"},
		Format:    "myth_vs_fact",
		HookStyle: "myth_callout",
	})
	if err != nil {
		t.Fatal(err)
	}
	if fc.lastTask != llm.TaskScript {
		t.Fatalf("task=%q", fc.lastTask)
	}
	if !strings.Contains(fc.last.System, "ORIGINAL English") {
		t.Fatalf("EN system prompt missing craft/originality: %q", fc.last.System[:min(80, len(fc.last.System))])
	}
	if !strings.Contains(fc.last.System, "thumbnail_text ≤ 4 words") {
		t.Fatal("EN prompt missing craft rule for thumbnail")
	}
	if len(fc.last.JSONSchema) == 0 {
		t.Fatal("expected JSON schema")
	}
	if doc.Format != "myth_vs_fact" || doc.HookStyle != "myth_callout" {
		t.Fatalf("format/hook = %q/%q", doc.Format, doc.HookStyle)
	}
	if doc.Long.Hook == "" || len(doc.Long.Beats) == 0 || doc.Short.CTA == "" {
		t.Fatalf("incomplete script: %+v", doc)
	}
	if len(doc.Long.Tags) > 15 {
		t.Fatal("too many tags")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "script.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fromDisk ScriptDoc
	if err := json.Unmarshal(raw, &fromDisk); err != nil {
		t.Fatal(err)
	}
	if fromDisk.Short.ThumbnailText != "Not Easy Money" {
		t.Fatalf("thumb=%q", fromDisk.Short.ThumbnailText)
	}
}

func TestScriptRun_hindiNativePromptAndDevanagari(t *testing.T) {
	dir := t.TempDir()
	fc := &scriptFakeCompleter{
		responses: []llm.Response{{Text: validHIScriptJSON(), Provider: "fake", Model: "hi"}},
	}
	s, err := NewScript(ScriptOptions{OutDir: dir, Completer: fc})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := s.Run(context.Background(), ScriptInput{
		ChannelID: "yt-money-hi",
		Language:  LanguageHI,
		Brief:     sampleBrief(),
		Topic:     "फ्रीलांसर इनकम मिथक",
		Allowed:   []string{"myth_vs_fact"},
		Format:    "myth_vs_fact",
		HookStyle: "question",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fc.last.System, "देवनागरी") {
		t.Fatalf("HI prompt should ask for Devanagari: %q", fc.last.System[:min(120, len(fc.last.System))])
	}
	if !strings.Contains(fc.last.Messages[0].Content, "देवनागरी") {
		t.Fatal("HI user prompt should reinforce Devanagari")
	}
	if !hasDevanagari(doc.Long.Hook) {
		t.Fatalf("long hook not Devanagari: %q", doc.Long.Hook)
	}
	if !hasDevanagari(doc.Short.Beats[0].Text) {
		t.Fatal("short beat missing Devanagari")
	}
}

func TestScriptRun_appliesRedoNotes(t *testing.T) {
	dir := t.TempDir()
	fc := &scriptFakeCompleter{
		responses: []llm.Response{{Text: validENScriptJSON(), Provider: "fake", Model: "test"}},
	}
	s, err := NewScript(ScriptOptions{OutDir: dir, Completer: fc})
	if err != nil {
		t.Fatal(err)
	}
	note := "Open with the $52k figure, drop the subscribe CTA wording"
	doc, err := s.Run(context.Background(), ScriptInput{
		ChannelID: "yt-money-en",
		Language:  LanguageEN,
		Topic:     "Freelancer income myths",
		Brief:     sampleBrief(),
		Allowed:   []string{"myth_vs_fact"},
		Format:    "myth_vs_fact",
		HookStyle: "number_lead",
		RedoNotes: note,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fc.last.Messages[0].Content, "REDO NOTES") {
		t.Fatal("user prompt missing REDO NOTES section")
	}
	if !strings.Contains(fc.last.Messages[0].Content, note) {
		t.Fatalf("redo note not in prompt: %q", fc.last.Messages[0].Content)
	}
	if doc.RedoNotes != note {
		t.Fatalf("doc.RedoNotes=%q", doc.RedoNotes)
	}
}

func TestScriptRun_usesFormatPicker(t *testing.T) {
	dir := t.TempDir()
	fc := &scriptFakeCompleter{
		responses: []llm.Response{{Text: validENScriptJSON(), Provider: "fake", Model: "test"}},
	}
	s, err := NewScript(ScriptOptions{OutDir: dir, Completer: fc})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := s.Run(context.Background(), ScriptInput{
		ChannelID: "yt-ai-en",
		Language:  LanguageEN,
		Topic:     "best AI tools list for freelancers",
		Brief:     sampleBrief(),
		Allowed:   []string{"explained_60s", "top_n", "how_to", "myth_vs_fact"},
		FormatScores: map[string]float64{
			"top_n":  8,
			"how_to": 1,
		},
		Recent: []formats.HistoryEntry{
			{Format: "how_to", HookStyle: "question"},
			{Format: "how_to", HookStyle: "bold_promise"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Format == "" || doc.HookStyle == "" {
		t.Fatalf("picker did not set format/hook: %+v", doc.Pick)
	}
	if doc.Format == "how_to" {
		t.Fatal("G6 should block how_to after two in a row")
	}
	if !strings.Contains(fc.last.Messages[0].Content, "Format: "+doc.Format) {
		t.Fatalf("prompt missing chosen format %q", doc.Format)
	}
}

func TestScriptRun_invalidThenPermanent(t *testing.T) {
	dir := t.TempDir()
	bad := `{"long":{"hook":"x","beats":[{"text":"a","visual_cue":"b"}],"cta":"c","title":"t","description":"d","tags":[],"thumbnail_text":"one two three four five"},"short":{"hook":"x","beats":[{"text":"a","visual_cue":"b"}],"cta":"c","title":"t","description":"d","tags":[],"thumbnail_text":"one two three four five"}}`
	fc := &scriptFakeCompleter{
		responses: []llm.Response{
			{Text: bad, Provider: "fake", Model: "test"},
			{Text: bad, Provider: "fake", Model: "test"},
		},
	}
	s, err := NewScript(ScriptOptions{OutDir: dir, Completer: fc})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Run(context.Background(), ScriptInput{
		ChannelID: "yt-money-en",
		Language:  LanguageEN,
		Topic:     "x",
		Brief:     sampleBrief(),
		Allowed:   []string{"top_n"},
		Format:    "top_n",
		HookStyle: "question",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !queue.IsPermanent(err) {
		t.Fatalf("want Permanent, got %v", err)
	}
	if fc.calls != 2 {
		t.Fatalf("calls=%d want 2 (one retry)", fc.calls)
	}
}

func TestScriptRun_schemaRetryThenOK(t *testing.T) {
	dir := t.TempDir()
	fc := &scriptFakeCompleter{
		responses: []llm.Response{
			{Text: `not-json`, Provider: "fake", Model: "test"},
			{Text: validENScriptJSON(), Provider: "fake", Model: "test"},
		},
	}
	s, err := NewScript(ScriptOptions{OutDir: dir, Completer: fc})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := s.Run(context.Background(), ScriptInput{
		ChannelID: "yt-money-en",
		Language:  LanguageEN,
		Topic:     "Freelancer income myths",
		Brief:     sampleBrief(),
		Allowed:   []string{"myth_vs_fact"},
		Format:    "myth_vs_fact",
		HookStyle: "question",
	})
	if err != nil {
		t.Fatal(err)
	}
	if fc.calls != 2 {
		t.Fatalf("calls=%d", fc.calls)
	}
	if doc.Long.Title == "" {
		t.Fatal("empty title after retry")
	}
}

func TestValidateVariant_limits(t *testing.T) {
	v := ScriptVariant{
		Hook: "h", Beats: []Beat{{Text: "t", VisualCue: "v"}}, CTA: "c",
		Title: strings.Repeat("a", 61), Description: "d", Tags: make([]string, 16),
		ThumbnailText: "one two three four five",
	}
	if err := validateVariant("long", v, LanguageEN); err == nil {
		t.Fatal("expected title length error")
	}
	v.Title = "ok"
	if err := validateVariant("long", v, LanguageEN); err == nil || !strings.Contains(err.Error(), "tags") {
		t.Fatalf("want tags error, got %v", err)
	}
	v.Tags = []string{"a"}
	if err := validateVariant("long", v, LanguageEN); err == nil || !strings.Contains(err.Error(), "thumbnail") {
		t.Fatalf("want thumb error, got %v", err)
	}
}

func TestFormatsPromptsEmbedded(t *testing.T) {
	if strings.TrimSpace(formats.ScriptPromptEN) == "" {
		t.Fatal("empty EN prompt embed")
	}
	if strings.TrimSpace(formats.ScriptPromptHI) == "" {
		t.Fatal("empty HI prompt embed")
	}
}
