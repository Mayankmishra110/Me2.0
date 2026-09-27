package telegram

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"mayank2/internal/queue"
)

// dispatchCommand routes a slash command. No command runs a shell or
// executes arbitrary code — only fixed handlers over DB/queue/Bot API.
func (b *Bot) dispatchCommand(ctx context.Context, chatID int64, text string) error {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return nil
	}
	cmd := strings.ToLower(fields[0])
	// Strip @BotName suffix Telegram may append in groups.
	if i := strings.IndexByte(cmd, '@'); i >= 0 {
		cmd = cmd[:i]
	}
	args := fields[1:]

	switch cmd {
	case "/help":
		return b.cmdHelp(ctx, chatID)
	case "/status":
		return b.cmdStatus(ctx, chatID)
	case "/today":
		return b.cmdToday(ctx, chatID)
	case "/queue":
		return b.cmdQueue(ctx, chatID)
	case "/pause":
		return b.cmdPause(ctx, chatID, args)
	case "/resume":
		return b.cmdResume(ctx, chatID, args)
	case "/topic":
		return b.cmdTopic(ctx, chatID, args)
	case "/blog":
		return b.cmdBlog(ctx, chatID, args)
	default:
		return b.reply(ctx, chatID, "Unknown command. Try /help.")
	}
}

func (b *Bot) cmdHelp(ctx context.Context, chatID int64) error {
	const help = `Commands:
/status — health snapshot
/today — published vs target today
/queue — pending approvals
/pause <all|heavy|light|net|agent> — asks for PIN
/resume <all|heavy|light|net|agent> — asks for PIN
/topic <text or URL> [channel]
/blog <topic>
/help`
	return b.reply(ctx, chatID, help)
}

func (b *Bot) cmdStatus(ctx context.Context, chatID int64) error {
	var queued, running, failed, dead int
	_ = b.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE status='queued'`).Scan(&queued)
	_ = b.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE status='running'`).Scan(&running)
	_ = b.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE status='failed'`).Scan(&failed)
	_ = b.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE status='dead'`).Scan(&dead)

	var pending int
	_ = b.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM approvals WHERE status='pending'`).Scan(&pending)

	pauseAll := b.settingIsOne(ctx, "pause:all")
	state := "Running"
	if pauseAll {
		state = "Paused"
	}

	msg := fmt.Sprintf("Status: %s\nJobs queued=%d running=%d failed=%d dead=%d\nPending approvals: %d",
		state, queued, running, failed, dead, pending)
	return b.reply(ctx, chatID, msg)
}

func (b *Bot) cmdToday(ctx context.Context, chatID int64) error {
	day := time.Now().UTC().Format("2006-01-02")
	var published int
	err := b.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM publications
WHERE status='published' AND published_at IS NOT NULL AND substr(published_at, 1, 10)=?`, day).Scan(&published)
	if err != nil {
		return fmt.Errorf("telegram: /today count: %w", err)
	}
	// Targets from CONTENT_STRATEGY / DESIGN: 8 Shorts + 2 long at full speed.
	msg := fmt.Sprintf("Today (UTC %s):\nPublished: %d\nTarget: 8 Shorts + 2 long (ramp-up may lower this)", day, published)
	return b.reply(ctx, chatID, msg)
}

