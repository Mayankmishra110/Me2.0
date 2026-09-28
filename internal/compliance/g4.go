package compliance

import (
	"context"
	"fmt"
	"strings"
)

// Forbidden finance/health/legal claim phrases (G4 keyword list).
var g4Forbidden = []string{
	"guaranteed returns",
	"guaranteed return",
	"sure-shot",
	"sure shot",
	"guaranteed profit",
	"guaranteed profits",
	"risk-free returns",
	"risk free returns",
	"buy this stock",
	"sell this stock",
	"guaranteed",
	"get rich quick",
	"double your money",
	"medical advice",
	"legal advice",
	"diagnose yourself",
	"prescribed dosage",
}

// Classifier optionally runs an LLM classify pass for G4/G5/G7.
type Classifier interface {
	Classify(ctx context.Context, system, user string) (pass bool, detail string, err error)
}

// G4 Claims: finance/health/legal prohibited phrasing.
type G4 struct {
	Classifier Classifier // optional; keyword list always runs
}

func (g G4) ID() string { return "G4" }

func (g G4) Check(ctx context.Context, item ContentItem) GateResult {
	text := NormalizeForShingle(item.ScriptText + " " + item.Title + " " + item.ThumbnailText)
	for _, phrase := range g4Forbidden {
		if strings.Contains(text, phrase) {
			return GateResult{
				ID:     "G4",
				Passed: false,
				Score:  scorePtr(0),
				Detail: fmt.Sprintf("phrase %q in script/title/thumb", phrase),
			}
		}
	}
	// Specific return-promise pattern: "N% returns" / "returns of N%"
	if hit := returnsPromise(text); hit != "" {
		return GateResult{
			ID:     "G4",
			Passed: false,
			Score:  scorePtr(0),
			Detail: fmt.Sprintf("return promise %q", hit),
		}
	}

	if g.Classifier != nil {
		pass, detail, err := g.Classifier.Classify(ctx,
			`You are a compliance classifier for finance/health/legal content. Reply JSON {"pass":true|false,"detail":"..."}. Fail on guaranteed returns, buy/sell calls, medical or legal advice.`,
			"Script:\n"+item.ScriptText+"\nTitle: "+item.Title,
		)
		if err != nil {
			return GateResult{ID: "G4", Passed: false, Detail: fmt.Sprintf("classifier error: %v", err)}
		}
		if !pass {
			return GateResult{ID: "G4", Passed: false, Score: scorePtr(0), Detail: detail}
		}
	}
	return GateResult{ID: "G4", Passed: true, Score: scorePtr(1), Detail: "0 hits"}
}

func returnsPromise(text string) string {
	// "guaranteed 12% returns" already caught by "guaranteed"; catch "12% returns" / "returns of 12%"
	lower := text
	idx := strings.Index(lower, "% returns")
	if idx >= 0 {
		start := idx
		for start > 0 && (lower[start-1] >= '0' && lower[start-1] <= '9' || lower[start-1] == '.' || lower[start-1] == ' ') {
			start--
		}
		end := idx + len("% returns")
		return strings.TrimSpace(lower[start:end])
	}
	if i := strings.Index(lower, "returns of "); i >= 0 {
		frag := lower[i:]
		if len(frag) > 40 {
			frag = frag[:40]
		}
		return strings.TrimSpace(frag)
	}
	return ""
}
