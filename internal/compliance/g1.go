package compliance

import (
	"context"
	"fmt"
	"strings"
	"unicode"
)

// G1 Source originality: 8-word shingle overlap vs source texts.
type G1 struct {
	Th Thresholds
}

func (g G1) ID() string { return "G1" }

func (g G1) Check(ctx context.Context, item ContentItem) GateResult {
	_ = ctx
	th := g.Th
	if th.G1ShingleSize <= 0 {
		th.G1ShingleSize = 8
	}
	if th.G1MaxSharedRun <= 0 {
		th.G1MaxSharedRun = 12
	}
	if th.G1MaxOverlap <= 0 {
		th.G1MaxOverlap = 0.05
	}

	script := NormalizeForShingle(item.ScriptText)
	if script == "" {
		return GateResult{ID: "G1", Passed: false, Detail: "empty script text"}
	}
	if len(item.SourceTexts) == 0 {
		return GateResult{ID: "G1", Passed: true, Score: scorePtr(0), Detail: "no source texts to compare"}
	}

	scriptWords := words(script)
	scriptShingles := shingleSet(scriptWords, th.G1ShingleSize)

	maxOverlap := 0.0
	maxRun := 0
	for _, src := range item.SourceTexts {
		srcNorm := NormalizeForShingle(src)
		if srcNorm == "" {
			continue
		}
		srcWords := words(srcNorm)
		srcShingles := shingleSet(srcWords, th.G1ShingleSize)
		overlap := shingleOverlap(scriptShingles, srcShingles)
		run := longestSharedRun(scriptWords, srcWords)
		if overlap > maxOverlap {
			maxOverlap = overlap
		}
		if run > maxRun {
			maxRun = run
		}
	}

	passed := maxOverlap < th.G1MaxOverlap && maxRun <= th.G1MaxSharedRun
	detail := fmt.Sprintf("overlap=%.4f max shared run %d words", maxOverlap, maxRun)
	if !passed {
		if maxOverlap >= th.G1MaxOverlap {
			detail = fmt.Sprintf("shingle overlap %.4f >= %.2f; max shared run %d words", maxOverlap, th.G1MaxOverlap, maxRun)
		} else {
			detail = fmt.Sprintf("max shared run %d words > %d; overlap=%.4f", maxRun, th.G1MaxSharedRun, maxOverlap)
		}
	}
	return GateResult{ID: "G1", Passed: passed, Score: scorePtr(maxOverlap), Detail: detail}
}

// NormalizeForShingle lowercases and collapses whitespace.
func NormalizeForShingle(s string) string {
	var b strings.Builder
	prevSpace := true
	for _, r := range strings.ToLower(s) {
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return strings.TrimSpace(b.String())
}

func words(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Fields(s)
}

func shingleSet(ws []string, n int) map[string]struct{} {
	out := make(map[string]struct{})
	if n <= 0 || len(ws) < n {
		return out
	}
	for i := 0; i+n <= len(ws); i++ {
		out[strings.Join(ws[i:i+n], " ")] = struct{}{}
	}
	return out
}

// shingleOverlap is |A∩B| / |A| (script shingles in the denominator).
func shingleOverlap(script, src map[string]struct{}) float64 {
	if len(script) == 0 {
		return 0
	}
	shared := 0
	for s := range script {
		if _, ok := src[s]; ok {
			shared++
		}
	}
	return float64(shared) / float64(len(script))
}

// longestSharedRun returns the longest contiguous word run shared by a and b.
func longestSharedRun(a, b []string) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	// DP on word equality; keep only previous row for memory.
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	best := 0
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				cur[j] = prev[j-1] + 1
				if cur[j] > best {
					best = cur[j]
				}
			} else {
				cur[j] = 0
			}
		}
		prev, cur = cur, prev
		for k := range cur {
			cur[k] = 0
		}
	}
	return best
}
