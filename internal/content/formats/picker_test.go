package formats

import "testing"

func TestPick_topicFitAndScores(t *testing.T) {
	choice, err := Pick(PickInput{
		Allowed:         []string{"explained_60s", "top_n", "how_to", "myth_vs_fact"},
		Topic:           "best AI tools list for freelancers",
		FormatScores:    map[string]float64{"how_to": 9, "top_n": 1},
		ExploreFraction: 0, // force top
		PreferBoth:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// "tools"/"list"/"best" → top_n; how_to has high perf but weaker keyword hit.
	// top_n fit keywords: top, best, list, tools… → high fit; how_to weaker on this topic.
	if choice.Format != "top_n" && choice.Format != "how_to" {
		t.Fatalf("unexpected format %q reason=%s", choice.Format, choice.Reason)
	}
	if choice.HookStyle == "" {
		t.Fatal("empty hook style")
	}
}

func TestPick_g6BlocksFormatTwiceInARow(t *testing.T) {
	choice, err := Pick(PickInput{
		Allowed: []string{"top_n", "how_to"},
		Topic:   "step by step how to start freelancing",
		Recent: []HistoryEntry{
			{Format: "how_to", HookStyle: "question"},
			{Format: "how_to", HookStyle: "bold_promise"},
		},
		ExploreFraction: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if choice.Format == "how_to" {
		t.Fatalf("G6 should block how_to after 2 in a row, got %q", choice.Format)
	}
	if choice.Format != "top_n" {
		t.Fatalf("want top_n, got %q", choice.Format)
	}
}

func TestPick_g6BlocksHookStyleThrice(t *testing.T) {
	blocked := "question"
	choice, err := Pick(PickInput{
		Allowed: []string{"top_n"},
		Topic:   "top side hustles",
		Recent: []HistoryEntry{
			{Format: "myth_vs_fact", HookStyle: blocked},
			{Format: "case_study", HookStyle: blocked},
			{Format: "comparison", HookStyle: blocked},
		},
		ExploreFraction: 0,
		Seed:            "hook-g6-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if choice.HookStyle == blocked {
		t.Fatalf("G6 should block hook style %q", blocked)
	}
}

func TestPick_kindShortFiltersTimeline(t *testing.T) {
	choice, err := Pick(PickInput{
		Allowed:         []string{"timeline", "explained_60s", "top_n"},
		Topic:           "what is a context window",
		Kind:            KindShort,
		ExploreFraction: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if choice.Format == "timeline" {
		t.Fatal("timeline is long-only")
	}
}

func TestPick_emptyAllowed(t *testing.T) {
	_, err := Pick(PickInput{})
	if err == nil {
		t.Fatal("expected error")
	}
}
