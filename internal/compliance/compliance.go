// Package compliance implements the Compliance & Originality Engine script gates
// (COMPLIANCE §3 G1–G7, report shape §5). Final gates (F*) live in final.go (M2-210).
package compliance

import (
	"context"
	"encoding/json"
	"time"
)

// Gate is one compliance check (SPEC §3).
type Gate interface {
	ID() string
	Check(ctx context.Context, item ContentItem) GateResult
}

// GateResult is one gate outcome in the COMPLIANCE §5 report.
type GateResult struct {
	ID     string   `json:"id"`
	Passed bool     `json:"passed"`
	Score  *float64 `json:"score,omitempty"`
	Detail string   `json:"detail,omitempty"`
}

// Disclosures records disclosure flags on the report (filled by final gates later).
type Disclosures struct {
	Synthetic         bool `json:"synthetic"`
	Affiliate         bool `json:"affiliate"`
	FinanceDisclaimer bool `json:"finance_disclaimer"`
}

// Report is content_items.compliance JSON (COMPLIANCE §5).
type Report struct {
	Passed      bool         `json:"passed"`
	Gates       []GateResult `json:"gates"`
	Disclosures Disclosures  `json:"disclosures"`
	CheckedAt   time.Time    `json:"checked_at"`
}

// Thresholds are config-driven gate limits (COMPLIANCE §3 defaults).
type Thresholds struct {
	G1MaxOverlap      float64 `yaml:"g1_max_overlap" json:"g1_max_overlap"`             // < 0.05
	G1MaxSharedRun    int     `yaml:"g1_max_shared_run" json:"g1_max_shared_run"`       // ≤ 12 words
	G1ShingleSize     int     `yaml:"g1_shingle_size" json:"g1_shingle_size"`           // 8
	G2MaxCosine       float64 `yaml:"g2_max_cosine" json:"g2_max_cosine"`               // < 0.90
	G2MaxTitleJaccard float64 `yaml:"g2_max_title_jaccard" json:"g2_max_title_jaccard"` // < 0.6
	G2PriorScripts    int     `yaml:"g2_prior_scripts" json:"g2_prior_scripts"`         // 200
	G2PriorTitles     int     `yaml:"g2_prior_titles" json:"g2_prior_titles"`           // 30
	MaxRewrites       int     `yaml:"max_rewrites" json:"max_rewrites"`                 // 2
}

// DefaultThresholds returns COMPLIANCE §3 defaults.
func DefaultThresholds() Thresholds {
	return Thresholds{
		G1MaxOverlap:      0.05,
		G1MaxSharedRun:    12,
		G1ShingleSize:     8,
		G2MaxCosine:       0.90,
		G2MaxTitleJaccard: 0.6,
		G2PriorScripts:    200,
		G2PriorTitles:     30,
		MaxRewrites:       2,
	}
}

// ContentItem is the gate input (script stage).
type ContentItem struct {
	ID              string
	ChannelID       string
	Kind            string // long | short | blog | post
	Format          string
	HookStyle       string
	Language        string // en | hi
	Title           string
	ThumbnailText   string
	ScriptText      string   // spoken text used for G1/G2/G4/G7
	SourceTexts     []string // fetched source bodies for G1
	Brief           Brief
	Recent          []HistoryEntry   // most recent first (G6)
	PriorTitles     []string         // channel titles for G2 Jaccard
	PriorEmbeddings []PriorEmbedding // channel script embeddings for G2 cosine, tagged by embed model
}

// PriorEmbedding is one stored script_fingerprints embedding plus the model
// that produced it. Vectors from different embedding models are not
// comparable (different dimensionality/semantics), so G2 only computes
// cosine similarity between vectors sharing the same Model — see
// CONTEXT.md D25/M2-124. A mismatched-model prior is skipped, not compared.
type PriorEmbedding struct {
	Vector []float64
	Model  string
}

// Brief holds facts/numbers needed by G3 (mirrors content.Brief fields used here).
type Brief struct {
	Facts      []Fact
	KeyNumbers []KeyNumber
}

// Fact is one sourced claim.
type Fact struct {
	Claim   string
	Sources []string
}

// KeyNumber is a numeric highlight tied to a source.
type KeyNumber struct {
	Label  string
	Value  string
	Source string
}

// HistoryEntry is recent format/hook for G6.
type HistoryEntry struct {
	Format    string
	HookStyle string
}

// MarshalReport encodes a Report as JSON for content_items.compliance.
func MarshalReport(r Report) (json.RawMessage, error) {
	return json.Marshal(r)
}

// ParseReport decodes content_items.compliance JSON.
func ParseReport(raw []byte) (Report, error) {
	var r Report
	if err := json.Unmarshal(raw, &r); err != nil {
		return Report{}, err
	}
	return r, nil
}

func scorePtr(v float64) *float64 { return &v }
