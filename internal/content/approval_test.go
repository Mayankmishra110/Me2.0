package content

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"mayank2/internal/db"
	"mayank2/internal/events"
	"mayank2/internal/queue"
)

func openApprovalDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	sqlDB, err := db.Open(ctx, filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := db.Migrate(ctx, sqlDB); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	return sqlDB
}

func seedChannelContent(t *testing.T, sqlDB *sql.DB, contentID, channelID, kind, lang string, warmup time.Time) {
	t.Helper()
	execSQL(t, sqlDB, `
INSERT INTO channels (id, platform, handle, language, niche, account_ref, status, warmup_started_at)
VALUES (?, 'youtube', 'h', ?, 'money_side_hustles', '', 'warming', ?)`,
		channelID, lang, warmup.UTC().Format(time.RFC3339Nano))
	execSQL(t, sqlDB, `
INSERT INTO content_items (id, channel_id, kind, language, format, stage, created_at)
VALUES (?, ?, ?, ?, 'myth_vs_fact', 'rendered', ?)`,
		contentID, channelID, kind, lang, time.Now().UTC().Format(time.RFC3339Nano))
}

func execSQL(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec: %v\n%s", err, q)
	}
}

type memEnqueue struct {
	jobs []struct {
		Type    string
		Payload map[string]any
	}
}

func (m *memEnqueue) Enqueue(ctx context.Context, jobType string, payload any, opts ...queue.EnqueueOpt) (string, error) {
	raw, _ := json.Marshal(payload)
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	m.jobs = append(m.jobs, struct {
		Type    string
		Payload map[string]any
	}{Type: jobType, Payload: p})
	return "job-" + jobType, nil
}

func TestApproval_StartEnqueuesRequestAndEvent(t *testing.T) {
	sqlDB := openApprovalDB(t)
	ctx := context.Background()
	seedChannelContent(t, sqlDB, "c1", "yt-money-en", "short", "en", time.Now().Add(-48*time.Hour))

	eq := &memEnqueue{}
	bus := events.New(sqlDB)
	svc := &ApprovalService{
		DB: sqlDB, Enqueue: eq, Events: bus,
		Now: func() time.Time { return time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC) },
		NewID: func() string {
			return "id-" + time.Now().Format("150405.000000000")
		},
	}
	// Stable ids for assert
	n := 0
	svc.NewID = func() string {
		n++
		return "ID" + string(rune('0'+n))
	}

	id, err := svc.Start(ctx, "c1", "short", "summary here", "/data/p.mp4")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if id == "" {
		t.Fatal("empty approval id")
	}
	if len(eq.jobs) != 1 || eq.jobs[0].Type != JobApprovalRequest {
		t.Fatalf("enqueue got %+v", eq.jobs)
	}
	if eq.jobs[0].Payload["approval_id"] != id {
		t.Fatalf("payload %+v", eq.jobs[0].Payload)
	}
	var status, summary string
	err = sqlDB.QueryRow(`SELECT status, summary FROM approvals WHERE id=?`, id).Scan(&status, &summary)
	if err != nil || status != "pending" || summary != "summary here" {
		t.Fatalf("row status=%s summary=%s err=%v", status, summary, err)
	}
	var kind string
	err = sqlDB.QueryRow(`SELECT kind FROM events WHERE kind=?`, events.KindApprovalCreated).Scan(&kind)
	if err != nil {
		t.Fatalf("approval.created event missing: %v", err)
	}
}

