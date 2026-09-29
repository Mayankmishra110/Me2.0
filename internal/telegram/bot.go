// Package telegram is the phone-control Bot API client: allowlisted long-poll,
// commands, approval buttons, and PIN-gated resume (docs/DESIGN.md §2,
// docs/ARCHITECTURE.md §7). Official HTTPS Bot API only — no SDK, no shell.
package telegram

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"mayank2/internal/content"
	"mayank2/internal/queue"
)

// ErrDisabled means TELEGRAM_BOT_TOKEN is unset — the daemon stays up without
// the bot (CONTEXT D24).
var ErrDisabled = errors.New("telegram: disabled (TELEGRAM_BOT_TOKEN empty)")

// Bot is the Telegram control surface for Mayank.
type Bot struct {
	client    *Client
	db        *sql.DB
	q         *queue.Queue
	approvals *content.ApprovalService
	log       *slog.Logger

	userID int64
	chatID int64

	mu           sync.Mutex
	awaitingPIN  map[int64]string // chatID → "pause|resume:<target>" (ARCH §7 bulk PIN)
	awaitingRedo map[int64]string // chatID → approval id waiting for a note
	dataRoot     string           // absolute path; preview uploads must stay under this
}

// Config holds construction inputs. Token/IDs normally come from env.
type Config struct {
	Token    string
	UserID   int64
	ChatID   int64
	DataRoot string // media/preview confinement root (config data_dir); empty → refuse file uploads
	DB       *sql.DB
	Queue    *queue.Queue
	// Approvals applies approve/reject/redo (schedules publications on approve).
	// Required for decisions; nil fails closed.
	Approvals  *content.ApprovalService
	Log        *slog.Logger
	ClientOpts []ClientOption
}

// NewFromEnv builds a Bot from TELEGRAM_BOT_TOKEN / TELEGRAM_USER_ID /
// TELEGRAM_CHAT_ID. Returns ErrDisabled when the token is empty.
func NewFromEnv(database *sql.DB, q *queue.Queue, log *slog.Logger, clientOpts ...ClientOption) (*Bot, error) {
	token := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if token == "" {
		return nil, ErrDisabled
	}
	userID, err := parseIntEnv("TELEGRAM_USER_ID")
	if err != nil {
		return nil, err
	}
	chatID, err := parseIntEnv("TELEGRAM_CHAT_ID")
	if err != nil {
		return nil, err
	}
	return New(Config{
		Token:      token,
		UserID:     userID,
		ChatID:     chatID,
		DB:         database,
		Queue:      q,
		Log:        log,
		ClientOpts: clientOpts,
	})
}

// New constructs a Bot. Token, UserID, ChatID, and DB are required.
func New(cfg Config) (*Bot, error) {
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, ErrDisabled
	}
	if cfg.UserID == 0 {
		return nil, fmt.Errorf("telegram: UserID is required")
	}
	if cfg.ChatID == 0 {
		return nil, fmt.Errorf("telegram: ChatID is required")
	}
	if cfg.DB == nil {
		return nil, fmt.Errorf("telegram: DB is required")
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	dataRoot := strings.TrimSpace(cfg.DataRoot)
	if dataRoot != "" {
		if abs, err := filepath.Abs(dataRoot); err == nil {
			dataRoot = abs
		}
	}
	opts := append([]ClientOption{}, cfg.ClientOpts...)
	opts = append(opts, WithDataRoot(dataRoot))
	return &Bot{
		client:       NewClient(cfg.Token, opts...),
		db:           cfg.DB,
		q:            cfg.Queue,
		approvals:    cfg.Approvals,
		log:          cfg.Log,
		userID:       cfg.UserID,
		chatID:       cfg.ChatID,
		dataRoot:     dataRoot,
		awaitingPIN:  make(map[int64]string),
		awaitingRedo: make(map[int64]string),
	}, nil
}

func parseIntEnv(key string) (int64, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0, fmt.Errorf("telegram: %s is required when TELEGRAM_BOT_TOKEN is set", key)
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("telegram: parse %s: %w", key, err)
	}
	if n == 0 {
		return 0, fmt.Errorf("telegram: %s must be non-zero", key)
	}
	return n, nil
}

// RegisterHandlers wires the approval.request job into the queue (resource
// class net — outbound Bot API). Idempotent-safe: re-sending an already
// decided approval is a no-op; a pending approval re-sends the preview.
func (b *Bot) RegisterHandlers(q *queue.Queue) {
	if q == nil {
		return
	}
	b.q = q
	q.Register("approval.request", queue.ResourceNet, 5, b.handleApprovalRequest)
}

