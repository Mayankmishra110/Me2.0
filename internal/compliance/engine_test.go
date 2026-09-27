package compliance

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"mayank2/internal/queue"
)

func TestReportShape_COMPLIANCE_S5(t *testing.T) {
	e := NewEngine(DefaultThresholds(), nil, nil)
	item := ContentItem{
		ID:         "c1",
		ChannelID:  "ch1",
		Language:   "en",
		Format:     "listicle",
		HookStyle:  "question",
		Title:      "Five freelancing tips",
		ScriptText: "Here is educational content about freelancing with no forbidden claims at all in this whole script body text.",
		Brief: Brief{
			Facts: []Fact{{Claim: "Freelancers bill hourly", Sources: []string{"https://example.com"}}},
		},
		SourceTexts: []string{"Unrelated source about astronomy and distant galaxies far away."},
	}
	rep := e.RunGates(context.Background(), item)
	raw, err := MarshalReport(rep)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"passed", "gates", "disclosures", "checked_at"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("report missing %q: %s", key, raw)
		}
	}
	gates, _ := m["gates"].([]any)
	if len(gates) != 7 {
		t.Fatalf("want 7 gates, got %d: %s", len(gates), raw)
	}
}

func TestHandle_scriptWriteCannotOverwriteComplianceKeys(t *testing.T) {
	th := DefaultThresholds()
	th.MaxRewrites = 2
	eq := &fakeEnqueuer{}
	e := NewEngine(th, nil, nil)
	e.Enqueue = eq
	e.Reports = &memSaver{}
	item := ContentItem{
		ID:         "c-sw",
		ChannelID:  "ch1",
		Language:   "en",
		ScriptText: "Investors get guaranteed returns with this plan.",
		Brief:      Brief{Facts: []Fact{{Claim: "x", Sources: []string{"https://x.test"}}}},
	}
	// Hostile script_write tries to reset rewrite_attempt and wipe feedback.
	sw, err := json.Marshal(map[string]any{
		"topic":             "side hustles",
		"rewrite_attempt":   0,
		"gate_feedback":     "",
		"compliance_report": map[string]any{"passed": true},
		"extra_field":       "keep-me",
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := e.Handle(context.Background(), ScriptPayload{
		ContentID:      "c-sw",
		ChannelID:      "ch1",
		Item:           item,
		RewriteAttempt: 0,
		ScriptWrite:    sw,
	})
	if err != nil {
		t.Fatalf("want rewrite enqueue, got err=%v", err)
	}
	if !out.Rewritten {
		t.Fatalf("want rewritten; got %+v", out)
	}
	m, ok := eq.lastPayload.(map[string]any)
	if !ok {
		t.Fatalf("payload type %T", eq.lastPayload)
	}
	if m["rewrite_attempt"] != 1 {
		t.Fatalf("rewrite_attempt must stay compliance-owned=1, got %#v", m["rewrite_attempt"])
	}
	fb, _ := m["gate_feedback"].(string)
	if fb == "" || !strings.Contains(fb, "G4") {
		t.Fatalf("gate_feedback must be set by compliance, got %#v", m["gate_feedback"])
	}
	if _, ok := m["compliance_report"].(Report); !ok {
		t.Fatalf("compliance_report must be Report from gates, got %T", m["compliance_report"])
	}
	if m["extra_field"] != "keep-me" {
		t.Fatalf("non-compliance ScriptWrite keys should forward; got %#v", m["extra_field"])
	}
	if m["topic"] != "side hustles" {
		t.Fatalf("topic should forward from ScriptWrite; got %#v", m["topic"])
	}
	if m["content_id"] != "c-sw" || m["channel_id"] != "ch1" {
		t.Fatalf("ids: content_id=%#v channel_id=%#v", m["content_id"], m["channel_id"])
	}
}

func TestHandle_rewriteThenDeadLetter(t *testing.T) {
	th := DefaultThresholds()
	th.MaxRewrites = 2
	eq := &fakeEnqueuer{}
	saver := &memSaver{}
	e := NewEngine(th, nil, nil)
	e.Enqueue = eq
	e.Reports = saver
	// Force G4 fail
	item := ContentItem{
		ID:         "c-fail",
		ChannelID:  "ch1",
		Language:   "en",
		ScriptText: "Investors get guaranteed returns with this plan.",
		Brief:      Brief{Facts: []Fact{{Claim: "x", Sources: []string{"https://x.test"}}}},
	}

	out, err := e.Handle(context.Background(), ScriptPayload{ContentID: "c-fail", Item: item, RewriteAttempt: 0})
	if err != nil {
		t.Fatalf("first fail should rewrite, not err: %v", err)
	}
	if !out.Rewritten || out.DeadLetter {
		t.Fatalf("want rewritten; got %+v", out)
	}
	if eq.n != 1 || eq.lastType != JobScriptWrite {
		t.Fatalf("expected script.write enqueue, got n=%d type=%q", eq.n, eq.lastType)
	}
	if saver.last == nil || saver.last.Passed {
		t.Fatalf("report should be saved as failed")
	}

	out, err = e.Handle(context.Background(), ScriptPayload{ContentID: "c-fail", Item: item, RewriteAttempt: 1})
	if err != nil {
		t.Fatalf("second fail should rewrite again: %v", err)
	}
	if !out.Rewritten || eq.n != 2 {
		t.Fatalf("want second rewrite; out=%+v n=%d", out, eq.n)
	}

	out, err = e.Handle(context.Background(), ScriptPayload{ContentID: "c-fail", Item: item, RewriteAttempt: 2})
	if err == nil || !queue.IsPermanent(err) {
		t.Fatalf("want Permanent dead-letter, got err=%v", err)
	}
	if !out.DeadLetter || out.Rewritten {
		t.Fatalf("want dead-letter outcome; got %+v", out)
	}
	if !strings.Contains(err.Error(), "guaranteed") && !strings.Contains(err.Error(), "G4") {
		t.Fatalf("dead-letter error should mention gate: %v", err)
	}
}

func TestG3_unsourcedFails(t *testing.T) {
	r := (G3{}).Check(context.Background(), ContentItem{
		ScriptText: "plain text",
		Brief:      Brief{Facts: []Fact{{Claim: "A claim", Sources: nil}}},
	})
	if r.Passed {
		t.Fatalf("G3 should fail unsourced fact: %+v", r)
	}
}

func TestG6_formatStreakFails(t *testing.T) {
	r := (G6{}).Check(context.Background(), ContentItem{
		Format: "listicle",
		Recent: []HistoryEntry{{Format: "listicle"}, {Format: "listicle"}},
	})
	if r.Passed {
		t.Fatalf("G6 should fail format streak: %+v", r)
	}
}

func TestEncodeDecodeEmbedding(t *testing.T) {
	v := []float64{0.1, -0.5, 2}
	b := EncodeEmbedding(v)
	got, err := DecodeEmbedding(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d", len(got))
	}
}

type fakeEnqueuer struct {
	n           int
	lastType    string
	lastPayload any
}

func (f *fakeEnqueuer) Enqueue(ctx context.Context, jobType string, payload any, opts ...queue.EnqueueOpt) (string, error) {
	f.n++
	f.lastType = jobType
	f.lastPayload = payload
	return "job-1", nil
}

type memSaver struct {
	last *Report
}

func (m *memSaver) SaveCompliance(ctx context.Context, contentID string, report Report) error {
	cp := report
	m.last = &cp
	return nil
}
