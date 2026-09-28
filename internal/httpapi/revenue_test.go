package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"mayank2/internal/events"
)

func TestRevenueListAndCreate(t *testing.T) {
	sqlDB := testDB(t)
	bus := events.New(sqlDB)
	h := testServer(t, sqlDB, bus).Handler()
	cookie := login(t, h)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/revenue", nil)
	req.AddCookie(cookie)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rr.Code, rr.Body.String())
	}
	var list struct {
		Entries []any `json:"entries"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Entries) != 0 {
		t.Fatalf("want empty, got %d", len(list.Entries))
	}

	rr = httptest.NewRecorder()
	body, _ := json.Marshal(map[string]any{
		"line": "tips", "source": "x", "amount": 1, "date": "2026-09-01",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/revenue", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad line status=%d", rr.Code)
	}

	rr = httptest.NewRecorder()
	body, _ = json.Marshal(map[string]any{
		"line": "ads", "source": "x", "date": "2026-09-01",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/revenue", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing amount status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	body, _ = json.Marshal(map[string]any{
		"line": "affiliate", "source": "amazon", "amount": 12.5, "date": "2026-09-15", "note": "sept",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/revenue", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/revenue?line=affiliate", nil)
	req.AddCookie(cookie)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("filter status=%d", rr.Code)
	}
	var listed struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Entries) != 1 {
		t.Fatalf("listed=%d body=%s", len(listed.Entries), rr.Body.String())
	}
}
