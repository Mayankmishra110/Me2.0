// Tests for the M2-117 queue adapters: research.brief (research.go) and
// script.write (script.go). Uses a real embedded SQLite DB (mirrors
// render_test.go's renderTestDB) since both handlers read/write
// content_items directly.
package content

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"mayank2/internal/compliance"
	"mayank2/internal/db"
	"mayank2/internal/llm"
	"mayank2/internal/queue"
)

func handlersTestDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	sqlDB, err := db.Open(ctx, filepath.Join(t.TempDir(), "handlers.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := db.Migrate(ctx, sqlDB); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	mustExec(t, sqlDB, `INSERT INTO channels (id, platform, language, niche, status) VALUES ('ch1','youtube','en','ai','active')`)
	return sqlDB
}

// fakeQueue is a minimal queue.Register/Enqueue surface for handler tests
// that need to observe what a handler chains to next, without a real
// worker pool.
type fakeQueue struct {
	*queue.Queue
	enqueued []fakeEnqueued
}

type fakeEnqueued struct {
	jobType string
	payload any
}

func newFakeQueue(t *testing.T, sqlDB *sql.DB) *fakeQueue {
	t.Helper()
	return &fakeQueue{Queue: queue.New(sqlDB)}
}

func (f *fakeQueue) Enqueue(ctx context.Context, jobType string, payload any, opts ...queue.EnqueueOpt) (string, error) {
	f.enqueued = append(f.enqueued, fakeEnqueued{jobType: jobType, payload: payload})
	return f.Queue.Enqueue(ctx, jobType, payload, opts...)
}

func TestResearchHandleJob_createsContentItemAndChainsToScriptWrite(t *testing.T) {
	sqlDB := handlersTestDB(t)
	fq := newFakeQueue(t, sqlDB)
	// Enqueue needs script.write registered to accept the chained job.
	fq.Queue.Register(JobScriptWrite, queue.ResourceNet, 3, func(context.Context, queue.Job) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	})

	briefJSON := `{"angle":"a","facts":[{"claim":"c","sources":["https://example.com/video"]}],"key_numbers":[],"open_questions":[]}`
	research, err := NewResearch(ResearchOptions{
		OutDir:    t.TempDir(),
		Completer: &fakeCompleter{resp: llmResponse(briefJSON)},
		DB:        sqlDB,
		Enqueue:   fq,
		FetchTranscript: func(context.Context, string) (string, error) {
			return "a transcript with enough words to be a usable source", nil
		},
	})
	if err != nil {
		t.Fatalf("NewResearch: %v", err)
	}

	payload, _ := json.Marshal(ResearchJobPayload{
		ChannelID: "ch1",
		Language:  "en",
		Topic:     "test topic",
		VideoURL:  "https://www.youtube.com/watch?v=abc123",
	})
	job := queue.Job{ID: "job1", Type: JobResearchBrief, Payload: payload}

	out, err := research.handleJob(context.Background(), job)
	if err != nil {
		t.Fatalf("handleJob: %v", err)
	}
	var res struct {
		ContentID string `json:"content_id"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if res.ContentID == "" {
		t.Fatal("expected a content_id to be created")
	}

	var stage string
	if err := sqlDB.QueryRow(`SELECT stage FROM content_items WHERE id=?`, res.ContentID).Scan(&stage); err != nil {
		t.Fatalf("load content_items: %v", err)
	}
	if stage != "researching" {
		t.Fatalf("stage = %q, want researching", stage)
	}

	if len(fq.enqueued) != 1 || fq.enqueued[0].jobType != JobScriptWrite {
		t.Fatalf("expected exactly one script.write enqueue, got %+v", fq.enqueued)
	}
}

func TestResearchHandleJob_requiresChannelIDAndTopic(t *testing.T) {
	research, err := NewResearch(ResearchOptions{OutDir: t.TempDir(), Completer: &fakeCompleter{}})
	if err != nil {
		t.Fatalf("NewResearch: %v", err)
	}
	job := queue.Job{ID: "job1", Type: JobResearchBrief, Payload: json.RawMessage(`{}`)}
	_, err = research.handleJob(context.Background(), job)
	if err == nil || !queue.IsPermanent(err) {
		t.Fatalf("expected a permanent error for missing channel_id/topic, got %v", err)
	}
}

func TestScriptHandleJob_persistsAndChainsToComplianceScript(t *testing.T) {
	sqlDB := handlersTestDB(t)
	mustExec(t, sqlDB, `INSERT INTO content_items (id, channel_id, kind, language, format, stage, created_at)
VALUES ('c1','ch1','short','en','','researching',?)`, time.Now().UTC().Format(time.RFC3339Nano))

	fq := newFakeQueue(t, sqlDB)
	fq.Queue.Register(compliance.JobScriptCompliance, queue.ResourceHeavy, 3, func(context.Context, queue.Job) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	})

	script, err := NewScript(ScriptOptions{
		OutDir:    t.TempDir(),
		Completer: &scriptFakeCompleter{responses: []llm.Response{{Text: validENScriptJSON()}}},
		DB:        sqlDB,
		Enqueue:   fq,
	})
	if err != nil {
		t.Fatalf("NewScript: %v", err)
	}

	payload, _ := json.Marshal(ScriptJobPayload{
		ContentID: "c1",
		ChannelID: "ch1",
		Language:  "en",
		Topic:     "test topic",
		Brief:     sampleBrief(),
	})
	job := queue.Job{ID: "job1", Type: JobScriptWrite, Payload: payload}

	if _, err := script.handleJob(context.Background(), job); err != nil {
		t.Fatalf("handleJob: %v", err)
	}

	var stage, scriptCol string
	if err := sqlDB.QueryRow(`SELECT stage, COALESCE(script,'') FROM content_items WHERE id='c1'`).Scan(&stage, &scriptCol); err != nil {
		t.Fatalf("load content_items: %v", err)
	}
	if stage != "script_written" {
		t.Fatalf("stage = %q, want script_written", stage)
	}
	if scriptCol == "" {
		t.Fatal("expected script column to be persisted")
	}

	if len(fq.enqueued) != 1 || fq.enqueued[0].jobType != compliance.JobScriptCompliance {
		t.Fatalf("expected exactly one compliance.script enqueue, got %+v", fq.enqueued)
	}
	sp, ok := fq.enqueued[0].payload.(compliance.ScriptPayload)
	if !ok {
		t.Fatalf("expected compliance.ScriptPayload, got %T", fq.enqueued[0].payload)
	}
	if sp.ContentID != "c1" || sp.Item.ChannelID != "ch1" || sp.Item.ScriptText == "" {
		t.Fatalf("unexpected compliance payload: %+v", sp)
	}
}

func TestResearchHandleJob_rejectsUnsafeContentID(t *testing.T) {
	research, err := NewResearch(ResearchOptions{OutDir: t.TempDir(), Completer: &fakeCompleter{}})
	if err != nil {
		t.Fatalf("NewResearch: %v", err)
	}
	payload, _ := json.Marshal(ResearchJobPayload{ContentID: "../../etc", ChannelID: "ch1", Language: "en", Topic: "t"})
	job := queue.Job{ID: "job1", Type: JobResearchBrief, Payload: payload}
	_, err = research.handleJob(context.Background(), job)
	if err == nil || !queue.IsPermanent(err) {
		t.Fatalf("expected a permanent error for an unsafe content_id, got %v", err)
	}
}

func TestScriptHandleJob_rejectsUnsafeContentID(t *testing.T) {
	script, err := NewScript(ScriptOptions{OutDir: t.TempDir(), Completer: &scriptFakeCompleter{}})
	if err != nil {
		t.Fatalf("NewScript: %v", err)
	}
	payload, _ := json.Marshal(ScriptJobPayload{ContentID: "../../etc"})
	job := queue.Job{ID: "job1", Type: JobScriptWrite, Payload: payload}
	_, err = script.handleJob(context.Background(), job)
	if err == nil || !queue.IsPermanent(err) {
		t.Fatalf("expected a permanent error for an unsafe content_id, got %v", err)
	}
}

func TestScriptHandleJob_requiresContentID(t *testing.T) {
	script, err := NewScript(ScriptOptions{OutDir: t.TempDir(), Completer: &scriptFakeCompleter{}})
	if err != nil {
		t.Fatalf("NewScript: %v", err)
	}
	job := queue.Job{ID: "job1", Type: JobScriptWrite, Payload: json.RawMessage(`{}`)}
	_, err = script.handleJob(context.Background(), job)
	if err == nil || !queue.IsPermanent(err) {
		t.Fatalf("expected a permanent error for missing content_id, got %v", err)
	}
}

func llmResponse(text string) llm.Response {
	return llm.Response{Text: text, Provider: "fake", Model: "test"}
}
