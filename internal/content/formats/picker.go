package formats

import (
	"fmt"
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

// HistoryEntry is one recently published item for G6 checks (most recent first).
type HistoryEntry struct {
	Format    string
	HookStyle string
}

// PickInput configures the format picker.
type PickInput struct {
	// Allowed is the channel's format list (required).
	Allowed []string
	// Topic drives keyword topic-fit scoring.
	Topic string
	// Kind filters formats that support long and/or short. Empty = no filter.
	Kind Kind
	// FormatScores are past-performance scores (scope=format); missing keys score 0.
	FormatScores map[string]float64
	// Recent is recent history, most recent first (COMPLIANCE G6).
	Recent []HistoryEntry
	// PreferBoth boosts formats that support both long and short when Kind is empty.
	PreferBoth bool
	// ExploreFraction (~0.2 per CONTENT_STRATEGY §6) picks a non-top eligible
	// format when the topic hash says explore. 0 disables; default used when <0.
	ExploreFraction float64
	// Seed overrides the explore hash (tests). Empty → hash Topic+Allowed.
	Seed string
}

// Choice is the picker result.
type Choice struct {
	Format    string  `json:"format"`
	HookStyle string  `json:"hook_style"`
	Score     float64 `json:"score"`
	Reason    string  `json:"reason"`
	Explored  bool    `json:"explored,omitempty"`
}

const defaultExplore = 0.20

// Pick selects a format and hook style by topic fit + scores + G6 variety.
func Pick(in PickInput) (Choice, error) {
	if len(in.Allowed) == 0 {
		return Choice{}, fmt.Errorf("formats: Allowed is empty")
	}
	explore := in.ExploreFraction
	if explore < 0 {
		explore = defaultExplore
	}

	blockedFmt := g6BlockedFormats(in.Recent)
	blockedHook := g6BlockedHookStyles(in.Recent)

	type cand struct {
		id    string
		score float64
		fit   float64
		perf  float64
	}
	var cands []cand
	for _, id := range in.Allowed {
		info, ok := Lookup(id)
		if !ok {
			continue
		}
		if blockedFmt[id] {
			continue
		}
		if in.Kind == KindLong && !info.Long {
			continue
		}
		if in.Kind == KindShort && !info.Short {
			continue
		}
		fit := topicFit(in.Topic, info)
		perf := 0.0
		if in.FormatScores != nil {
			perf = in.FormatScores[id]
		}
		score := fit*2 + perf
		if in.PreferBoth && info.Long && info.Short {
			score += 0.5
		}
		cands = append(cands, cand{id: id, score: score, fit: fit, perf: perf})
	}
	if len(cands) == 0 {
		// Soft fail: G6 blocked everything — widen by ignoring format streak only
		// if Kind filter left nothing; if still empty, error.
		for _, id := range in.Allowed {
			info, ok := Lookup(id)
			if !ok {
				continue
			}
			if in.Kind == KindLong && !info.Long {
				continue
			}
			if in.Kind == KindShort && !info.Short {
				continue
			}
			fit := topicFit(in.Topic, info)
			perf := 0.0
			if in.FormatScores != nil {
				perf = in.FormatScores[id]
			}
			cands = append(cands, cand{id: id, score: fit*2 + perf, fit: fit, perf: perf})
		}
	}
	if len(cands) == 0 {
		return Choice{}, fmt.Errorf("formats: no eligible format for kind %q among %v", in.Kind, in.Allowed)
	}

	// Sort descending by score (stable by id for ties).
	for i := 0; i < len(cands); i++ {
		for j := i + 1; j < len(cands); j++ {
			if cands[j].score > cands[i].score || (cands[j].score == cands[i].score && cands[j].id < cands[i].id) {
				cands[i], cands[j] = cands[j], cands[i]
			}
		}
	}

	picked := cands[0]
	explored := false
	seed := in.Seed
	if seed == "" {
		seed = in.Topic + "|" + strings.Join(in.Allowed, ",")
	}
	if explore > 0 && len(cands) > 1 && hashUnit(seed) < explore {
		// Pick among non-top (exploration).
		idx := 1 + int(hashUnit(seed+"|explore")*float64(len(cands)-1))
		if idx >= len(cands) {
			idx = len(cands) - 1
		}
		picked = cands[idx]
		explored = true
	}

	hook := pickHookStyle(seed, blockedHook)
	reason := fmt.Sprintf("fit=%.2f perf=%.2f g6_ok", picked.fit, picked.perf)
	if explored {
		reason += " explore"
	}
	return Choice{
		Format:    picked.id,
		HookStyle: hook,
		Score:     math.Round(picked.score*100) / 100,
		Reason:    reason,
		Explored:  explored,
	}, nil
}

func g6BlockedFormats(recent []HistoryEntry) map[string]bool {
	out := map[string]bool{}
	if len(recent) >= 2 && recent[0].Format != "" && recent[0].Format == recent[1].Format {
		out[recent[0].Format] = true
	}
	return out
}

func g6BlockedHookStyles(recent []HistoryEntry) map[string]bool {
	out := map[string]bool{}
	if len(recent) >= 3 &&
		recent[0].HookStyle != "" &&
		recent[0].HookStyle == recent[1].HookStyle &&
		recent[1].HookStyle == recent[2].HookStyle {
		out[recent[0].HookStyle] = true
	}
	return out
}

func pickHookStyle(seed string, blocked map[string]bool) string {
	var opts []string
	for _, h := range HookStyles {
		if !blocked[h] {
			opts = append(opts, h)
		}
	}
	if len(opts) == 0 {
		opts = append([]string{}, HookStyles...)
	}
	i := int(hashUnit(seed+"|hook") * float64(len(opts)))
	if i >= len(opts) {
		i = len(opts) - 1
	}
	return opts[i]
}

func topicFit(topic string, info Info) float64 {
	t := normalize(topic)
	if t == "" {
		return 0
	}
	hits := 0
	for _, kw := range info.BestFor {
		if strings.Contains(t, normalize(kw)) {
			hits++
		}
	}
	if hits == 0 {
		return 0
	}
	return float64(hits) / float64(len(info.BestFor))
}

func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsSpace(r) {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(r)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func hashUnit(s string) float64 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return float64(h.Sum32()%10_000) / 10_000.0
}
