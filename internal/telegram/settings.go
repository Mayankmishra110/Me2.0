package telegram

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

const (
	settingOffset     = "telegram:offset"
	settingPINHash    = "pin_hash"
	settingAgentPause = "pause:agent:"
)

func (b *Bot) getOffset(ctx context.Context) (int64, error) {
	var raw string
	err := b.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, settingOffset).Scan(&raw)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("telegram: read offset: %w", err)
	}
	var offset int64
	if _, err := fmt.Sscan(raw, &offset); err != nil {
		return 0, fmt.Errorf("telegram: parse offset %q: %w", raw, err)
	}
	return offset, nil
}

func (b *Bot) setOffset(ctx context.Context, offset int64) error {
	_, err := b.db.ExecContext(ctx, `
INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		settingOffset, fmt.Sprintf("%d", offset),
	)
	if err != nil {
		return fmt.Errorf("telegram: write offset: %w", err)
	}
	return nil
}

func (b *Bot) getPINHash(ctx context.Context) (string, error) {
	var hash string
	err := b.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, settingPINHash).Scan(&hash)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("telegram: read pin_hash: %w", err)
	}
	return hash, nil
}

func (b *Bot) setSetting(ctx context.Context, key, value string) error {
	_, err := b.db.ExecContext(ctx, `
INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		key, value,
	)
	if err != nil {
		return fmt.Errorf("telegram: set setting %s: %w", key, err)
	}
	return nil
}

func (b *Bot) deleteSetting(ctx context.Context, key string) error {
	_, err := b.db.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, key)
	if err != nil {
		return fmt.Errorf("telegram: delete setting %s: %w", key, err)
	}
	return nil
}

func (b *Bot) emitEvent(ctx context.Context, actor, kind, ref, message string, data string) error {
	id, err := newID()
	if err != nil {
		return err
	}
	at := time.Now().UTC().Format(time.RFC3339Nano)
	var dataArg any
	if data == "" {
		dataArg = nil
	} else {
		dataArg = data
	}
	_, err = b.db.ExecContext(ctx, `
INSERT INTO events (id, at, actor, kind, ref, message, data) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, at, actor, kind, nullIfEmpty(ref), message, dataArg,
	)
	if err != nil {
		return fmt.Errorf("telegram: insert event %s: %w", kind, err)
	}
	return nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// newID returns a 26-char Crockford-ish random id (enough uniqueness for
// events/topics inside this process; not a full ULID).
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("telegram: rand id: %w", err)
	}
	return hex.EncodeToString(b[:])[:26], nil
}
