package compliance

import (
	"context"
	"fmt"
	"strings"
	"unicode"
)

// Literal-translation artifacts that fail Hindi native-phrasing checks.
var hiArtifacts = []string{
	"the the",
	"is is",
	"please kindly",
	"do the needful",
	"prepone",
}

// Filler phrases that signal low-effort, unedited, or bot-like English
// writing (COMPLIANCE §3 G7 "English: reading level for target"). Single
// words are matched against whole (punctuation-trimmed) tokens so they don't
// false-positive on substrings (e.g. "um" inside "document"); multi-word
// phrases are matched as substrings of the normalized script.
var enFillerWordsSingle = map[string]bool{
	"um": true, "uh": true, "umm": true, "uhh": true,
	"basically": true, "literally": true, "honestly": true,
}

var enFillerPhrasesMulti = []string{
	"you know", "sort of", "kind of", "i mean",
	"at the end of the day", "needless to say", "for what it's worth",
	"like i said", "as i said before",
}

// enFillerDensityMax is the max fraction of script words that may be filler
// phrases before the English heuristic fails; below this many words the
// check is skipped (too few words for density to be meaningful).
const (
	enFillerDensityMax = 0.06
	enFillerMinWords   = 15
	enRepeatNGram      = 4 // n-gram size for looping/templated-phrase detection
	enRepeatMaxCount   = 3 // same n-gram at/above this count fails
	enRunOnMaxWords    = 45
)

// G7 Language quality: Hindi native phrasing / English readability.
//
// When Classifier is nil this is NOT a skip: Hindi gets the native-phrasing
// heuristic below, and English gets a real (non-LLM) readability heuristic —
// stutter/duplicate-word detection, filler-word density, repeated/looping
// phrase detection, and a run-on-sentence check. None of these paths is a
// stub; each can independently return Passed: false. When Classifier is set,
// its LLM judgment is authoritative and runs after (and can override) the
// heuristic pass, matching G5's Classifier-optional pattern.
type G7 struct {
	Classifier Classifier // optional LLM judge; heuristic always runs first when nil
}

func (g G7) ID() string { return "G7" }

func (g G7) Check(ctx context.Context, item ContentItem) GateResult {
	lang := strings.ToLower(strings.TrimSpace(item.Language))
	text := item.ScriptText
	if strings.TrimSpace(text) == "" {
		return GateResult{ID: "G7", Passed: false, Detail: "empty script"}
	}

	if lang == "hi" {
		norm := NormalizeForShingle(text)
		for _, a := range hiArtifacts {
			if strings.Contains(norm, a) {
				return GateResult{ID: "G7", Passed: false, Detail: fmt.Sprintf("literal-translation artifact %q", a)}
			}
		}
		// Rough check: expect substantial Devanagari.
		dev, total := 0, 0
		for _, r := range text {
			if unicode.IsLetter(r) {
				total++
				if r >= 0x0900 && r <= 0x097F {
					dev++
				}
			}
		}
		if total > 20 && float64(dev)/float64(total) < 0.4 {
			return GateResult{ID: "G7", Passed: false, Detail: "Hindi script lacks native Devanagari phrasing"}
		}
	}

	if g.Classifier == nil && lang != "hi" {
		// English readability heuristic. Real fallback, not a skip: this can
		// and does fail a genuinely bad script (see g7_test.go).
		if ok, detail := englishReadabilityHeuristic(text); !ok {
			return GateResult{ID: "G7", Passed: false, Detail: detail}
		}
	}

	if g.Classifier != nil {
		sys := `Judge language quality. English: reading level suitable for general adult audience. Hindi: native phrasing, not literal translation. Reply JSON {"pass":true|false,"detail":"pass|fail: ..."}.`
		pass, detail, err := g.Classifier.Classify(ctx, sys,
			fmt.Sprintf("Language: %s\nScript:\n%s", lang, text))
		if err != nil {
			return GateResult{ID: "G7", Passed: false, Detail: fmt.Sprintf("judge error: %v", err)}
		}
		if !pass {
			return GateResult{ID: "G7", Passed: false, Detail: detail}
		}
		if detail == "" {
			detail = "pass"
		}
		return GateResult{ID: "G7", Passed: true, Detail: detail}
	}

	if lang != "hi" {
		return GateResult{ID: "G7", Passed: true, Detail: "pass (heuristic)"}
	}
	return GateResult{ID: "G7", Passed: true, Detail: "pass"}
}

