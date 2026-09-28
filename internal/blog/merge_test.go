package blog

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"mayank2/internal/content"
	"mayank2/internal/queue"
)

func TestLivePostURL(t *testing.T) {
	cases := []struct{ base, slug, want string }{
		{"https://mayankbuilt.com", "why-go", "https://mayankbuilt.com/blog/why-go"},
		{"https://mayankbuilt.com/", "why-go", "https://mayankbuilt.com/blog/why-go"},
		{"https://mayankbuilt.com/site", "x", "https://mayankbuilt.com/site/blog/x"},
		{"", "x", ""},
		{"https://mayankbuilt.com", "", ""},
	}
	for _, c := range cases {
		if got := livePostURL(c.base, c.slug); got != c.want {
			t.Errorf("livePostURL(%q,%q)=%q want %q", c.base, c.slug, got, c.want)
		}
	}
}

func TestMerge_RunHappyPath(t *testing.T) {
	_, cloneDir := newTestMayankbuiltRepo(t)
	cfg := Config{
		RepoPath: cloneDir, BaseBranch: "main", Remote: "origin",
		PostsDir: "content/blog", SiteBaseURL: "https://example.test",
	}
	git := NewLocalGitRepo(cfg, nil, nil)
	sqlDB := openBlogDB(t)
	draft, err := NewDraft(DraftOptions{
		Config: cfg, Completer: &fakeCompleter{body: "Happy path merge body."},
		Git: git, DB: sqlDB, Approvals: &fakeApproval{nextID: seqIDs("appr-")},
		Now: fixedNow(time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)), NewID: seqIDs("ID"),
	})
	if err != nil {
		t.Fatalf("NewDraft: %v", err)
	}
	dres, err := draft.Run(context.Background(), DraftPayload{Topic: "Merge Happy Path", TopicsSource: "manual"})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	seedApproval(t, sqlDB, "appr-approved", dres.ContentID, "approved")

	merge, err := NewMerge(MergeOptions{
		Config: cfg, Git: git, DB: sqlDB,
		Now: fixedNow(time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC)), NewID: seqIDs("PUB"),
	})
	if err != nil {
		t.Fatalf("NewMerge: %v", err)
	}
	mres, err := merge.Run(context.Background(), MergePayload{ContentID: dres.ContentID})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if mres.AlreadyMerged {
		t.Error("first merge should not report already_merged")
	}
	if mres.Slug != "merge-happy-path" || mres.Branch != "blog/merge-happy-path" {
		t.Errorf("slug/branch = %q/%q", mres.Slug, mres.Branch)
	}
	wantURL := "https://example.test/blog/merge-happy-path"
	if mres.LiveURL != wantURL {
		t.Errorf("live url = %q, want %q", mres.LiveURL, wantURL)
	}
	got := runGitT(t, cloneDir, "show", "main:content/blog/merge-happy-path.mdx")
	if !strings.Contains(got, "Happy path merge body.") {
		t.Errorf("main missing body:\n%s", got)
	}
	var platform, status, url string
	if err := sqlDB.QueryRow(`SELECT platform, status, url FROM publications WHERE id=?`, mres.PublicationID).
		Scan(&platform, &status, &url); err != nil {
		t.Fatalf("publications: %v", err)
	}
	if platform != PlatformMayankbuilt || status != "published" || url != wantURL {
		t.Errorf("pub = %s/%s/%s", platform, status, url)
	}
}

func TestMerge_RunIdempotentReMerge(t *testing.T) {
	_, cloneDir := newTestMayankbuiltRepo(t)
	cfg := Config{RepoPath: cloneDir, BaseBranch: "main", Remote: "origin", PostsDir: "content/blog", SiteBaseURL: "https://example.test"}
	git := NewLocalGitRepo(cfg, nil, nil)
	sqlDB := openBlogDB(t)
	draft, err := NewDraft(DraftOptions{
		Config: cfg, Completer: &fakeCompleter{body: "Idempotent body."}, Git: git, DB: sqlDB,
		Approvals: &fakeApproval{nextID: seqIDs("appr-")}, NewID: seqIDs("ID"),
	})
	if err != nil {
		t.Fatalf("NewDraft: %v", err)
	}
	dres, err := draft.Run(context.Background(), DraftPayload{Topic: "Idempotent Merge", TopicsSource: "manual"})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	seedApproval(t, sqlDB, "appr-ok", dres.ContentID, "approved")
	merge, err := NewMerge(MergeOptions{Config: cfg, Git: git, DB: sqlDB, NewID: seqIDs("PUB")})
	if err != nil {
		t.Fatalf("NewMerge: %v", err)
	}
	first, err := merge.Run(context.Background(), MergePayload{ContentID: dres.ContentID})
	if err != nil {
		t.Fatalf("merge 1: %v", err)
	}
	second, err := merge.Run(context.Background(), MergePayload{ContentID: dres.ContentID})
	if err != nil {
		t.Fatalf("merge 2: %v", err)
	}
	if !second.AlreadyMerged {
		t.Error("second merge should be already_merged")
	}
	if second.PublicationID != first.PublicationID {
		t.Errorf("publication id changed")
	}
	var n int
	_ = sqlDB.QueryRow(`SELECT COUNT(*) FROM publications WHERE content_id=? AND platform=?`,
		dres.ContentID, PlatformMayankbuilt).Scan(&n)
	if n != 1 {
		t.Errorf("count=%d", n)
	}
}

