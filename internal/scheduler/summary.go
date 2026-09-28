package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"mayank2/internal/queue"
	"mayank2/internal/storage"
)

const settingClaudeLimit = "claude:limit_state"

// DailySummary is the structured DESIGN §2 daily summary payload.
type DailySummary struct {
	PublishedByChannel map[string]int
	PendingApprovals   int
	Failures           int
	BestPerformer      string
	BestViews          int
	QuotaLines         []string
	DiskFreeBytes      uint64
	DiskFreeOK         bool
	ClaudeLimitState   string
	GeneratedAt        time.Time
}

func (s *Scheduler) handleSummaryDaily(ctx context.Context, job queue.Job) (json.RawMessage, error) {
	_ = job
	sum, err := s.buildDailySummary(ctx)
	if err != nil {
		return nil, err
	}
	text := FormatDailySummary(sum)
	if s.notify != nil {
		if err := s.notify.Notify(ctx, text); err != nil {
			return nil, fmt.Errorf("scheduler: summary.daily notify: %w", err)
		}
	} else {
		s.log.Info("scheduler: summary.daily (no notifier)", "bytes", len(text))
	}
	body, err := json.Marshal(map[string]any{
		"pending":  sum.PendingApprovals,
		"failures": sum.Failures,
		"sent":     s.notify != nil,
	})
	if err != nil {
		return nil, err
	}
	return body, nil
}