// SendMessage sends a plain-text message to chatID over the same raw Bot
// API client Bot itself uses for previews/replies (one token, one HTTP
// client, one place any future retry/rate-limit behavior would live).
//
// M2-121 (CONTEXT §5 open question 6, `internal/blog/medium.go`'s
// TelegramSender): this is the narrow accessor this ticket adds instead of
// exposing Bot's unexported *Client wholesale. A hypothetical `Bot.Client()`
// would also hand out Client.GetUpdates, SendPhoto/SendVideo,
// EditMessageText/Caption and AnswerCallbackQuery — in particular
// GetUpdates is unsafe to expose: Bot.Run's own long-poll loop is the only
// thing that should ever advance the stored offset (settings
// telegram:offset), and the allowlist check (Bot.handleMessage's allowed())
// lives entirely in Bot, not in Client. A second, uncoordinated GetUpdates
// caller could steal/skip updates out from under Bot.Run, which would look
// like inbound approval taps or commands silently going missing — a real
// safety regression for a control surface CLAUDE.md/D5 depend on. Exposing
// only SendMessage (which every caller needs the same way: fire a message,
// no polling, no state) avoids that risk entirely while still centralizing
// on the one bot token / HTTP client, per CLAUDE.md's "Telegram bot" being
// the one place Telegram access should live.
func (b *Bot) SendMessage(ctx context.Context, chatID int64, text string, markup *InlineKeyboardMarkup) (int64, error) {
	if b == nil || b.client == nil {
		return 0, fmt.Errorf("telegram: SendMessage: bot not configured")
	}
	return b.client.SendMessage(ctx, chatID, text, markup)
}

// ChatID returns Mayank's allowlisted chat id (TELEGRAM_CHAT_ID) this Bot
// was constructed with, so a caller that needs to address him directly
// (e.g. blog.medium, M2-121) doesn't have to re-read the env var itself.
func (b *Bot) ChatID() int64 {
	if b == nil {
		return 0
	}
	return b.chatID
}

// Run long-polls getUpdates until ctx is cancelled. Offset is stored in
// settings under telegram:offset.
func (b *Bot) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		offset, err := b.getOffset(ctx)
		if err != nil {
			return err
		}
		updates, err := b.client.GetUpdates(ctx, offset, 30)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			b.log.Warn("telegram: getUpdates failed", "error", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
				continue
			}
		}
		for _, u := range updates {
			if err := b.handleUpdate(ctx, u); err != nil {
				b.log.Warn("telegram: handle update", "update_id", u.UpdateID, "error", err)
			}
			if err := b.setOffset(ctx, u.UpdateID+1); err != nil {
				return err
			}
		}
	}
}

// HandleUpdate is exported for tests to drive a single update without polling.
func (b *Bot) HandleUpdate(ctx context.Context, u Update) error {
	return b.handleUpdate(ctx, u)
}

func (b *Bot) handleUpdate(ctx context.Context, u Update) error {
	switch {
	case u.CallbackQuery != nil:
		return b.handleCallback(ctx, u.CallbackQuery)
	case u.Message != nil:
		return b.handleMessage(ctx, u.Message)
	default:
		return nil
	}
}

func (b *Bot) handleMessage(ctx context.Context, msg *Message) error {
	if msg.From == nil {
		return nil
	}
	if !b.allowed(msg.From.ID, msg.Chat.ID) {
		return b.rejectStranger(ctx, msg.From.ID, msg.Chat.ID, "message")
	}

	text := strings.TrimSpace(msg.Text)

	// Pending interactive flows take the next non-command text.
	if text != "" && !strings.HasPrefix(text, "/") {
		b.mu.Lock()
		pinTarget, pinOK := b.awaitingPIN[msg.Chat.ID]
		redoID, redoOK := b.awaitingRedo[msg.Chat.ID]
		b.mu.Unlock()
		if pinOK {
			return b.completePIN(ctx, msg.Chat.ID, pinTarget, text)
		}
		if redoOK {
			return b.completeRedoNote(ctx, msg.Chat.ID, redoID, text)
		}
	}

	if text == "" || !strings.HasPrefix(text, "/") {
		return nil
	}
	return b.dispatchCommand(ctx, msg.Chat.ID, text)
}

func (b *Bot) allowed(fromID, chatID int64) bool {
	return fromID == b.userID && chatID == b.chatID
}

func (b *Bot) rejectStranger(ctx context.Context, fromID, chatID int64, kind string) error {
	b.log.Info("telegram: dropped non-allowlisted update",
		"kind", kind, "from_id", fromID, "chat_id", chatID)
	data := fmt.Sprintf(`{"from_id":%d,"chat_id":%d,"kind":%q}`, fromID, chatID, kind)
	if err := b.emitEvent(ctx, "system", "telegram.ignored", "",
		"dropped telegram update outside allowlist", data); err != nil {
		return err
	}
	return nil
}

func (b *Bot) reply(ctx context.Context, chatID int64, text string) error {
	_, err := b.client.SendMessage(ctx, chatID, text, nil)
	return err
}