func (b *Bot) cmdQueue(ctx context.Context, chatID int64) error {
	rows, err := b.db.QueryContext(ctx, `
SELECT id, kind, summary FROM approvals WHERE status='pending' ORDER BY id LIMIT 20`)
	if err != nil {
		return fmt.Errorf("telegram: /queue: %w", err)
	}
	defer rows.Close()

	var lines []string
	for rows.Next() {
		var id, kind, summary string
		if err := rows.Scan(&id, &kind, &summary); err != nil {
			return fmt.Errorf("telegram: /queue scan: %w", err)
		}
		if summary == "" {
			summary = "(no summary)"
		}
		lines = append(lines, fmt.Sprintf("• %s [%s] %s", id, kind, truncate(summary, 80)))
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(lines) == 0 {
		return b.reply(ctx, chatID, "No pending approvals.")
	}
	return b.reply(ctx, chatID, "Pending approvals:\n"+strings.Join(lines, "\n"))
}

func (b *Bot) cmdPause(ctx context.Context, chatID int64, args []string) error {
	return b.beginPINGate(ctx, chatID, "pause", args)
}

func (b *Bot) cmdResume(ctx context.Context, chatID int64, args []string) error {
	return b.beginPINGate(ctx, chatID, "resume", args)
}

// beginPINGate validates the target then prompts for PIN before applying
// pause/resume (ARCHITECTURE §7: bulk actions need a PIN).
func (b *Bot) beginPINGate(ctx context.Context, chatID int64, op string, args []string) error {
	target := "all"
	if len(args) > 0 {
		target = strings.ToLower(args[0])
	}
	if _, err := parsePauseTarget(target); err != nil {
		return b.reply(ctx, chatID, err.Error())
	}
	b.mu.Lock()
	b.awaitingPIN[chatID] = op + ":" + target
	b.mu.Unlock()
	return b.reply(ctx, chatID, "Enter PIN to "+op+" "+target+":")
}

func (b *Bot) applyPause(ctx context.Context, target string, pause bool) error {
	kind, err := parsePauseTarget(target)
	if err != nil {
		return err
	}
	if b.q == nil && (kind == "all" || kind == "heavy" || kind == "light" || kind == "net") {
		return fmt.Errorf("queue not wired")
	}
	switch kind {
	case "all":
		if pause {
			return b.q.PauseAll(ctx)
		}
		return b.q.ResumeAll(ctx)
	case "heavy", "light", "net":
		res := queue.Resource(kind)
		if pause {
			return b.q.PauseResource(ctx, res)
		}
		return b.q.ResumeResource(ctx, res)
	default: // agent name
		key := settingAgentPause + kind
		if pause {
			return b.setSetting(ctx, key, "1")
		}
		return b.deleteSetting(ctx, key)
	}
}

// parsePauseTarget returns "all"|"heavy"|"light"|"net"|agentName.
func parsePauseTarget(target string) (string, error) {
	target = strings.TrimSpace(strings.ToLower(target))
	if target == "" {
		return "", fmt.Errorf("usage: /pause|/resume <all|heavy|light|net|agent>")
	}
	switch target {
	case "all", "heavy", "light", "net":
		return target, nil
	default:
		// Agent names: alphanumeric + dash/underscore only — never a shell string.
		for _, r := range target {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
				continue
			}
			return "", fmt.Errorf("invalid agent name %q", target)
		}
		return target, nil
	}
}

func (b *Bot) cmdTopic(ctx context.Context, chatID int64, args []string) error {
	if len(args) == 0 {
		return b.reply(ctx, chatID, "Usage: /topic <text or URL> [channel]")
	}
	channelID := ""
	titleParts := args
	// Last arg is channel if it matches an existing channel id.
	if len(args) >= 2 {
		cand := args[len(args)-1]
		var exists string
		err := b.db.QueryRowContext(ctx, `SELECT id FROM channels WHERE id=?`, cand).Scan(&exists)
		if err == nil {
			channelID = exists
			titleParts = args[:len(args)-1]
		} else if err != sql.ErrNoRows {
			return fmt.Errorf("telegram: /topic channel lookup: %w", err)
		}
	}
	title := strings.TrimSpace(strings.Join(titleParts, " "))
	if title == "" {
		return b.reply(ctx, chatID, "Usage: /topic <text or URL> [channel]")
	}
	if channelID == "" {
		err := b.db.QueryRowContext(ctx, `SELECT id FROM channels WHERE status='active' ORDER BY id LIMIT 1`).Scan(&channelID)
		if err == sql.ErrNoRows {
			return b.reply(ctx, chatID, "No channels configured yet — add one before /topic.")
		}
		if err != nil {
			return fmt.Errorf("telegram: /topic default channel: %w", err)
		}
	}
	id, err := newID()
	if err != nil {
		return err
	}
	sourceURL := ""
	if strings.HasPrefix(title, "http://") || strings.HasPrefix(title, "https://") {
		sourceURL = title
	}
	at := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = b.db.ExecContext(ctx, `
INSERT INTO topics (id, channel_id, title, source, source_url, score, signals, status, created_at)
VALUES (?, ?, ?, 'manual', ?, 0, '{}', 'new', ?)`,
		id, channelID, title, nullIfEmpty(sourceURL), at,
	)
	if err != nil {
		return fmt.Errorf("telegram: insert topic: %w", err)
	}
	_ = b.emitEvent(ctx, "user", "telegram.topic", id, "manual topic added", "")
	return b.reply(ctx, chatID, fmt.Sprintf("Topic saved for %s: %s", channelID, truncate(title, 120)))
}

func (b *Bot) cmdBlog(ctx context.Context, chatID int64, args []string) error {
	if len(args) == 0 {
		return b.reply(ctx, chatID, "Usage: /blog <topic>")
	}
	topic := strings.TrimSpace(strings.Join(args, " "))
	_ = b.emitEvent(ctx, "user", "telegram.blog", "", topic, "")
	return b.reply(ctx, chatID, "Blog topic queued: "+truncate(topic, 120))
}

func (b *Bot) settingIsOne(ctx context.Context, key string) bool {
	var v string
	err := b.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	return err == nil && v == "1"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
