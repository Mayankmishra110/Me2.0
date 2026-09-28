// Approval flow (M2-210): pending approvals → Telegram/dashboard; approve
// schedules publications at the next channel slots within cadence caps;
// reject/redo route correctly. Publishers must call RequireApproved.
package content

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"mayank2/internal/events"
	"mayank2/internal/queue"
)

const (
	JobApprovalRequest = "approval.request"
	JobScriptWrite     = "script.write"

	pubStatusScheduled = "scheduled"
)

// ErrNotApproved means no approved approvals row exists for the content item.
var ErrNotApproved = errors.New("content: no approved approval for content item")

// ApprovalDB is the DB surface ApprovalService needs.
type ApprovalDB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ApprovalEnqueuer enqueues follow-up jobs.
type ApprovalEnqueuer interface {
	Enqueue(ctx context.Context, jobType string, payload any, opts ...queue.EnqueueOpt) (id string, err error)
}

// EventEmitter is optional (dashboard SSE via events bus).
type EventEmitter interface {
	Emit(ctx context.Context, actor events.Actor, kind, ref, message string, data any) (events.Event, error)
}

// ApprovalService owns approval.request creation and decisions.
type ApprovalService struct {
	DB      ApprovalDB
	Enqueue ApprovalEnqueuer
	Events  EventEmitter
	Now     func() time.Time
	NewID   func() string
	Log     *slog.Logger
	// ChannelsDir is where config/channels/*.yaml live (for windows/slots).
	// Optional: if empty, SlotResolver must be set or approve uses UTC now.
	ChannelsDir string
	// ResolveChannel loads channel posting windows; nil → LoadChannels lookup.
	ResolveChannel func(channelID string) (Channel, error)
}

func (s *ApprovalService) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *ApprovalService) newID() string {
	if s != nil && s.NewID != nil {
		return s.NewID()
	}
	return newApprovalULID(time.Now)
}

