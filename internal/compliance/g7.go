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

// G7 Language quality: Hindi native phrasing / English readability.
type G7 struct {
	Classifier Classifier // optional LLM judge
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

	return GateResult{ID: "G7", Passed: true, Detail: "pass"}
}
