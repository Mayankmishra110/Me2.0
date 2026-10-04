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
	embed := staticEmbedder{v: vec, model: "gemini-embedding-001"}
	item := ContentItem{
		ScriptText:      "A unique looking script that is actually a near duplicate of a prior one on this channel.",
		Title:           "Totally different title words here",
		PriorEmbeddings: []PriorEmbedding{{Vector: vec, Model: "gemini-embedding-001"}}, // identical → cosine 1.0
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
	embed := staticEmbedder{v: []float64{1, 0, 0}, model: "gemini-embedding-001"}
	item := ContentItem{
		ScriptText:      "Fresh angle on side hustles.",
		Title:           "Side hustles that pay in 2024",
		PriorEmbeddings: []PriorEmbedding{{Vector: []float64{0, 1, 0}, Model: "gemini-embedding-001"}},
		PriorTitles:     []string{"Cooking pasta at home"},
	}
	r := (G2{Th: DefaultThresholds(), Embed: embed}).Check(context.Background(), item)
	if !r.Passed {
		t.Fatalf("G2 should pass on distinct embedding; got %+v", r)
	}
}

func TestG2_priorsWithoutEmbedFailsClosed(t *testing.T) {
	item := ContentItem{
		ScriptText:      "Any script text that would otherwise look fine without an originality check.",
		Title:           "Unique title about freelancing tips",
		PriorEmbeddings: []PriorEmbedding{{Vector: []float64{1, 0, 0}, Model: "gemini-embedding-001"}},
		PriorTitles:     []string{"Cooking pasta at home"},
	}
	r := (G2{Th: DefaultThresholds(), Embed: nil}).Check(context.Background(), item)
	if r.Passed {
		t.Fatalf("G2 must fail closed when priors exist but Embed is nil; got %+v", r)
	}
	if !strings.Contains(r.Detail, "embedding required") {
		t.Fatalf("detail should say embedding required; got %q", r.Detail)
	}
}

func TestG2_emptyEmbedAgainstPriorsFailsClosed(t *testing.T) {
	item := ContentItem{
		ScriptText:      "Script with an embedder that returns an empty vector.",
		Title:           "Unique title words here",
		PriorEmbeddings: []PriorEmbedding{{Vector: []float64{0, 1, 0}, Model: "gemini-embedding-001"}},
	}
	r := (G2{Th: DefaultThresholds(), Embed: staticEmbedder{v: nil, model: "gemini-embedding-001"}}).Check(context.Background(), item)
	if r.Passed {
		t.Fatalf("G2 must fail closed on empty embedding with priors; got %+v", r)
	}
	if !strings.Contains(strings.ToLower(r.Detail), "embed") {
		t.Fatalf("detail should mention embed failure; got %q", r.Detail)
	}
}

// TestG2_crossModelPriorsSkippedNotCompared proves a prior embedding stored
// under a different model (e.g. leftover Ollama nomic-embed-text vectors
// after switching to Gemini, CONTEXT D25 / M2-124) is never fed into cosine
// similarity, and does not trip the "fail closed" path either: it is simply
// not comparable, so G2 passes when the only priors are a different model.
func TestG2_crossModelPriorsSkippedNotCompared(t *testing.T) {
	vec := []float64{1, 0, 0, 0}
	embed := staticEmbedder{v: vec, model: "gemini-embedding-001"}
	item := ContentItem{
		ScriptText: "A script whose only channel history was embedded with a retired model.",
		Title:      "Totally new framing",
		// Identical vector, but tagged with the OLD model — must be skipped,
		// not compared (an identical-looking vector would otherwise trip a
		// false "near duplicate").
		PriorEmbeddings: []PriorEmbedding{{Vector: vec, Model: "nomic-embed-text"}},
	}
	r := (G2{Th: DefaultThresholds(), Embed: embed}).Check(context.Background(), item)
	if !r.Passed {
		t.Fatalf("G2 should pass when the only priors are from a different embed model; got %+v", r)
	}
	if !strings.Contains(r.Detail, "skipped") {
		t.Fatalf("detail should note the skipped cross-model prior; got %q", r.Detail)
	}
}

func TestG2_noPriorsNoEmbedPasses(t *testing.T) {
	item := ContentItem{
		ScriptText: "Fresh script with no channel history to compare.",
		Title:      "Brand new channel first video",
	}
	r := (G2{Th: DefaultThresholds(), Embed: nil}).Check(context.Background(), item)
	if !r.Passed {
		t.Fatalf("G2 may skip cosine with no priors and no embedder; got %+v", r)
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

type staticEmbedder struct {
	v     []float64
	model string
}

func (s staticEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, string, error) {
	out := make([][]float64, len(texts))
	for i := range texts {
		cp := append([]float64(nil), s.v...)
		out[i] = cp
	}
	model := s.model
	if model == "" {
		model = "static-test-model"
	}
	return out, model, nil
}
