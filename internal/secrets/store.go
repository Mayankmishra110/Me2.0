package secrets

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// Store reads and writes DPAPI-encrypted OAuth tokens in oauth_tokens.
type Store struct {
	db        *sql.DB
	protect   func([]byte) ([]byte, error)
	unprotect func([]byte) ([]byte, error)
}

// NewStore returns a Store that uses Protect/Unprotect (DPAPI on Windows).
func NewStore(db *sql.DB) *Store {
	return &Store{
		db:        db,
		protect:   Protect,
		unprotect: Unprotect,
	}
}

// newStoreWithCrypter is for tests that inject a non-DPAPI round-trip.
func newStoreWithCrypter(db *sql.DB, protect, unprotect func([]byte) ([]byte, error)) *Store {
	return &Store{db: db, protect: protect, unprotect: unprotect}
}

// Put encrypts tok and upserts it for (platform, account). Never logs tok.
func (s *Store) Put(ctx context.Context, platform, account string, tok *oauth2.Token) error {
	if platform == "" || account == "" {
		return fmt.Errorf("secrets.Put: platform and account are required")
	}
	if tok == nil {
		return fmt.Errorf("secrets.Put: token is nil for %s/%s", platform, account)
	}
	plain, err := json.Marshal(tok)
	if err != nil {
		return fmt.Errorf("secrets.Put: marshal token for %s/%s: %w", platform, account, err)
	}
	blob, err := s.protect(plain)
	if err != nil {
		return fmt.Errorf("secrets.Put: protect for %s/%s: %w", platform, account, err)
	}
	var expires sql.NullString
	if !tok.Expiry.IsZero() {
		expires = sql.NullString{String: tok.Expiry.UTC().Format(time.RFC3339Nano), Valid: true}
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO oauth_tokens (platform, account, encrypted_blob, expires_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(platform, account) DO UPDATE SET
			encrypted_blob = excluded.encrypted_blob,
			expires_at = excluded.expires_at
	`, platform, account, blob, expires)
	if err != nil {
		return fmt.Errorf("secrets.Put: upsert %s/%s: %w", platform, account, err)
	}
	return nil
}

// ErrNotFound means no row exists for the platform/account pair.
var ErrNotFound = errors.New("secrets: oauth token not found")

// Get decrypts and returns the stored token for (platform, account).
func (s *Store) Get(ctx context.Context, platform, account string) (*oauth2.Token, error) {
	if platform == "" || account == "" {
		return nil, fmt.Errorf("secrets.Get: platform and account are required")
	}
	var blob []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT encrypted_blob FROM oauth_tokens WHERE platform = ? AND account = ?
	`, platform, account).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s/%s", ErrNotFound, platform, account)
	}
	if err != nil {
		return nil, fmt.Errorf("secrets.Get: query %s/%s: %w", platform, account, err)
	}
	plain, err := s.unprotect(blob)
	if err != nil {
		return nil, fmt.Errorf("secrets.Get: unprotect %s/%s: %w", platform, account, err)
	}
	var tok oauth2.Token
	if err := json.Unmarshal(plain, &tok); err != nil {
		return nil, fmt.Errorf("secrets.Get: unmarshal %s/%s: %w", platform, account, err)
	}
	return &tok, nil
}

// TokenSource returns a source that refreshes via cfg and persists new tokens.
// Callers must not log the returned token.
func (s *Store) TokenSource(ctx context.Context, platform, account string, cfg *oauth2.Config) (oauth2.TokenSource, error) {
	if cfg == nil {
		return nil, fmt.Errorf("secrets.TokenSource: config is nil for %s/%s", platform, account)
	}
	tok, err := s.Get(ctx, platform, account)
	if err != nil {
		return nil, err
	}
	base := cfg.TokenSource(ctx, tok)
	return &persistingSource{
		ctx:      ctx,
		platform: platform,
		account:  account,
		store:    s,
		inner:    base,
	}, nil
}

// persistingSource wraps an oauth2 TokenSource and writes refreshed tokens back.
type persistingSource struct {
	ctx      context.Context
	platform string
	account  string
	store    *Store
	inner    oauth2.TokenSource

	mu     sync.Mutex
	lastAT string // last AccessToken we persisted (detect refresh without logging it)
}

func (p *persistingSource) Token() (*oauth2.Token, error) {
	tok, err := p.inner.Token()
	if err != nil {
		return nil, fmt.Errorf("secrets.TokenSource: refresh %s/%s: %w", p.platform, p.account, err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if tok.AccessToken != p.lastAT {
		if err := p.store.Put(p.ctx, p.platform, p.account, tok); err != nil {
			return nil, err
		}
		p.lastAT = tok.AccessToken
	}
	return tok, nil
}
