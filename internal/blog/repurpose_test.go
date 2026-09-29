// Tests for the M2-117 blog.repurpose dispatcher (repurpose.go), which fans
// the shared SPEC §5 "blog.repurpose" job type out to the LinkedIn (M2-402)
// and X-personal (M2-403) legs.
package blog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mayank2/internal/llm"
	"mayank2/internal/queue"
)

func TestDispatcher_handle_runsBothLegs(t *testing.T) {
	sqlDB := openBlogDBNoSeed(t)
	mustExecBlog(t, sqlDB, `
INSERT INTO channels (id, platform, handle, language, niche, account_ref, status)
VALUES ('blog-ch', 'mayankbuilt', '', 'en', 'blog', '', 'active')`)
	topicJSON, _ := json.Marshal(map[string]string{"topic": "Shipping a Go daemon on 16 GB"})
	mustExecBlog(t, sqlDB, `
INSERT INTO content_items (id, channel_id, kind, language, stage, script, created_at)
VALUES ('c1', 'blog-ch', 'blog', 'en', 'live', ?, ?)`, string(topicJSON), time.Now().UTC().Format(time.RFC3339Nano))
	mustExecBlog(t, sqlDB, `
INSERT INTO publications (id, content_id, platform, account, status, url, idempotency_key, published_at)
VALUES ('pub1', 'c1', 'mayankbuilt', 'mayankbuilt', 'published', ?, 'c1:mayankbuilt', ?)`,
		sourceURL, time.Now().UTC().Format(time.RFC3339Nano))
	mdxPath := filepath.Join(t.TempDir(), "post.mdx")
	if err := os.WriteFile(mdxPath, []byte("---\ntitle: Go Daemon\n---\n\n"+sourceBody), 0o644); err != nil {
		t.Fatalf("write mdx: %v", err)
	}
	mustExecBlog(t, sqlDB, `INSERT INTO assets (id, content_id, kind, path) VALUES ('a1', 'c1', 'mdx', ?)`, mdxPath)

	liDraft := "Most 'production-ready' setups I see are five services held together by hope.\n\n" +
		"Mine is one Go binary and SQLite.\n\nFull writeup: " + sourceURL
	liComp := &seqCompleter{responses: []string{liDraft}}
	liAppr := &fakeApprover{returnID: "ap-li"}
	linkedin, err := NewRepurposer(Options{Completer: liComp, Approver: liAppr})
	if err != nil {
		t.Fatalf("NewRepurposer: %v", err)
	}

	xComp := &seqXCompleter{responses: []llm.Response{threadResponse("Shipping a Go daemon on 16 GB of RAM — a thread. " + sourceURL)}}
	xAppr := &fakeApprovalStarter{}
	x := &XRepurposer{DB: sqlDB, LLM: xComp, Approval: xAppr, ChannelID: "blog-ch"}

	d, err := NewDispatcher(RepurposeOptions{DB: sqlDB, LinkedIn: linkedin, X: x})
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}

	payload, _ := json.Marshal(RepurposePayload{ContentID: "c1"})
	job := queue.Job{ID: "job1", Type: JobRepurpose, Payload: payload}

	out, err := d.handle(context.Background(), job)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	var res RepurposeResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.LinkedInApprovalID != "ap-li" {
		t.Fatalf("LinkedInApprovalID = %q, want ap-li", res.LinkedInApprovalID)
	}
	if res.LinkedInError != "" {
		t.Fatalf("unexpected LinkedInError: %s", res.LinkedInError)
	}
	if res.XApprovalID == "" || res.XContentID == "" {
		t.Fatalf("expected X leg to succeed, got %+v", res)
	}
	if res.XError != "" {
		t.Fatalf("unexpected XError: %s", res.XError)
	}
	if liAppr.gotContentID != "c1" {
		t.Fatalf("linkedin approval content_id = %q, want c1", liAppr.gotContentID)
	}
	if len(xAppr.calls) != 1 {
		t.Fatalf("expected exactly one X approval.Start call, got %d", len(xAppr.calls))
	}
}

func TestDispatcher_handle_requiresContentID(t *testing.T) {
	sqlDB := openBlogDBNoSeed(t)
	d, err := NewDispatcher(RepurposeOptions{DB: sqlDB})
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}
	job := queue.Job{ID: "job1", Type: JobRepurpose, Payload: json.RawMessage(`{}`)}
	_, err = d.handle(context.Background(), job)
	if err == nil || !queue.IsPermanent(err) {
		t.Fatalf("expected a permanent error for missing content_id, got %v", err)
	}
}

func TestNewDispatcher_requiresDB(t *testing.T) {
	if _, err := NewDispatcher(RepurposeOptions{}); err == nil {
		t.Fatal("expected an error when DB is nil")
	}
}
