package telegram

import (
	"context"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// completePIN checks the user-supplied PIN against settings.pin_hash (bcrypt)
// and, on success, resumes the pending target. The raw PIN is never logged.
func (b *Bot) completePIN(ctx context.Context, chatID int64, target, pin string) error {
	b.mu.Lock()
	delete(b.awaitingPIN, chatID)
	b.mu.Unlock()

	hash, err := b.getPINHash(ctx)
	if err != nil {
		return err
	}
	if hash == "" {
		return b.reply(ctx, chatID, "PIN not configured. Run mayank2 set-pin first.")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(pin)); err != nil {
		_ = b.emitEvent(ctx, "user", "telegram.pin_failed", "", "resume PIN rejected", "")
		return b.reply(ctx, chatID, "Wrong PIN. Resume cancelled. Send /resume again to retry.")
	}
	if err := b.applyPause(ctx, target, false); err != nil {
		return b.reply(ctx, chatID, "PIN ok but resume failed: "+err.Error())
	}
	_ = b.emitEvent(ctx, "user", "telegram.resumed", target, "resumed after PIN", "")
	return b.reply(ctx, chatID, "Resumed: "+target)
}

// SetPINHash stores a bcrypt hash in settings (for tests / set-pin wiring).
// Callers must pass an already-hashed value — never a raw PIN.
func (b *Bot) SetPINHash(ctx context.Context, bcryptHash string) error {
	if bcryptHash == "" {
		return fmt.Errorf("telegram: empty pin hash")
	}
	return b.setSetting(ctx, settingPINHash, bcryptHash)
}

// HashPIN is a test/helper that bcrypt-hashes a PIN. Production set-pin
// commands should use this (or equivalent) and only persist the hash.
func HashPIN(pin string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pin), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("telegram: hash pin: %w", err)
	}
	return string(h), nil
}
