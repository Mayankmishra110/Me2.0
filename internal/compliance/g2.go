package compliance

import (
	"context"
	"fmt"
	"math"
	"strings"
)

// Embedder produces vectors for script originality (Ollama nomic-embed-text).
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float64, error)
}

// G2 Self-originality: embedding cosine vs prior scripts + title Jaccard.
type G2 struct {
	Th    Thresholds
	Embed Embedder // optional only when no PriorEmbeddings; with priors, missing embed fails closed
}

func (g G2) ID() string { return "G2" }

func (g G2) Check(ctx context.Context, item ContentItem) GateResult {
	th := g.Th
	if th.G2MaxCosine <= 0 {
		th.G2MaxCosine = 0.90
	}
	if th.G2MaxTitleJaccard <= 0 {
		th.G2MaxTitleJaccard = 0.6
	}

	maxCos := 0.0
	cosDetail := "no prior embeddings"

	vec, err := g.scriptVector(ctx, item)
	if err != nil {
		return GateResult{ID: "G2", Passed: false, Detail: fmt.Sprintf("embed: %v", err)}
	}
	if len(item.PriorEmbeddings) > 0 {
		// Fail closed: originality vs priors requires a current embedding.
		if len(vec) == 0 {
			return GateResult{
				ID:     "G2",
				Passed: false,
				Detail: "embedding required to compare against prior scripts",
			}
		}
		for _, prior := range item.PriorEmbeddings {
			c := cosine(vec, prior)
			if c > maxCos {
				maxCos = c
			}
		}
		cosDetail = fmt.Sprintf("max cosine=%.4f", maxCos)
	} else if len(vec) == 0 {
		cosDetail = "no prior embeddings; cosine skipped"
	}

	titleJac := 0.0
	title := strings.TrimSpace(item.Title)
	if title != "" && len(item.PriorTitles) > 0 {
		for _, t := range item.PriorTitles {
			j := jaccardTokens(title, t)
			if j > titleJac {
				titleJac = j
			}
		}
	}

	passed := maxCos < th.G2MaxCosine && titleJac < th.G2MaxTitleJaccard
	detail := fmt.Sprintf("%s; title Jaccard=%.4f", cosDetail, titleJac)
	if !passed {
		if maxCos >= th.G2MaxCosine {
			detail = fmt.Sprintf("near-duplicate script cosine %.4f >= %.2f; title Jaccard=%.4f", maxCos, th.G2MaxCosine, titleJac)
		} else {
			detail = fmt.Sprintf("title Jaccard %.4f >= %.2f; %s", titleJac, th.G2MaxTitleJaccard, cosDetail)
		}
	}
	return GateResult{ID: "G2", Passed: passed, Score: scorePtr(maxCos), Detail: detail}
}

func (g G2) scriptVector(ctx context.Context, item ContentItem) ([]float64, error) {
	if g.Embed == nil {
		if len(item.PriorEmbeddings) > 0 {
			return nil, fmt.Errorf("embedding required to compare against prior scripts")
		}
		return nil, nil
	}
	text := strings.TrimSpace(item.ScriptText)
	if text == "" {
		return nil, fmt.Errorf("empty script text")
	}
	vecs, err := g.Embed.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 || len(vecs[0]) == 0 {
		return nil, fmt.Errorf("empty embedding")
	}
	return vecs[0], nil
}

func cosine(a, b []float64) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := 0; i < n; i++ {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func jaccardTokens(a, b string) float64 {
	sa := tokenSet(a)
	sb := tokenSet(b)
	if len(sa) == 0 && len(sb) == 0 {
		return 0
	}
	inter := 0
	for t := range sa {
		if _, ok := sb[t]; ok {
			inter++
		}
	}
	union := len(sa) + len(sb) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func tokenSet(s string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, w := range words(NormalizeForShingle(s)) {
		if w != "" {
			out[w] = struct{}{}
		}
	}
	return out
}
