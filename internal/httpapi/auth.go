package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookieName = "session"
	sessionTTL        = 7 * 24 * time.Hour
	loginWindow       = time.Minute
	loginMaxAttempts  = 10
)

type sessionStore struct {
	mu   sync.Mutex
	byID map[string]time.Time
}

func newSessionStore() *sessionStore {
	return &sessionStore{byID: make(map[string]time.Time)}
}

func (s *sessionStore) create() (string, time.Time, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", time.Time{}, err
	}
	id := hex.EncodeToString(b[:])
	exp := time.Now().UTC().Add(sessionTTL)
	s.mu.Lock()
	s.byID[id] = exp
	s.mu.Unlock()
	return id, exp, nil
}

func (s *sessionStore) valid(id string) bool {
	if id == "" {
		return false
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.byID[id]
	if !ok {
		return false
	}
	if now.After(exp) {
		delete(s.byID, id)
		return false
	}
	return true
}

type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{attempts: make(map[string][]time.Time)}
}

func (l *loginLimiter) allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := now.Add(-loginWindow)
	kept := l.attempts[key][:0]
	for _, t := range l.attempts[key] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	l.attempts[key] = kept
	if len(kept) >= loginMaxAttempts {
		return false
	}
	l.attempts[key] = append(kept, now)
	return true
}

func hashToken(token string) [32]byte {
	return sha256.Sum256([]byte(token))
}

func tokensEqual(want [32]byte, got string) bool {
	gotHash := hashToken(got)
	return subtle.ConstantTimeCompare(want[:], gotHash[:]) == 1
}

type loginRequest struct {
	Token string `json:"token"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.logins.allow(clientKey(r)) {
		writeError(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	var body loginRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	body.Token = strings.TrimSpace(body.Token)
	if body.Token == "" || !tokensEqual(s.tokenHash, body.Token) {
		// Never say which part failed; do not log the token.
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id, _, err := s.sessions.create()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session create failed")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		// Secure left false: dashboard speaks plain HTTP on 127.0.0.1 / Tailscale IP.
		MaxAge: int(sessionTTL.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookieName)
		if err != nil || !s.sessions.valid(c.Value) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
