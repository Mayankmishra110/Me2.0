package compliance

import (
	"context"
	"fmt"
	"strings"
)

// G5 Metadata honesty: title/thumb promise must be delivered by the script.
type G5 struct {
	Classifier Classifier // required for LLM judge; when nil, heuristic pass
}

func (g G5) ID() string { return "G5" }

func (g G5) Check(ctx context.Context, item ContentItem) GateResult {
	title := strings.TrimSpace(item.Title)
	thumb := strings.TrimSpace(item.ThumbnailText)
	if title == "" && thumb == "" {
		return GateResult{ID: "G5", Passed: true, Detail: "no title/thumb to judge"}
	}
	if g.Classifier == nil {
		// Heuristic: every significant title token should appear in script (soft).
		script := NormalizeForShingle(item.ScriptText)
		missing := 0
		for _, tok := range words(NormalizeForShingle(title + " " + thumb)) {
			if len(tok) < 4 {
				continue
			}
			if !strings.Contains(script, tok) {
				missing++
			}
		}
		if missing > 3 {
			return GateResult{
				ID:     "G5",
				Passed: false,
				Detail: fmt.Sprintf("heuristic: %d title/thumb tokens absent from script", missing),
			}
		}
		return GateResult{ID: "G5", Passed: true, Detail: "yes (heuristic)"}
	}
	pass, detail, err := g.Classifier.Classify(ctx,
		`Judge metadata honesty. Does the title/thumbnail text promise something the script delivers? Reply JSON {"pass":true|false,"detail":"yes|no: ..."}. pass=true only when the answer is yes.`,
		fmt.Sprintf("Title: %s\nThumbnail text: %s\nScript:\n%s", title, thumb, item.ScriptText),
	)
	if err != nil {
		return GateResult{ID: "G5", Passed: false, Detail: fmt.Sprintf("judge error: %v", err)}
	}
	if !pass {
		if detail == "" {
			detail = "no"
		}
		return GateResult{ID: "G5", Passed: false, Detail: detail}
	}
	if detail == "" {
		detail = "yes"
	}
	return GateResult{ID: "G5", Passed: true, Detail: detail}
}