func (s *ApprovalService) log() *slog.Logger {
	if s != nil && s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Start creates a pending approvals row and enqueues approval.request
// (Telegram handler + dashboard list). Implements compliance.ApprovalStarter.
func (s *ApprovalService) Start(ctx context.Context, contentID, kind, summary, previewPath string) (string, error) {
	if s == nil || s.DB == nil {
		return "", fmt.Errorf("approval: nil service/db")
	}
	if contentID == "" {
		return "", fmt.Errorf("approval: content_id required")
	}
	if kind == "" {
		kind = "short"
	}
	id := s.newID()
	nonce := s.newID()
	var preview any
	if strings.TrimSpace(previewPath) != "" {
		preview = previewPath
	}
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO approvals (id, content_id, kind, summary, preview_path, status, nonce)
VALUES (?, ?, ?, ?, ?, 'pending', ?)`,
		id, contentID, kind, summary, preview, nonce,
	)
	if err != nil {
		return "", fmt.Errorf("approval: insert %s: %w", id, err)
	}
	if s.Enqueue != nil {
		_, err = s.Enqueue.Enqueue(ctx, JobApprovalRequest, map[string]any{
			"approval_id": id,
		}, queue.ContentID(contentID))
		if err != nil {
			return "", fmt.Errorf("approval: enqueue approval.request: %w", err)
		}
	}
	if s.Events != nil {
		_, _ = s.Events.Emit(ctx, events.ActorSystem, events.KindApprovalCreated, id, "approval pending", map[string]any{
			"content_id": contentID,
			"kind":       kind,
		})
	}
	s.log().Info("approval: requested", "approval_id", id, "content_id", contentID)
	return id, nil
}

// Decision is approve|reject|redo.
type Decision string

const (
	DecisionApprove Decision = "approve"
	DecisionReject  Decision = "reject"
	DecisionRedo    Decision = "redo"
)

// DecideRequest is one human decision (Telegram or dashboard).
type DecideRequest struct {
	ApprovalID string
	Decision   Decision
	Note       string
	// ChannelWindows optional override for tests (TZ + slots).
	Windows *Windows
	// Destinations optional override; nil → DestinationsFor(channel, kind, lang).
	Destinations []PublicationDest
}

// PublicationDest is one scheduled destination.
type PublicationDest struct {
	Platform string
	Account  string
}

// Decide applies approve/reject/redo. Approve creates publications at the
// channel's next slots within caps. Redo enqueues script.write with the note.
func (s *ApprovalService) Decide(ctx context.Context, req DecideRequest) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("approval: nil service/db")
	}
	switch req.Decision {
	case DecisionApprove, DecisionReject, DecisionRedo:
	default:
		return fmt.Errorf("approval: invalid decision %q", req.Decision)
	}

	var contentID, kind, status string
	err := s.DB.QueryRowContext(ctx, `
SELECT content_id, kind, status FROM approvals WHERE id=?`, req.ApprovalID).
		Scan(&contentID, &kind, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("approval: %s not found", req.ApprovalID)
	}
	if err != nil {
		return fmt.Errorf("approval: load %s: %w", req.ApprovalID, err)
	}
	if status != "pending" {
		return fmt.Errorf("approval: %s already %s", req.ApprovalID, status)
	}

	at := s.now().Format(time.RFC3339Nano)
	note := strings.TrimSpace(req.Note)
	var noteArg any
	if note != "" {
		noteArg = note
	}

	var newStatus string
	switch req.Decision {
	case DecisionApprove:
		newStatus = "approved"
		// Schedule before flipping status so a cap/slot failure cannot leave
		// an approved row with no publications.
		if err := s.schedulePublications(ctx, contentID, kind, req); err != nil {
			return err
		}
	case DecisionReject:
		newStatus = "rejected"
	case DecisionRedo:
		newStatus = "redo"
		if note == "" {
			return fmt.Errorf("approval: redo requires a note")
		}
	}

	res, err := s.DB.ExecContext(ctx, `
UPDATE approvals SET status=?, note=COALESCE(?, note), decided_at=?, nonce=''
WHERE id=? AND status='pending'`,
		newStatus, noteArg, at, req.ApprovalID,
	)
	if err != nil {
		return fmt.Errorf("approval: decide %s: %w", req.ApprovalID, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("approval: %s already decided", req.ApprovalID)
	}

	if req.Decision == DecisionRedo && s.Enqueue != nil {
		_, err = s.Enqueue.Enqueue(ctx, JobScriptWrite, map[string]any{
			"content_id":  contentID,
			"redo_notes":  note,
			"approval_id": req.ApprovalID,
		}, queue.ContentID(contentID))
		if err != nil {
			return fmt.Errorf("approval: enqueue script.write for redo: %w", err)
		}
	}

	if s.Events != nil {
		_, _ = s.Events.Emit(ctx, events.ActorUser, events.KindApprovalDecided, req.ApprovalID, newStatus, map[string]any{
			"decision":   string(req.Decision),
			"content_id": contentID,
		})
	}
	return nil
}

func (s *ApprovalService) schedulePublications(ctx context.Context, contentID, kind string, req DecideRequest) error {
	var channelID, language string
	err := s.DB.QueryRowContext(ctx, `
SELECT channel_id, language FROM content_items WHERE id=?`, contentID).
		Scan(&channelID, &language)
	if err != nil {
		return fmt.Errorf("approval: load content %s: %w", contentID, err)
	}

	rec, err := GetChannel(ctx, s.DB, channelID)
	if err != nil {
		return err
	}
	if rec == nil {
		return fmt.Errorf("approval: channel %s not found", channelID)
	}
	caps := CapsFor(*rec, s.now())
	if kind == "short" && caps.ShortsPerDay <= 0 {
		return fmt.Errorf("approval: channel %s short cap is 0", channelID)
	}
	if kind == "long" && caps.LongPerWeek <= 0 {
		return fmt.Errorf("approval: channel %s long cap is 0", channelID)
	}

	// Re-check channel cadence counts (F5 at decide time).
	if kind == "short" {
		n, err := s.countScheduled(ctx, channelID, "short", s.now().Truncate(24*time.Hour), s.now().Truncate(24*time.Hour).Add(24*time.Hour))
		if err != nil {
			return err
		}
		if n >= caps.ShortsPerDay {
			return fmt.Errorf("approval: short daily cap reached for %s (%d/%d)", channelID, n, caps.ShortsPerDay)
		}
	}
	if kind == "long" {
		weekStart := startOfUTCWeek(s.now())
		n, err := s.countScheduled(ctx, channelID, "long", weekStart, weekStart.Add(7*24*time.Hour))
		if err != nil {
			return err
		}
		if n >= caps.LongPerWeek {
			return fmt.Errorf("approval: long weekly cap reached for %s (%d/%d)", channelID, n, caps.LongPerWeek)
		}
	}

	dests := req.Destinations
	if dests == nil {
		dests = DestinationsFor(channelID, kind, language)
	}
	windows := req.Windows
	if windows == nil {
		ch, err := s.channelConfig(channelID)
		if err != nil {
			s.log().Warn("approval: channel config missing; scheduling at now", "channel", channelID, "error", err)
			w := Windows{TZ: "UTC", Slots: []string{s.now().Format("15:04")}}
			windows = &w
		} else {
			windows = &ch.Windows
		}
	}

	slot, err := NextSlot(s.now(), *windows)
	if err != nil {
		return fmt.Errorf("approval: next slot: %w", err)
	}

	account := rec.AccountRef
	if account == "" {
		account = channelID
	}

	for i, d := range dests {
		plat := d.Platform
		acc := d.Account
		if acc == "" {
			acc = account
		}
		// Stagger multi-platform by a few minutes so they don't collide.
		when := slot.Add(time.Duration(i) * 2 * time.Minute)
		id := s.newID()
		key := contentID + ":" + plat + ":" + acc
		_, err := s.DB.ExecContext(ctx, `
INSERT INTO publications (id, content_id, platform, account, scheduled_at, status, idempotency_key)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(idempotency_key) DO NOTHING`,
			id, contentID, plat, acc, when.UTC().Format(time.RFC3339Nano), pubStatusScheduled, key,
		)
		if err != nil {
			return fmt.Errorf("approval: insert publication %s: %w", key, err)
		}
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE content_items SET stage='approved' WHERE id=?`, contentID)
	return nil
}

