package compliance

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// G3 Facts sourced: every brief fact has ≥1 URL; script numbers appear in brief.
type G3 struct{}

func (g G3) ID() string { return "G3" }

var numberRe = regexp.MustCompile(`\d[\d,]*(?:\.\d+)?%?`)

func (g G3) Check(ctx context.Context, item ContentItem) GateResult {
	_ = ctx
	unsourced := 0
	for i, f := range item.Brief.Facts {
		if strings.TrimSpace(f.Claim) == "" {
			continue
		}
		ok := false
		for _, s := range f.Sources {
			if strings.TrimSpace(s) != "" {
				ok = true
				break
			}
		}
		if !ok {
			unsourced++
			_ = i
		}
	}
	if unsourced > 0 {
		return GateResult{
			ID:     "G3",
			Passed: false,
			Score:  scorePtr(0),
			Detail: fmt.Sprintf("%d fact(s) missing source URL", unsourced),
		}
	}

	// Script numbers must appear in brief (facts + key numbers text).
	briefBlob := briefText(item.Brief)
	scriptNums := numberRe.FindAllString(item.ScriptText, -1)
	missing := 0
	var examples []string
	seen := map[string]bool{}
	for _, n := range scriptNums {
		key := normalizeNumber(n)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		if !strings.Contains(briefBlob, key) && !numberInBrief(n, item.Brief) {
			missing++
			if len(examples) < 3 {
				examples = append(examples, n)
			}
		}
	}
	if missing > 0 {
		return GateResult{
			ID:     "G3",
			Passed: false,
			Score:  scorePtr(0),
			Detail: fmt.Sprintf("%d script number(s) not in brief (e.g. %s)", missing, strings.Join(examples, ", ")),
		}
	}
	return GateResult{ID: "G3", Passed: true, Score: scorePtr(1), Detail: "all facts sourced; script numbers in brief"}
}

func briefText(b Brief) string {
	var parts []string
	for _, f := range b.Facts {
		parts = append(parts, NormalizeForShingle(f.Claim))
	}
	for _, k := range b.KeyNumbers {
		parts = append(parts, NormalizeForShingle(k.Label+" "+k.Value))
	}
	return strings.Join(parts, " ")
}

func numberInBrief(n string, b Brief) bool {
	key := normalizeNumber(n)
	for _, k := range b.KeyNumbers {
		if normalizeNumber(k.Value) == key || strings.Contains(NormalizeForShingle(k.Value), key) {
			return true
		}
	}
	for _, f := range b.Facts {
		if strings.Contains(NormalizeForShingle(f.Claim), key) {
			return true
		}
	}
	return false
}

func normalizeNumber(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", "")
	return strings.ToLower(s)
}
