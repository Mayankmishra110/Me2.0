package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"mayank2/internal/events"
)

// setPINHash seeds settings.pin_hash directly, mirroring what `mayank2
// set-pin` and internal/telegram.SetPINHash write — a real bcrypt hash, not
// a plaintext PIN.
func setPINHash(t *testing.T, sqlDB *sql.DB, pin string) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(pin), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt.GenerateFromPassword: %v", err)
	}
	_, err = sqlDB.ExecContext(context.Background(), `
INSERT INTO settings (key, value) VALUES ('pin_hash', ?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(hash))
	if err != nil {
		t.Fatalf("seed pin_hash: %v", err)
	}
}

func TestCheckPIN_failsClosedWhenNoPINConfigured(t *testing.T) {
	sqlDB := testDB(t)
	bus := events.New(sqlDB)
	s := testServer(t, sqlDB, bus)

	for _, pin := range []string{"", "0000", "anything", "1234"} {
		if err := s.checkPIN(context.Background(), pin); err == nil {
			t.Fatalf("checkPIN(%q) with no pin_hash set: want error (fail closed), got nil", pin)
		}
	}
}

func TestCheckPIN_acceptsCorrectRejectsWrong(t *testing.T) {
	sqlDB := testDB(t)
	bus := events.New(sqlDB)
	s := testServer(t, sqlDB, bus)
	setPINHash(t, sqlDB, "4242")

	if err := s.checkPIN(context.Background(), "4242"); err != nil {
		t.Fatalf("checkPIN with correct pin: %v", err)
	}
	if err := s.checkPIN(context.Background(), "0000"); err == nil {
		t.Fatal("checkPIN with wrong pin: want error, got nil")
	}
	if err := s.checkPIN(context.Background(), ""); err == nil {
		t.Fatal("checkPIN with empty pin: want error, got nil")
	}
}

func TestCheckPIN_storedValueIsNotPlaintext(t *testing.T) {
	sqlDB := testDB(t)
	setPINHash(t, sqlDB, "4242")

	var stored string
	err := sqlDB.QueryRowContext(context.Background(),
		`SELECT value FROM settings WHERE key='pin_hash'`).Scan(&stored)
	if err != nil {
		t.Fatalf("read pin_hash: %v", err)
	}
	if stored == "4242" {
		t.Fatal("pin_hash stored the raw PIN, not a hash")
	}
	if bcrypt.CompareHashAndPassword([]byte(stored), []byte("4242")) != nil {
		t.Fatalf("stored value is not a valid bcrypt hash of the PIN: %q", stored)
	}
}

func TestResume_requiresPIN_failsClosedBeforeSetPin(t *testing.T) {
	sqlDB := testDB(t)
	bus := events.New(sqlDB)
	h := testServer(t, sqlDB, bus).Handler()
	cookie := login(t, h)

	// Pause first so a would-be resume has something to undo.
	rr := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]any{"scope": "all"})
	req := httptest.NewRequest(http.MethodPost, "/api/pause", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("pause status=%d body=%s", rr.Code, rr.Body.String())
	}

	// No pin_hash configured yet: every resume attempt must be refused,
	// including one that previously "worked" (any non-empty string).
	for _, pin := range []string{"0000", "anything"} {
		rr = httptest.NewRecorder()
		body, _ = json.Marshal(map[string]any{"scope": "all", "pin": pin})
		req = httptest.NewRequest(http.MethodPost, "/api/resume", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("resume with pin=%q before set-pin: status=%d body=%s (want 401, fail closed)", pin, rr.Code, rr.Body.String())
		}
	}
}

func TestResume_wrongPINRejected_correctPINResumes(t *testing.T) {
	sqlDB := testDB(t)
	bus := events.New(sqlDB)
	h := testServer(t, sqlDB, bus).Handler()
	cookie := login(t, h)
	setPINHash(t, sqlDB, "1357")

	rr := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]any{"scope": "all"})
	req := httptest.NewRequest(http.MethodPost, "/api/pause", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("pause status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	body, _ = json.Marshal(map[string]any{"scope": "all", "pin": "0000"})
	req = httptest.NewRequest(http.MethodPost, "/api/resume", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("resume with wrong pin: status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	body, _ = json.Marshal(map[string]any{"scope": "all", "pin": "1357"})
	req = httptest.NewRequest(http.MethodPost, "/api/resume", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("resume with correct pin: status=%d body=%s", rr.Code, rr.Body.String())
	}
}
