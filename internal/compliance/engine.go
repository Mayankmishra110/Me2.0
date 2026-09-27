package compliance

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"mayank2/internal/queue"
)

const (
	JobScriptCompliance = "compliance.script"
	JobScriptWrite      = "script.write"
)

// ReportSaver persists the COMPLIANCE §5 report on a content item.
type ReportSaver interface {
	SaveCompliance(ctx context.Context, contentID string, report Report) error
}

// SQLReportSaver writes content_items.compliance.
type SQLReportSaver struct {
	DB *sql.DB
}

func (s *SQLReportSaver) SaveCompliance(ctx context.Context, contentID string, report Report) error {
	raw, err := MarshalReport(report)
	if err != nil {
		return fmt.Errorf("compliance: marshal report: %w", err)
	}
	res, err := s.DB.ExecContext(ctx, `
UPDATE content_items SET compliance = ?, stage = CASE
  WHEN ? THEN 'compliance_passed'
  ELSE 'compliance_failed'
END
WHERE id = ?
`, string(raw), report.Passed, contentID)
	if err != nil {
		return fmt.Errorf("compliance: save report %s: %w", contentID, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("compliance: content item %s not found", contentID)
	}
	return nil
}

// Enqueuer enqueues follow-up jobs (rewrite).
type Enqueuer interface {
	Enqueue(ctx context.Context, jobType string, payload any, opts ...queue.EnqueueOpt) (id string, err error)
}

// Engine runs G1–G7 and handles rewrite / dead-letter.
type Engine struct {
	Th      Thresholds
	Gates   []Gate
	Embed   Embedder
	Store   FingerprintStore
	Reports ReportSaver
	Enqueue Enqueuer
	Log     *slog.Logger
}

// NewEngine wires default G1–G7 gates.
func NewEngine(th Thresholds, embed Embedder, classify Classifier) *Engine {
	if th.MaxRewrites <= 0 {
		th.MaxRewrites = 2
	}
	return &Engine{
		Th:    th,
		Embed: embed,
		Gates: []Gate{
			G1{Th: th},
			G2{Th: th, Embed: embed},
			G3{},
			G4{Classifier: classify},
			G5{Classifier: classify},
			G6{},
			G7{Classifier: classify},
		},
		Log: slog.Default(),
	}
}

// RunGates evaluates all gates and returns a §5 report.
func (e *Engine) RunGates(ctx context.Context, item ContentItem) Report {
	gates := e.Gates
	if len(gates) == 0 {
		gates = NewEngine(e.Th, e.Embed, nil).Gates
	}
	results := make([]GateResult, 0, len(gates))
	passed := true
	for _, g := range gates {
		if err := ctx.Err(); err != nil {
			results = append(results, GateResult{ID: g.ID(), Passed: false, Detail: err.Error()})
			passed = false
			continue
		}
		r := g.Check(ctx, item)
		if r.ID == "" {
			r.ID = g.ID()
		}
		results = append(results, r)
		if !r.Passed {
			passed = false
		}
	}
	return Report{
		Passed:    passed,
		Gates:     results,
		CheckedAt: time.Now().UTC(),
	}
}

// ScriptPayload is the compliance.script job body.
type ScriptPayload struct {
	ContentID      string          `json:"content_id"`
	ChannelID      string          `json:"channel_id"`
	RewriteAttempt int             `json:"rewrite_attempt"` // 0 on first check; increments on each rewrite request
	Item           ContentItem     `json:"item"`
	ScriptWrite    json.RawMessage `json:"script_write,omitempty"` // forwarded to script.write on rewrite
}

// Outcome is the result of handling a compliance.script job.
type Outcome struct {
	Report     Report
	Rewritten  bool
	DeadLetter bool
}

// Handle runs gates, saves the report, stores fingerprint on pass, and
// either rewrites (max Th.MaxRewrites) or dead-letters on failure.
func (e *Engine) Handle(ctx context.Context, p ScriptPayload) (Outcome, error) {
	item := p.Item
	if item.ID == "" {
		item.ID = p.ContentID
	}
	if item.ChannelID == "" {
		item.ChannelID = p.ChannelID
	}
	if item.ID == "" {
		return Outcome{}, queue.Permanent(fmt.Errorf("compliance: content_id required"))
	}

	// Load prior fingerprints when store is available and priors not preloaded.
	if e.Store != nil && item.ChannelID != "" {
		if len(item.PriorEmbeddings) == 0 {
			emb, err := e.Store.ListEmbeddings(ctx, item.ChannelID, e.Th.G2PriorScripts, item.ID)
			if err != nil {
				e.log().Warn("compliance: list embeddings", "error", err)
			} else {
				item.PriorEmbeddings = emb
			}
		}
		if len(item.PriorTitles) == 0 {
			titles, err := e.Store.ListTitles(ctx, item.ChannelID, e.Th.G2PriorTitles, item.ID)
			if err != nil {
				e.log().Warn("compliance: list titles", "error", err)
			} else {
				item.PriorTitles = titles
			}
		}
	}

	report := e.RunGates(ctx, item)
	if e.Reports != nil {
		if err := e.Reports.SaveCompliance(ctx, item.ID, report); err != nil {
			return Outcome{Report: report}, fmt.Errorf("compliance: save report: %w", err)
		}
	}

	if report.Passed {
		if err := e.saveFingerprint(ctx, item); err != nil {
			e.log().Warn("compliance: fingerprint", "error", err)
		}
		return Outcome{Report: report}, nil
	}

	max := e.Th.MaxRewrites
	if max <= 0 {
		max = 2
	}
	feedback := gateFeedback(report)
	if p.RewriteAttempt < max {
		if e.Enqueue == nil {
			return Outcome{Report: report}, queue.Permanent(fmt.Errorf("compliance: rewrite needed but no Enqueuer: %s", feedback))
		}
		payload := map[string]any{
			"content_id":        item.ID,
			"channel_id":        item.ChannelID,
			"rewrite_attempt":   p.RewriteAttempt + 1,
			"gate_feedback":     feedback,
			"compliance_report": report,
		}
		if len(p.ScriptWrite) > 0 {
			var sw map[string]any
			if err := json.Unmarshal(p.ScriptWrite, &sw); err == nil {
				for k, v := range sw {
					payload[k] = v
				}
			}
		}
		_, err := e.Enqueue.Enqueue(ctx, JobScriptWrite, payload,
			queue.ContentID(item.ID),
		)
		if err != nil {
			return Outcome{Report: report}, fmt.Errorf("compliance: enqueue rewrite: %w", err)
		}
		e.log().Info("compliance: rewrite requested",
			"content_id", item.ID,
			"attempt", p.RewriteAttempt+1,
			"feedback", feedback,
		)
		return Outcome{Report: report, Rewritten: true}, nil
	}

	e.log().Info("compliance: dead-letter after max rewrites",
		"content_id", item.ID,
		"attempts", p.RewriteAttempt,
		"feedback", feedback,
	)
	return Outcome{Report: report, DeadLetter: true}, queue.Permanent(
		fmt.Errorf("compliance: gates failed after %d rewrites: %s", max, feedback),
	)
}

func (e *Engine) saveFingerprint(ctx context.Context, item ContentItem) error {
	if e.Store == nil || e.Embed == nil {
		return nil
	}
	text := strings.TrimSpace(item.ScriptText)
	if text == "" {
		return nil
	}
	vecs, err := e.Embed.Embed(ctx, []string{text})
	if err != nil {
		return err
	}
	if len(vecs) == 0 {
		return fmt.Errorf("empty embedding")
	}
	sum := sha256.Sum256([]byte(NormalizeForShingle(text)))
	return e.Store.Save(ctx, item.ID, sum[:], EncodeEmbedding(vecs[0]))
}

func (e *Engine) log() *slog.Logger {
	if e.Log != nil {
		return e.Log
	}
	return slog.Default()
}

func gateFeedback(r Report) string {
	var parts []string
	for _, g := range r.Gates {
		if !g.Passed {
			parts = append(parts, fmt.Sprintf("%s: %s", g.ID, g.Detail))
		}
	}
	return strings.Join(parts, "; ")
}

// RegisterHandlers wires compliance.script onto the queue (resource: heavy — embeddings).
func (e *Engine) RegisterHandlers(q *queue.Queue) {
	q.Register(JobScriptCompliance, queue.ResourceHeavy, 3, e.handleJob)
}

func (e *Engine) handleJob(ctx context.Context, job queue.Job) (json.RawMessage, error) {
	var p ScriptPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return nil, queue.Permanent(fmt.Errorf("compliance: payload: %w", err))
	}
	if p.ContentID == "" && job.ContentID != nil {
		p.ContentID = *job.ContentID
	}
	out, err := e.Handle(ctx, p)
	raw, _ := MarshalReport(out.Report)
	return raw, err
}