func TestMerge_RunRejectsWithoutApproval(t *testing.T) {
	sqlDB := openBlogDB(t)
	seedBlogContentItem(t, sqlDB, "c-reject")
	fake := &fakeGitRepo{dir: t.TempDir()}
	merge, err := NewMerge(MergeOptions{Config: Config{RepoPath: fake.dir}, Git: fake, DB: sqlDB})
	if err != nil {
		t.Fatalf("NewMerge: %v", err)
	}
	_, err = merge.Run(context.Background(), MergePayload{ContentID: "c-reject", Branch: "blog/x", Slug: "x"})
	if err == nil || !queue.IsPermanent(err) || !errors.Is(err, content.ErrNotApproved) {
		t.Fatalf("want permanent not-approved, got %v", err)
	}
	if fake.fetchCalls != 0 || fake.ffMergeCalls != 0 {
		t.Errorf("git must not run: fetch=%d ff=%d", fake.fetchCalls, fake.ffMergeCalls)
	}
}

func TestMerge_RunRejectPathNeverMerges(t *testing.T) {
	sqlDB := openBlogDB(t)
	seedBlogContentItem(t, sqlDB, "c-rej2")
	seedApproval(t, sqlDB, "appr-rej", "c-rej2", "rejected")
	fake := &fakeGitRepo{dir: t.TempDir()}
	merge, err := NewMerge(MergeOptions{Config: Config{RepoPath: fake.dir}, Git: fake, DB: sqlDB})
	if err != nil {
		t.Fatalf("NewMerge: %v", err)
	}
	_, err = merge.Run(context.Background(), MergePayload{ContentID: "c-rej2", Branch: "blog/x", Slug: "x"})
	if err == nil || !errors.Is(err, content.ErrNotApproved) {
		t.Fatalf("want not-approved, got %v", err)
	}
	if fake.ffMergeCalls != 0 {
		t.Error("rejected must not merge")
	}
}

func TestMerge_HandlerAdaptsToQueue(t *testing.T) {
	_, cloneDir := newTestMayankbuiltRepo(t)
	cfg := Config{RepoPath: cloneDir, BaseBranch: "main", Remote: "origin", PostsDir: "content/blog", SiteBaseURL: "https://example.test"}
	git := NewLocalGitRepo(cfg, nil, nil)
	sqlDB := openBlogDB(t)
	draft, err := NewDraft(DraftOptions{
		Config: cfg, Completer: &fakeCompleter{body: "Handler topic body."}, Git: git, DB: sqlDB,
		Approvals: &fakeApproval{nextID: seqIDs("appr-")}, NewID: seqIDs("ID"),
	})
	if err != nil {
		t.Fatalf("NewDraft: %v", err)
	}
	dres, err := draft.Run(context.Background(), DraftPayload{Topic: "Handler Topic", TopicsSource: "manual"})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	seedApproval(t, sqlDB, "appr-h", dres.ContentID, "approved")
	merge, err := NewMerge(MergeOptions{Config: cfg, Git: git, DB: sqlDB, NewID: seqIDs("PUB")})
	if err != nil {
		t.Fatalf("NewMerge: %v", err)
	}
	payload, _ := json.Marshal(MergePayload{ContentID: dres.ContentID})
	raw, err := merge.Handler()(context.Background(), queue.Job{Payload: payload})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	var out MergeResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.PublicationID == "" || out.LiveURL == "" {
		t.Errorf("result = %+v", out)
	}
}