func (s *Scheduler) buildDailySummary(ctx context.Context) (DailySummary, error) {
	now := s.now().UTC()
	since := now.Add(-24 * time.Hour).Format(time.RFC3339Nano)
	sum := DailySummary{
		PublishedByChannel: map[string]int{},
		GeneratedAt:        now,
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT ci.channel_id, COUNT(*)
FROM publications p
JOIN content_items ci ON ci.id = p.content_id
WHERE p.status = 'published'
  AND p.published_at IS NOT NULL
  AND p.published_at >= ?
GROUP BY ci.channel_id
ORDER BY ci.channel_id`, since)
	if err != nil {
		return sum, fmt.Errorf("scheduler: summary published: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var ch string
		var n int
		if err := rows.Scan(&ch, &n); err != nil {
			return sum, fmt.Errorf("scheduler: summary published scan: %w", err)
		}
		sum.PublishedByChannel[ch] = n
	}
	if err := rows.Err(); err != nil {
		return sum, err
	}

	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM approvals WHERE status='pending'`).Scan(&sum.PendingApprovals); err != nil {
		return sum, fmt.Errorf("scheduler: summary pending: %w", err)
	}

	if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM jobs
WHERE status IN ('failed', 'dead') AND updated_at >= ?`, since).Scan(&sum.Failures); err != nil {
		return sum, fmt.Errorf("scheduler: summary failures: %w", err)
	}

	var bestAccount sql.NullString
	var bestURL sql.NullString
	var bestViews sql.NullInt64
	err = s.db.QueryRowContext(ctx, `
SELECT p.account, p.url, m.views
FROM metrics m
JOIN publications p ON p.id = m.publication_id
WHERE m.captured_at >= ?
ORDER BY m.views DESC
LIMIT 1`, since).Scan(&bestAccount, &bestURL, &bestViews)
	switch {
	case err == sql.ErrNoRows:
		sum.BestPerformer = "(none)"
	case err != nil:
		return sum, fmt.Errorf("scheduler: summary best: %w", err)
	default:
		sum.BestViews = int(bestViews.Int64)
		label := bestAccount.String
		if bestURL.Valid && bestURL.String != "" {
			label = fmt.Sprintf("%s · %s", bestAccount.String, bestURL.String)
		}
		if label == "" {
			label = "(unknown)"
		}
		sum.BestPerformer = fmt.Sprintf("%s (%d views)", label, sum.BestViews)
	}

	qrows, err := s.db.QueryContext(ctx, `SELECT provider, used, "limit" FROM quotas ORDER BY provider`)
	if err != nil {
		return sum, fmt.Errorf("scheduler: summary quotas: %w", err)
	}
	defer qrows.Close()
	for qrows.Next() {
		var provider string
		var used, limit int
		if err := qrows.Scan(&provider, &used, &limit); err != nil {
			return sum, err
		}
		left := limit - used
		if limit <= 0 {
			sum.QuotaLines = append(sum.QuotaLines, fmt.Sprintf("%s: used %d (no limit recorded)", provider, used))
		} else {
			sum.QuotaLines = append(sum.QuotaLines, fmt.Sprintf("%s: %d left (%d/%d)", provider, left, used, limit))
		}
	}
	if err := qrows.Err(); err != nil {
		return sum, err
	}

	diskPath := s.cfg.DataDir
	if diskPath == "" {
		diskPath = "."
	}
	freeFn := s.freeDisk
	if freeFn == nil {
		freeFn = storage.FreeBytes
	}
	free, err := freeFn(diskPath)
	if err != nil {
		s.log.Warn("scheduler: summary disk", "err", err)
		sum.DiskFreeOK = false
	} else {
		sum.DiskFreeBytes = free
		sum.DiskFreeOK = true
	}

	var claude string
	err = s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, settingClaudeLimit).Scan(&claude)
	switch {
	case err == sql.ErrNoRows:
		sum.ClaudeLimitState = "ok"
	case err != nil:
		return sum, fmt.Errorf("scheduler: summary claude limit: %w", err)
	default:
		if strings.TrimSpace(claude) == "" {
			claude = "ok"
		}
		sum.ClaudeLimitState = claude
	}
	return sum, nil
}

// FormatDailySummary renders DESIGN §2 daily summary text for Telegram.
func FormatDailySummary(sum DailySummary) string {
	var b strings.Builder
	b.WriteString("Daily summary\n")
	b.WriteString(fmt.Sprintf("%s\n\n", sum.GeneratedAt.UTC().Format(time.RFC3339)))

	b.WriteString("Published (24h)\n")
	if len(sum.PublishedByChannel) == 0 {
		b.WriteString("• (none)\n")
	} else {
		channels := make([]string, 0, len(sum.PublishedByChannel))
		for ch := range sum.PublishedByChannel {
			channels = append(channels, ch)
		}
		sort.Strings(channels)
		for _, ch := range channels {
			b.WriteString(fmt.Sprintf("• %s: %d\n", ch, sum.PublishedByChannel[ch]))
		}
	}

	b.WriteString(fmt.Sprintf("\nPending approvals: %d\n", sum.PendingApprovals))
	b.WriteString(fmt.Sprintf("Failures (24h): %d\n", sum.Failures))
	b.WriteString(fmt.Sprintf("Best performer (24h): %s\n", sum.BestPerformer))

	b.WriteString("\nFree-tier quota\n")
	if len(sum.QuotaLines) == 0 {
		b.WriteString("• (none recorded)\n")
	} else {
		for _, line := range sum.QuotaLines {
			b.WriteString("• " + line + "\n")
		}
	}

	if sum.DiskFreeOK {
		b.WriteString(fmt.Sprintf("\nDisk free: %s\n", formatBytes(sum.DiskFreeBytes)))
	} else {
		b.WriteString("\nDisk free: (unavailable)\n")
	}
	b.WriteString(fmt.Sprintf("Claude limit: %s\n", sum.ClaudeLimitState))
	return b.String()
}

func formatBytes(n uint64) string {
	const (
		kib = 1024
		mib = kib * 1024
		gib = mib * 1024
	)
	switch {
	case n >= gib:
		return fmt.Sprintf("%.1f GiB", float64(n)/float64(gib))
	case n >= mib:
		return fmt.Sprintf("%.1f MiB", float64(n)/float64(mib))
	case n >= kib:
		return fmt.Sprintf("%.1f KiB", float64(n)/float64(kib))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