func TestApproval_ApproveCreatesPublicationsAtNextSlot(t *testing.T) {
	sqlDB := openApprovalDB(t)
	ctx := context.Background()
	warmup := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	seedChannelContent(t, sqlDB, "c1", "yt-money-en", "short", "en", warmup)

	now := time.Date(2026, 9, 28, 14, 0, 0, 0, time.UTC) // 10:00 ET
	eq := &memEnqueue{}
	ids := 0
	svc := &ApprovalService{
		DB: sqlDB, Enqueue: eq,
		Now: func() time.Time { return now },
		NewID: func() string {
			ids++
			return "X" + string(rune('A'+ids-1))
		},
		ResolveChannel: func(channelID string) (Channel, error) {
			return Channel{
				ID:      channelID,
				Windows: Windows{TZ: "America/New_York", Slots: []string{"12:30", "19:30"}},
			}, nil
		},
	}

	apprID, err := svc.Start(ctx, "c1", "short", "s", "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	err = svc.Decide(ctx, DecideRequest{ApprovalID: apprID, Decision: DecisionApprove})
	if err != nil {
		t.Fatalf("Decide approve: %v", err)
	}

	rows, err := sqlDB.Query(`SELECT platform, account, status, scheduled_at, idempotency_key FROM publications ORDER BY platform`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plats []string
	for rows.Next() {
		var platform, account, status, scheduled, key string
		if err := rows.Scan(&platform, &account, &status, &scheduled, &key); err != nil {
			t.Fatal(err)
		}
		if status != pubStatusScheduled {
			t.Fatalf("status %s", status)
		}
		plats = append(plats, platform)
		at, err := time.Parse(time.RFC3339Nano, scheduled)
		if err != nil {
			t.Fatal(err)
		}
		// Next slot after 10:00 ET is 12:30 ET.
		et, _ := time.LoadLocation("America/New_York")
		local := at.In(et)
		if local.Hour() != 12 || local.Minute() < 30 {
			// first dest exactly 12:30; others staggered +2m
			if local.Hour() != 12 {
				t.Fatalf("scheduled local %v want ~12:30 ET", local)
			}
		}
	}
	want := []string{"facebook", "instagram", "pinterest", "x", "youtube"}
	if len(plats) != len(want) {
		t.Fatalf("platforms %v want %v", plats, want)
	}

	if err := RequireApproved(ctx, sqlDB, "c1"); err != nil {
		t.Fatalf("RequireApproved: %v", err)
	}
}

func TestApproval_RejectAndRedo(t *testing.T) {
	sqlDB := openApprovalDB(t)
	ctx := context.Background()
	seedChannelContent(t, sqlDB, "c1", "yt-ai-en", "short", "en", time.Now().Add(-30*24*time.Hour))

	eq := &memEnqueue{}
	n := 0
	svc := &ApprovalService{
		DB: sqlDB, Enqueue: eq,
		NewID: func() string { n++; return "R" + string(rune('0'+n)) },
	}

	id1, _ := svc.Start(ctx, "c1", "short", "a", "")
	if err := svc.Decide(ctx, DecideRequest{ApprovalID: id1, Decision: DecisionReject}); err != nil {
		t.Fatalf("reject: %v", err)
	}
	var st string
	_ = sqlDB.QueryRow(`SELECT status FROM approvals WHERE id=?`, id1).Scan(&st)
	if st != "rejected" {
		t.Fatalf("status %s", st)
	}
	var pubs int
	_ = sqlDB.QueryRow(`SELECT COUNT(*) FROM publications`).Scan(&pubs)
	if pubs != 0 {
		t.Fatalf("reject must not create publications, got %d", pubs)
	}

	// new content for redo
	execSQL(t, sqlDB, `
INSERT INTO content_items (id, channel_id, kind, language, format, stage, created_at)
VALUES ('c2', 'yt-ai-en', 'short', 'en', 'qa', 'rendered', ?)`, time.Now().UTC().Format(time.RFC3339Nano))
	id2, _ := svc.Start(ctx, "c2", "short", "b", "")
	if err := svc.Decide(ctx, DecideRequest{ApprovalID: id2, Decision: DecisionRedo}); err == nil {
		t.Fatal("redo without note should fail")
	}
	if err := svc.Decide(ctx, DecideRequest{ApprovalID: id2, Decision: DecisionRedo, Note: "hook weaker"}); err != nil {
		t.Fatalf("redo: %v", err)
	}
	_ = sqlDB.QueryRow(`SELECT status FROM approvals WHERE id=?`, id2).Scan(&st)
	if st != "redo" {
		t.Fatalf("status %s", st)
	}
	found := false
	for _, j := range eq.jobs {
		if j.Type == JobScriptWrite && j.Payload["redo_notes"] == "hook weaker" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected script.write with redo notes, jobs=%+v", eq.jobs)
	}
}

func TestRequireApproved_blocksWithoutApproval(t *testing.T) {
	sqlDB := openApprovalDB(t)
	ctx := context.Background()
	seedChannelContent(t, sqlDB, "c1", "yt-ai-en", "short", "en", time.Now())
	err := RequireApproved(ctx, sqlDB, "c1")
	if !errors.Is(err, ErrNotApproved) {
		t.Fatalf("want ErrNotApproved, got %v", err)
	}
}

func TestNextSlot(t *testing.T) {
	et, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	// 10:00 ET → next 12:30 ET
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, et)
	got, err := NextSlot(now, Windows{TZ: "America/New_York", Slots: []string{"12:30", "19:30"}})
	if err != nil {
		t.Fatal(err)
	}
	local := got.In(et)
	if local.Hour() != 12 || local.Minute() != 30 {
		t.Fatalf("got %v", local)
	}
	// After last slot → next day first slot
	now = time.Date(2026, 9, 28, 20, 0, 0, 0, et)
	got, err = NextSlot(now, Windows{TZ: "America/New_York", Slots: []string{"12:30", "19:30"}})
	if err != nil {
		t.Fatal(err)
	}
	local = got.In(et)
	if local.Day() != 29 || local.Hour() != 12 {
		t.Fatalf("got %v want next day 12:30", local)
	}
}

func TestDestinationsFor_hindiYouTubeOnly(t *testing.T) {
	d := DestinationsFor("yt-money-hi", "short", "hi")
	if len(d) != 1 || d[0].Platform != "youtube" {
		t.Fatalf("%+v", d)
	}
}

func TestApproval_capBlocksApprove(t *testing.T) {
	sqlDB := openApprovalDB(t)
	ctx := context.Background()
	// No warmup → CapsFor zero
	execSQL(t, sqlDB, `
INSERT INTO channels (id, platform, handle, language, niche, account_ref, status, warmup_started_at)
VALUES ('yt-ai-en', 'youtube', 'h', 'en', 'ai', '', 'warming', NULL)`)
	execSQL(t, sqlDB, `
INSERT INTO content_items (id, channel_id, kind, language, format, stage, created_at)
VALUES ('c1', 'yt-ai-en', 'short', 'en', 'qa', 'rendered', ?)`, time.Now().UTC().Format(time.RFC3339Nano))

	svc := &ApprovalService{DB: sqlDB, Enqueue: &memEnqueue{}, NewID: func() string { return "Z1" }}
	// Second id for nonce
	n := 0
	svc.NewID = func() string { n++; return "Z" + string(rune('0'+n)) }
	id, err := svc.Start(ctx, "c1", "short", "s", "")
	if err != nil {
		t.Fatal(err)
	}
	err = svc.Decide(ctx, DecideRequest{ApprovalID: id, Decision: DecisionApprove})
	if err == nil {
		t.Fatal("approve should fail when caps are 0")
	}
	var st string
	_ = sqlDB.QueryRow(`SELECT status FROM approvals WHERE id=?`, id).Scan(&st)
	if st != "pending" {
		t.Fatalf("failed approve must leave status pending, got %s", st)
	}
	if err := RequireApproved(ctx, sqlDB, "c1"); err == nil {
		t.Fatal("RequireApproved must still refuse after failed approve")
	}
}
