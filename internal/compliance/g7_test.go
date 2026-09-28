package compliance

import (
	"context"
	"strings"
	"testing"
)

// These tests directly rebut the audit finding on M2-205: with Classifier
// nil, G7 must NOT unconditionally pass English scripts. Each heuristic
// signal (stutter, filler density, looping phrase, run-on sentence) is
// exercised in isolation so a regression in one doesn't hide behind another.

func TestG7_englishHeuristicPasses(t *testing.T) {
	item := ContentItem{
		Language: "en",
		ScriptText: "Freelancing rates have shifted a lot since last year. " +
			"Many independent contractors now charge by project instead of by the hour. " +
			"This shift rewards clear scoping and strong client communication. " +
			"Newer freelancers should study three or four recent contracts before setting their own prices.",
	}
	r := (G7{}).Check(context.Background(), item)
	if !r.Passed {
		t.Fatalf("G7 heuristic should pass a well-formed English script; got %+v", r)
	}
}

func TestG7_englishHeuristicFailsOnFillerDensity(t *testing.T) {
	item := ContentItem{
		Language: "en",
		ScriptText: "Um, this video is honestly kind of about savings tips, and honestly, " +
			"you know, basically anyone can follow it if they try, honestly.",
	}
	r := (G7{}).Check(context.Background(), item)
	if r.Passed {
		t.Fatalf("G7 must not unconditionally pass this filler-heavy English script; got %+v", r)
	}
	if !strings.Contains(r.Detail, "filler-word density") {
		t.Fatalf("expected filler-word density detail, got %q", r.Detail)
	}
}

func TestG7_englishHeuristicFailsOnStutter(t *testing.T) {
	item := ContentItem{
		Language:   "en",
		ScriptText: "This is the the best strategy for beginners to learn quickly and understand the market fundamentals well.",
	}
	r := (G7{}).Check(context.Background(), item)
	if r.Passed {
		t.Fatalf("G7 must fail on a repeated/stuttered word; got %+v", r)
	}
	if !strings.Contains(r.Detail, "repeated word") {
		t.Fatalf("expected repeated-word detail, got %q", r.Detail)
	}
}

func TestG7_englishHeuristicFailsOnLoopingPhrase(t *testing.T) {
	item := ContentItem{
		Language:   "en",
		ScriptText: "click the link below click the link below click the link below",
	}
	r := (G7{}).Check(context.Background(), item)
	if r.Passed {
		t.Fatalf("G7 must fail on a looping/templated phrase; got %+v", r)
	}
	if !strings.Contains(r.Detail, "repeated") {
		t.Fatalf("expected looping-phrase detail, got %q", r.Detail)
	}
}

func TestG7_englishHeuristicFailsOnRunOnSentence(t *testing.T) {
	words := []string{
		"understanding", "how", "compound", "interest", "actually", "multiplies", "wealth", "over",
		"multiple", "decades", "requires", "looking", "closely", "at", "contribution", "timing",
		"account", "fees", "tax", "treatment", "inflation", "adjusted", "returns", "diversification",
		"strategy", "risk", "tolerance", "changes", "with", "age", "employer", "matching", "programs",
		"automatic", "rebalancing", "dollar", "cost", "averaging", "discipline", "patience", "avoiding",
		"panic", "selling", "during", "downturns", "builds", "security", "consistent",
	}
	item := ContentItem{
		Language:   "en",
		ScriptText: strings.Join(words, " "), // no punctuation at all: one giant run-on sentence
	}
	if len(words) <= enRunOnMaxWords {
		t.Fatalf("test fixture too short: need > %d words, have %d", enRunOnMaxWords, len(words))
	}
	r := (G7{}).Check(context.Background(), item)
	if r.Passed {
		t.Fatalf("G7 must fail a single run-on sentence with no punctuation; got %+v", r)
	}
	if !strings.Contains(r.Detail, "run-on") {
		t.Fatalf("expected run-on detail, got %q", r.Detail)
	}
}

func TestG7_hindiArtifactStillFails(t *testing.T) {
	item := ContentItem{
		Language:   "hi",
		ScriptText: "Please kindly do the needful and prepone the meeting as soon as possible today.",
	}
	r := (G7{}).Check(context.Background(), item)
	if r.Passed {
		t.Fatalf("G7 should still fail Hindi literal-translation artifacts; got %+v", r)
	}
}

func TestG7_classifierOverridesHeuristicFail(t *testing.T) {
	// Heuristic alone would fail this (filler-heavy) script, but a present
	// classifier is authoritative and says pass — proving the LLM path is
	// actually invoked, not merely consulted after a heuristic verdict.
	fc := &fakeClassifier{pass: true, detail: "pass"}
	item := ContentItem{
		Language: "en",
		ScriptText: "Um, this video is honestly kind of about savings tips, and honestly, " +
			"you know, basically anyone can follow it if they try, honestly.",
	}
	r := (G7{Classifier: fc}).Check(context.Background(), item)
	if !fc.called {
		t.Fatalf("expected Classifier.Classify to be invoked")
	}
	if !r.Passed {
		t.Fatalf("classifier pass should override heuristic fail; got %+v", r)
	}
}

func TestG7_classifierOverridesHeuristicPass(t *testing.T) {
	// Heuristic alone would pass this clean script, but a present classifier
	// says fail — proving the classifier verdict is authoritative both ways.
	fc := &fakeClassifier{pass: false, detail: "fail: reading level too advanced"}
	item := ContentItem{
		Language: "en",
		ScriptText: "Freelancing rates have shifted a lot since last year. " +
			"Many independent contractors now charge by project instead of by the hour.",
	}
	r := (G7{Classifier: fc}).Check(context.Background(), item)
	if !fc.called {
		t.Fatalf("expected Classifier.Classify to be invoked")
	}
	if r.Passed {
		t.Fatalf("classifier fail should override heuristic pass; got %+v", r)
	}
	if r.Detail != fc.detail {
		t.Fatalf("expected classifier detail %q, got %q", fc.detail, r.Detail)
	}
}
