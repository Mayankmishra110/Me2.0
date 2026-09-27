package compliance

import (
	"context"
	"strings"
	"testing"
)

func TestG1_copiedParagraphFails(t *testing.T) {
	source := strings.Repeat("alpha bravo charlie delta echo foxtrot golf hotel ", 4) +
		"the quick brown fox jumps over the lazy dog today forever amen"
	// Script copies a long contiguous run from the source (>12 words + high shingle overlap).
	copied := "the quick brown fox jumps over the lazy dog today forever amen alpha bravo charlie delta echo foxtrot golf hotel alpha bravo charlie delta echo foxtrot golf hotel"
	item := ContentItem{
		ScriptText:  copied,
		SourceTexts: []string{source},
	}
	r := (G1{Th: DefaultThresholds()}).Check(context.Background(), item)
	if r.Passed {
		t.Fatalf("G1 should fail on copied paragraph; got %+v", r)
	}
	if r.Score == nil || *r.Score < 0.05 {
		t.Fatalf("expected high overlap score, got %+v", r)
	}
}

func TestG1_originalPasses(t *testing.T) {
	item := ContentItem{
		ScriptText:  "Here is an original script about freelancing rates in 2024 with unique phrasing throughout the whole piece.",
		SourceTexts: []string{"Completely unrelated article about cooking pasta with tomatoes and basil in Italy."},
	}
	r := (G1{Th: DefaultThresholds()}).Check(context.Background(), item)
	if !r.Passed {
		t.Fatalf("G1 should pass on original text; got %+v", r)
	}
}

func TestG2_nearDuplicateFails(t *testing.T) {
	vec := []float64{1, 0, 0, 0}
	embed := staticEmbedder{v: vec}
	item := ContentItem{
		ScriptText:      "A unique looking script that is actually a near duplicate of a prior one on this channel.",
		Title:           "Totally different title words here",
		PriorEmbeddings: [][]float64{vec}, // identical → cosine 1.0
	}
	r := (G2{Th: DefaultThresholds(), Embed: embed}).Check(context.Background(), item)
	if r.Passed {
		t.Fatalf("G2 should fail on near-duplicate embedding; got %+v", r)
	}
	if r.Score == nil || *r.Score < 0.90 {
		t.Fatalf("expected cosine >= 0.90, got %+v", r)
	}
}

func TestG2_distinctPasses(t *testing.T) {
	embed := staticEmbedder{v: []float64{1, 0, 0}}
	item := ContentItem{
		ScriptText:      "Fresh angle on side hustles.",
		Title:           "Side hustles that pay in 2024",
		PriorEmbeddings: [][]float64{{0, 1, 0}},
		PriorTitles:     []string{"Cooking pasta at home"},
	}
	r := (G2{Th: DefaultThresholds(), Embed: embed}).Check(context.Background(), item)
	if !r.Passed {
		t.Fatalf("G2 should pass on distinct embedding; got %+v", r)
	}
}

func TestG4_guaranteedReturnsFails(t *testing.T) {
	item := ContentItem{
		ScriptText: "This strategy offers guaranteed returns if you follow the plan carefully every month.",
		Title:      "Easy money tips",
	}
	r := (G4{}).Check(context.Background(), item)
	if r.Passed {
		t.Fatalf("G4 should fail on %q; got %+v", "guaranteed returns", r)
	}
	if !strings.Contains(r.Detail, "guaranteed") {
		t.Fatalf("detail should mention guaranteed phrase: %q", r.Detail)
	}
}

func TestG4_cleanPasses(t *testing.T) {
	item := ContentItem{
		ScriptText: "Past performance is not a promise of future results. This is educational only.",
		Title:      "What the data shows",
	}
	r := (G4{}).Check(context.Background(), item)
	if !r.Passed {
		t.Fatalf("G4 should pass clean script; got %+v", r)
	}
}

type staticEmbedder struct{ v []float64 }

func (s staticEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i := range texts {
		cp := append([]float64(nil), s.v...)
		out[i] = cp
	}
	return out, nil
}
