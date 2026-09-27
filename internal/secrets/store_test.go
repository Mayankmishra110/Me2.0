package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"mayank2/internal/db"
)

func passThroughProtect(b []byte) ([]byte, error) {
	out := make([]byte, len(b))
	copy(out, b)
	return out, nil
}

func testStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	sqlDB, err := db.Open(ctx, filepath.Join(t.TempDir(), "secrets.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := db.Migrate(ctx, sqlDB); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	return newStoreWithCrypter(sqlDB, passThroughProtect, passThroughProtect)
}

func TestStorePutGet(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	tok := &oauth2.Token{
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		TokenType:    "Bearer",
		Expiry:       time.Now().UTC().Add(time.Hour).Truncate(time.Second),
	}
	if err := s.Put(ctx, "youtube", "money-en", tok); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Get(ctx, "youtube", "money-en")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AccessToken != tok.AccessToken || got.RefreshToken != tok.RefreshToken {
		t.Fatalf("token mismatch: access=%q refresh=%q", got.AccessToken, got.RefreshToken)
	}
	if !got.Expiry.Equal(tok.Expiry) {
		t.Fatalf("expiry: got %v want %v", got.Expiry, tok.Expiry)
	}
}

func TestStoreGetNotFound(t *testing.T) {
	_, err := testStore(t).Get(context.Background(), "youtube", "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v want ErrNotFound", err)
	}
}

func TestTokenSourceRefreshPersists(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)

	var refreshCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if !strings.Contains(string(body), "grant_type=refresh_token") {
			http.Error(w, "want refresh_token grant", http.StatusBadRequest)
			return
		}
		refreshCalls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-refreshed",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"refresh_token": "refresh-1",
		})
	}))
	t.Cleanup(srv.Close)

	expired := &oauth2.Token{
		AccessToken:  "access-old",
		RefreshToken: "refresh-1",
		TokenType:    "Bearer",
		Expiry:       time.Now().UTC().Add(-time.Minute),
	}
	if err := s.Put(ctx, "youtube", "ch1", expired); err != nil {
		t.Fatalf("Put: %v", err)
	}

	cfg := &oauth2.Config{
		ClientID:     "cid",
		ClientSecret: "csec",
		Endpoint: oauth2.Endpoint{
			AuthURL:   srv.URL + "/auth",
			TokenURL:  srv.URL + "/token",
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}
	ts, err := s.TokenSource(ctx, "youtube", "ch1", cfg)
	if err != nil {
		t.Fatalf("TokenSource: %v", err)
	}
	tok, err := ts.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok.AccessToken != "access-refreshed" {
		t.Fatalf("access_token=%q want access-refreshed", tok.AccessToken)
	}
	if refreshCalls != 1 {
		t.Fatalf("refreshCalls=%d want 1", refreshCalls)
	}

	stored, err := s.Get(ctx, "youtube", "ch1")
	if err != nil {
		t.Fatalf("Get after refresh: %v", err)
	}
	if stored.AccessToken != "access-refreshed" {
		t.Fatalf("stored access=%q want access-refreshed", stored.AccessToken)
	}
}

func TestLookupPlatform(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"youtube", "youtube", true},
		{"Google", "youtube", true},
		{"instagram", "meta", true},
		{"twitter", "x", true},
		{"pinterest", "pinterest", true},
		{"linkedin", "linkedin", true},
		{"nope", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		p, err := LookupPlatform(tc.in)
		if tc.ok {
			if err != nil {
				t.Fatalf("LookupPlatform(%q): %v", tc.in, err)
			}
			if p.Name != tc.want {
				t.Fatalf("LookupPlatform(%q)=%q want %q", tc.in, p.Name, tc.want)
			}
		} else if err == nil {
			t.Fatalf("LookupPlatform(%q): expected error", tc.in)
		}
	}
}

func TestPlatformPKCEFlags(t *testing.T) {
	yt, _ := LookupPlatform("youtube")
	if !yt.UsePKCE {
		t.Fatal("youtube should use PKCE")
	}
	meta, _ := LookupPlatform("meta")
	if meta.UsePKCE {
		t.Fatal("meta should not use PKCE")
	}
}

func TestOAuthConfigMissingEnv(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "")
	t.Setenv("GOOGLE_CLIENT_SECRET", "")
	p, err := LookupPlatform("youtube")
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.OAuthConfig("http://127.0.0.1:9/callback")
	if err == nil {
		t.Fatal("expected missing env error")
	}
}

func TestOAuthConfigOK(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "sec")
	p, err := LookupPlatform("youtube")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := p.OAuthConfig("http://127.0.0.1:9/callback")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientID != "id" || cfg.RedirectURL != "http://127.0.0.1:9/callback" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if len(cfg.Scopes) == 0 {
		t.Fatal("expected scopes")
	}
}
