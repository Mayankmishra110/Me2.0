package compliance

import (
	"context"
	"strings"
	"testing"
)

// fakeClassifier is a test double for Classifier that records whether it was
// invoked and returns a fixed verdict, so tests can prove the LLM path is
// actually taken (not just that the heuristic silently passes).
type fakeClassifier struct {
	pass   bool
	detail string
	err    error
	called bool
}

func (f *fakeClassifier) Classify(ctx context.Context, system, user string) (bool, string, error) {
	f.called = true
	return f.pass, f.detail, f.err
}

func TestG5_heuristicNilClassifierPasses(t *testing.T) {
	item := ContentItem{
		Title:         "Five freelancing tips for 2024",
		ThumbnailText: "freelancing tips",
		ScriptText:    "This video covers five freelancing tips that actually work in 2024 for new freelancers everywhere.",
	}
	r := (G5{}).Check(context.Background(), item)
	if !r.Passed {
		t.Fatalf("G5 heuristic should pass when title/thumb tokens appear in script; got %+v", r)
	}
}

func TestG5_heuristicNilClassifierFails(t *testing.T) {
	item := ContentItem{
		Title:         "Five freelancing tips for 2024",
		ThumbnailText: "freelancing tips",
		ScriptText:    "This video is actually about cooking pasta with tomatoes and basil in a warm kitchen.",
	}
	r := (G5{}).Check(context.Background(), item)
	if r.Passed {
		t.Fatalf("G5 heuristic should fail when title/thumb tokens are absent from script; got %+v", r)
	}
	if !strings.Contains(r.Detail, "heuristic") {
		t.Fatalf("detail should mention heuristic; got %q", r.Detail)
	}
}

func TestG5_classifierInvokedWhenPresent(t *testing.T) {
	// Script would pass the nil-classifier heuristic (all title tokens present),
	// but the fake classifier says fail — this proves the LLM path is actually
	// invoked and its verdict is authoritative, not the heuristic's.
	fc := &fakeClassifier{pass: false, detail: "no: thumbnail overstates the content"}
	item := ContentItem{
		Title:         "Five freelancing tips for 2024",
		ThumbnailText: "freelancing tips",
		ScriptText:    "This video covers five freelancing tips that actually work in 2024 for new freelancers everywhere.",
	}
	r := (G5{Classifier: fc}).Check(context.Background(), item)
	if !fc.called {
		t.Fatalf("expected Classifier.Classify to be invoked")
	}
	if r.Passed {
		t.Fatalf("G5 should fail when classifier says fail, even though heuristic would pass; got %+v", r)
	}
	if r.Detail != fc.detail {
		t.Fatalf("expected classifier detail %q, got %q", fc.detail, r.Detail)
	}
}

func TestG5_classifierPassInvoked(t *testing.T) {
	fc := &fakeClassifier{pass: true, detail: "yes"}
	item := ContentItem{
		Title:      "Five freelancing tips",
		ScriptText: "Totally unrelated script text with no matching tokens at all.",
	}
	r := (G5{Classifier: fc}).Check(context.Background(), item)
	if !fc.called {
		t.Fatalf("expected Classifier.Classify to be invoked")
	}
	if !r.Passed {
		t.Fatalf("G5 should pass when classifier says pass, even though heuristic would fail; got %+v", r)
	}
}