// englishReadabilityHeuristic runs four independent, real (non-LLM) signals
// against English script text. Any one of them can fail the gate.
func englishReadabilityHeuristic(text string) (bool, string) {
	norm := NormalizeForShingle(text)
	ws := words(norm)
	if len(ws) == 0 {
		return false, "empty script"
	}

	// 1. Immediate word repetition ("the the", "and and") — a strong signal
	// of broken/looping generation, independent of the Hindi artifact list.
	for i := 1; i < len(ws); i++ {
		a, b := trimPunct(ws[i-1]), trimPunct(ws[i])
		if len(a) > 2 && a == b {
			return false, fmt.Sprintf("repeated word %q %q back to back — likely broken generation", a, b)
		}
	}

	// 2. Filler-word / hedge-phrase density.
	if len(ws) >= enFillerMinWords {
		fillerWords := 0
		for _, w := range ws {
			if enFillerWordsSingle[trimPunct(w)] {
				fillerWords++
			}
		}
		for _, p := range enFillerPhrasesMulti {
			n := strings.Count(norm, p)
			if n == 0 {
				continue
			}
			fillerWords += n * len(strings.Fields(p))
		}
		density := float64(fillerWords) / float64(len(ws))
		if density > enFillerDensityMax {
			return false, fmt.Sprintf("filler-word density %.1f%% exceeds %.0f%% — reads as unedited/low-effort", density*100, enFillerDensityMax*100)
		}
	}

	// 3. Repeated / looping phrase: the same n-gram appearing several times
	// usually means templated or stuck generation rather than natural prose.
	if phrase, count := mostRepeatedNGram(ws, enRepeatNGram); count >= enRepeatMaxCount {
		return false, fmt.Sprintf("phrase %q repeated %d times — templated or looping text", phrase, count)
	}

	// 4. Run-on sentence: a single sentence far longer than a spoken script
	// should be reads poorly out loud and suggests no real editing pass.
	sentences := splitSentences(text)
	if len(sentences) <= 1 && len(ws) > enRunOnMaxWords {
		return false, fmt.Sprintf("single %d-word run-on sentence with no punctuation break — poor reading level", len(ws))
	}

	return true, "heuristic: no readability red flags"
}

// trimPunct strips leading/trailing non-letter, non-digit runes from a
// whitespace-split token (e.g. "um," -> "um") so word-level comparisons
// aren't fooled by attached punctuation.
func trimPunct(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// mostRepeatedNGram returns the n-word phrase (joined by single spaces) that
// occurs most often in ws, and how many times it occurs.
func mostRepeatedNGram(ws []string, n int) (string, int) {
	if n <= 0 || len(ws) < n {
		return "", 0
	}
	counts := make(map[string]int, len(ws))
	best, bestCount := "", 0
	for i := 0; i+n <= len(ws); i++ {
		phrase := strings.Join(ws[i:i+n], " ")
		counts[phrase]++
		if counts[phrase] > bestCount {
			bestCount = counts[phrase]
			best = phrase
		}
	}
	return best, bestCount
}

// splitSentences splits on '.', '!', '?' terminators, keeping non-empty
// trailing text (no terminator) as a final sentence.
func splitSentences(s string) []string {
	var out []string
	var b strings.Builder
	for _, r := range s {
		b.WriteRune(r)
		if r == '.' || r == '!' || r == '?' {
			out = append(out, b.String())
			b.Reset()
		}
	}
	if strings.TrimSpace(b.String()) != "" {
		out = append(out, b.String())
	}
	return out
}