func (s *ApprovalService) countScheduled(ctx context.Context, channelID, kind string, from, to time.Time) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM publications p
JOIN content_items c ON c.id = p.content_id
WHERE c.channel_id=? AND c.kind=? AND p.status IN ('scheduled','publishing','published')
  AND p.scheduled_at >= ? AND p.scheduled_at < ?`,
		channelID, kind, from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano),
	).Scan(&n)
	return n, err
}

func (s *ApprovalService) channelConfig(channelID string) (Channel, error) {
	if s.ResolveChannel != nil {
		return s.ResolveChannel(channelID)
	}
	if s.ChannelsDir == "" {
		return Channel{}, fmt.Errorf("no channels dir")
	}
	chs, err := LoadChannels(s.ChannelsDir)
	if err != nil {
		return Channel{}, err
	}
	for _, ch := range chs {
		if ch.ID == channelID {
			return ch, nil
		}
	}
	return Channel{}, fmt.Errorf("channel %s not in %s", channelID, s.ChannelsDir)
}

// DestinationsFor returns default cross-post targets (CONTENT_STRATEGY §1).
func DestinationsFor(channelID, kind, language string) []PublicationDest {
	lang := strings.ToLower(language)
	// Hindi → YouTube only until a Hindi IG account exists.
	if lang == "hi" {
		return []PublicationDest{{Platform: "youtube", Account: channelID}}
	}
	switch kind {
	case "long":
		return []PublicationDest{
			{Platform: "youtube", Account: channelID},
			{Platform: "facebook", Account: "fb-en"},
			{Platform: "x", Account: "x-business"},
		}
	default: // short / post
		return []PublicationDest{
			{Platform: "youtube", Account: channelID},
			{Platform: "instagram", Account: "ig-en"},
			{Platform: "facebook", Account: "fb-en"},
			{Platform: "x", Account: "x-business"},
			{Platform: "pinterest", Account: "pin-en"},
		}
	}
}

// NextSlot returns the next posting time from windows.slots in windows.tz
// at or after now (DESIGN audience windows / channel YAML).
func NextSlot(now time.Time, w Windows) (time.Time, error) {
	if len(w.Slots) == 0 {
		return now.UTC(), nil
	}
	loc, err := time.LoadLocation(w.TZ)
	if err != nil {
		return time.Time{}, fmt.Errorf("windows.tz %q: %w", w.TZ, err)
	}
	local := now.In(loc)
	best := time.Time{}
	for dayOffset := 0; dayOffset < 8; dayOffset++ {
		day := local.AddDate(0, 0, dayOffset)
		for _, slot := range w.Slots {
			hh, mm, ok := parseHHMM(slot)
			if !ok {
				continue
			}
			cand := time.Date(day.Year(), day.Month(), day.Day(), hh, mm, 0, 0, loc)
			if !cand.Before(local) {
				if best.IsZero() || cand.Before(best) {
					best = cand
				}
			}
		}
		if !best.IsZero() {
			return best.UTC(), nil
		}
	}
	return now.UTC(), nil
}

func parseHHMM(s string) (h, m int, ok bool) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(parts[0], "%d", &h); err != nil {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &m); err != nil {
		return 0, 0, false
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

func startOfUTCWeek(t time.Time) time.Time {
	t = t.UTC().Truncate(24 * time.Hour)
	// Monday-start week
	weekday := int(t.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	return t.AddDate(0, 0, -(weekday - 1))
}

// RequireApproved refuses publish when there is no approved approvals row.
// Publisher base must call this before any platform API call.
func RequireApproved(ctx context.Context, db ApprovalDB, contentID string) error {
	if contentID == "" {
		return fmt.Errorf("%w: empty content_id", ErrNotApproved)
	}
	var status string
	err := db.QueryRowContext(ctx, `
SELECT status FROM approvals
WHERE content_id=? AND status='approved'
ORDER BY decided_at DESC LIMIT 1`, contentID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s", ErrNotApproved, contentID)
	}
	if err != nil {
		return fmt.Errorf("content: check approval %s: %w", contentID, err)
	}
	return nil
}

// RequireApprovedPublication checks the specific publication's content has
// an approved approval (and optionally that the publication row exists).
func RequireApprovedPublication(ctx context.Context, db ApprovalDB, publicationID string) error {
	var contentID string
	err := db.QueryRowContext(ctx, `SELECT content_id FROM publications WHERE id=?`, publicationID).Scan(&contentID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("content: publication %s not found", publicationID)
	}
	if err != nil {
		return err
	}
	return RequireApproved(ctx, db, contentID)
}

func newApprovalULID(now func() time.Time) string {
	const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	ms := uint64(now().UTC().UnixMilli())
	var buf [26]byte
	for i := 9; i >= 0; i-- {
		buf[i] = crockford[ms&31]
		ms >>= 5
	}
	var r [16]byte
	_, _ = rand.Read(r[:])
	for i := 10; i < 26; i++ {
		buf[i] = crockford[int(r[i-10])%32]
	}
	return string(buf[:])
}
