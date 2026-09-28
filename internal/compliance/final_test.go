package compliance

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func passItem(t *testing.T) FinalItem {
	t.Helper()
	dir := t.TempDir()
	clip := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(clip, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	sidecar := clip + ".license.json"
	if err := os.WriteFile(sidecar, []byte(`{"license_url":"https://example.com/l"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	srt := filepath.Join(dir, "subs.srt")
	if err := os.WriteFile(srt, []byte("1\n00:00:00,000 --> 00:00:01,000\nHi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return FinalItem{
		ID:        "c1",
		ChannelID: "yt-money-en",
		Kind:      "short",
		Language:  "en",
		Assets: []AssetRef{
			{Kind: "clip", Path: clip, LicenseURL: ""},
			{Kind: "render", Path: filepath.Join(dir, "short.mp4")},
			{Kind: "subs", Path: srt},
		},
		Technical: TechnicalInfo{
			DurationSec:      38,
			LoudnessLUFS:     -14,
			LoudnessSet:      true,
			Width:            1080,
			Height:           1920,
			MaxBlackFrameSec: 0.2,
			CaptionsBurned:   true,
		},
		Disclosures: FinalDisclosures{
			SyntheticDecided:  true,
			Synthetic:         true,
			HasAffiliateLinks: true,
			AffiliatePresent:  true,
			FinanceChannel:    true,
			FinanceDisclaimer: true,
		},
		Destinations: []Destination{
			{Platform: "youtube", Account: "yt-money-en"},
			{Platform: "instagram", Account: "ig-en"},
		},
		Caps:         Caps{ShortsPerDay: 2, LongPerWeek: 2},
		ShortsToday:  0,
		LongThisWeek: 0,
		Quotas: []QuotaUsage{
			{Provider: "youtube", Used: 100, Limit: 10000},
			{Provider: "meta", Used: 1, Limit: 100},
		},
		Title:  "AI can't read your screen… right?",
		Format: "myth_vs_fact",
	}
}

func TestRunFinalGates_allPass(t *testing.T) {
	r := RunFinalGates(context.Background(), passItem(t))
	if !r.Passed {
		t.Fatalf("expected pass, got %+v", r)
	}
	if len(r.Gates) != 6 {
		t.Fatalf("want 6 gates, got %d", len(r.Gates))
	}
	for _, g := range r.Gates {
		if !g.Passed {
			t.Fatalf("gate %s failed: %s", g.ID, g.Detail)
		}
	}
	if !r.Disclosures.Affiliate || !r.Disclosures.FinanceDisclaimer {
		t.Fatalf("disclosures not filled: %+v", r.Disclosures)
	}
}

func TestF1_unlicensedClipFails(t *testing.T) {
	item := passItem(t)
	item.Assets = []AssetRef{{Kind: "clip", Path: filepath.Join(t.TempDir(), "bare.mp4")}}
	r := (F1{}).check(item)
	if r.Passed {
		t.Fatalf("F1 should fail: %+v", r)
	}
}

func TestF2_shortNeedsBurnedCaptions(t *testing.T) {
	item := passItem(t)
	item.Technical.CaptionsBurned = false
	r := (F2{}).check(item)
	if r.Passed {
		t.Fatalf("F2 should fail: %+v", r)
	}
}

func TestF2_longNeedsSRT(t *testing.T) {
	item := passItem(t)
	item.Kind = "long"
	item.Assets = []AssetRef{{Kind: "clip", Path: item.Assets[0].Path, LicenseURL: "https://x"}}
	r := (F2{}).check(item)
	if r.Passed {
		t.Fatalf("F2 long without srt should fail: %+v", r)
	}
}

func TestF3_financeDisclaimerRequired(t *testing.T) {
	item := passItem(t)
	item.Disclosures.FinanceDisclaimer = false
	r := (F3{}).check(item)
	if r.Passed || !strings.Contains(r.Detail, "finance") {
		t.Fatalf("F3 should fail finance: %+v", r)
	}
}

func TestF4_loudnessAndBlackFrames(t *testing.T) {
	item := passItem(t)
	item.Technical.LoudnessLUFS = -8
	r := (F4{}).check(item)
	if r.Passed {
		t.Fatalf("F4 loudness should fail: %+v", r)
	}
	item = passItem(t)
	item.Technical.MaxBlackFrameSec = 1.5
	r = (F4{}).check(item)
	if r.Passed {
		t.Fatalf("F4 black frames should fail: %+v", r)
	}
}

func TestF5_capAndXDuplicate(t *testing.T) {
	item := passItem(t)
	item.ShortsToday = 2
	item.Caps.ShortsPerDay = 2
	r := (F5{}).check(item)
	if r.Passed {
		t.Fatalf("F5 cap should fail: %+v", r)
	}
	item = passItem(t)
	item.Destinations = []Destination{
		{Platform: "x", Account: "x-business"},
		{Platform: "x_personal", Account: "x-personal"},
	}
	r = (F5{}).check(item)
	if r.Passed || !strings.Contains(r.Detail, "X business") {
		t.Fatalf("F5 X duplicate should fail: %+v", r)
	}
}

func TestF6_quotaExhausted(t *testing.T) {
	item := passItem(t)
	item.Quotas = []QuotaUsage{{Provider: "youtube", Used: 100, Limit: 100}}
	r := (F6{}).check(item)
	if r.Passed {
		t.Fatalf("F6 should fail: %+v", r)
	}
}

type stubApprovals struct {
	id  string
	err error
	got struct {
		contentID, kind, summary, preview string
	}
}

func (s *stubApprovals) Start(ctx context.Context, contentID, kind, summary, previewPath string) (string, error) {
	s.got.contentID = contentID
	s.got.kind = kind
	s.got.summary = summary
	s.got.preview = previewPath
	if s.err != nil {
		return "", s.err
	}
	if s.id == "" {
		s.id = "appr1"
	}
	return s.id, nil
}

type memReports struct {
	last Report
	id   string
}

func (m *memReports) SaveCompliance(ctx context.Context, contentID string, report Report) error {
	m.id = contentID
	m.last = report
	return nil
}

func TestFinalEngine_HandlePassStartsApproval(t *testing.T) {
	ap := &stubApprovals{id: "A1"}
	rep := &memReports{}
	e := NewFinalEngine(rep, ap)
	item := passItem(t)
	item.PreviewPath = "/data/preview.mp4"
	out, err := e.Handle(context.Background(), FinalPayload{ContentID: "c1", Item: item})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if out.ApprovalID != "A1" || !out.Report.Passed {
		t.Fatalf("outcome %+v", out)
	}
	if ap.got.contentID != "c1" || ap.got.preview != "/data/preview.mp4" {
		t.Fatalf("starter got %+v", ap.got)
	}
	if rep.id != "c1" || !rep.last.Passed {
		t.Fatalf("report save %+v id=%s", rep.last, rep.id)
	}
}

func TestFinalEngine_HandleFailBlocksApproval(t *testing.T) {
	ap := &stubApprovals{id: "A1"}
	e := NewFinalEngine(&memReports{}, ap)
	item := passItem(t)
	item.Technical.CaptionsBurned = false
	out, err := e.Handle(context.Background(), FinalPayload{Item: item})
	if err == nil {
		t.Fatal("expected permanent failure")
	}
	if out.Report.Passed {
		t.Fatal("report should fail")
	}
	if ap.got.contentID != "" {
		t.Fatal("must not start approval on failure")
	}
	if !strings.Contains(err.Error(), "F2") {
		t.Fatalf("error should name failing gate: %v", err)
	}
}
