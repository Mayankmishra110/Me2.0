package telegram

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// completePIN checks the user-supplied PIN against settings.pin_hash (bcrypt)
// and, on success, applies the pending pause or resume. The raw PIN is never logged.
// pending is "pause:<target>" or "resume:<target>" from beginPINGate.
func (b *Bot) completePIN(ctx context.Context, chatID int64, pending, pin string) error {
	b.mu.Lock()
	delete(b.awaitingPIN, chatID)
	b.mu.Unlock()

	op, target, ok := parsePINPending(pending)
	if !ok {
		return b.reply(ctx, chatID, "PIN flow cancelled. Send /pause or /resume again.")
	}

	hash, err := b.getPINHash(ctx)
	if err != nil {
		return err
	}
	if hash == "" {
		return b.reply(ctx, chatID, "PIN not configured. Run mayank2 set-pin first.")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(pin)); err != nil {
		_ = b.emitEvent(ctx, "user", "telegram.pin_failed", "", op+" PIN rejected", "")
		return b.reply(ctx, chatID, "Wrong PIN. "+strings.ToUpper(op[:1])+op[1:]+" cancelled. Send /"+op+" again to retry.")
	}
	pause := op == "pause"
	if err := b.applyPause(ctx, target, pause); err != nil {
		return b.reply(ctx, chatID, "PIN ok but "+op+" failed: "+err.Error())
	}
	kind := "telegram.resumed"
	label := "Resumed"
	if pause {
		kind = "telegram.paused"
		label = "Paused"
	}
	_ = b.emitEvent(ctx, "user", kind, target, op+" after PIN", "")
	return b.reply(ctx, chatID, label+": "+target)
}

// parsePINPending splits "pause:all" / "resume:heavy" from awaitingPIN.
func parsePINPending(pending string) (op, target string, ok bool) {
	op, target, found := strings.Cut(pending, ":")
	if !found || target == "" {
		return "", "", false
	}
	switch op {
	case "pause", "resume":
		return op, target, true
	default:
		return "", "", false
	}
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
