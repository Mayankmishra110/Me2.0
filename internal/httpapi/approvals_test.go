package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestApprovals_decisionFlow(t *testing.T) {
	sqlDB := testDB(t)
	seedApproval(t, sqlDB, "ap1", "pending")
	h := testServer(t, sqlDB, nil).Handler()
	c := login(t, h)

	// List pending.
	req := httptest.NewRequest(http.MethodGet, "/api/approvals?status=pending", nil)
	req.AddCookie(c)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status %d body %s", rr.Code, rr.Body.String())
	}
	var list struct {
		Approvals []map[string]any `json:"approvals"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("list json: %v", err)
	}
	if len(list.Approvals) != 1 || list.Approvals[0]["id"] != "ap1" {
		t.Fatalf("list=%v", list.Approvals)
	}

	// Detail includes compliance.
	req = httptest.NewRequest(http.MethodGet, "/api/approvals/ap1", nil)
	req.AddCookie(c)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("detail status %d body %s", rr.Code, rr.Body.String())
	}
	var detail map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &detail); err != nil {
		t.Fatalf("detail json: %v", err)
	}
	if detail["status"] != "pending" {
		t.Fatalf("detail=%v", detail)
	}

	// Approve.
	req = httptest.NewRequest(http.MethodPost, "/api/approvals/ap1/decision", strings.NewReader(`{"decision":"approve","note":"ship it"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(c)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("decision status %d body %s", rr.Code, rr.Body.String())
	}

	var st string
	if err := sqlDB.QueryRow(`SELECT status FROM approvals WHERE id='ap1'`).Scan(&st); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if st != "approved" {
		t.Fatalf("status=%q want approved", st)
	}
	var pubs int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM publications WHERE content_id='c1' AND status='scheduled'`).Scan(&pubs); err != nil {
		t.Fatalf("pubs: %v", err)
	}
	if pubs < 1 {
		t.Fatalf("approve must schedule publications, got %d", pubs)
	}

	// Second decision → 409.
	req = httptest.NewRequest(http.MethodPost, "/api/approvals/ap1/decision", strings.NewReader(`{"decision":"reject"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(c)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("second decision status %d want 409", rr.Code)
	}
}

func TestApprovals_badDecision(t *testing.T) {
	sqlDB := testDB(t)
	seedApproval(t, sqlDB, "ap2", "pending")
	h := testServer(t, sqlDB, nil).Handler()
	c := login(t, h)

	req := httptest.NewRequest(http.MethodPost, "/api/approvals/ap2/decision", strings.NewReader(`{"decision":"maybe"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(c)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d want 400", rr.Code)
	}
}

func TestApprovals_notFound(t *testing.T) {
	sqlDB := testDB(t)
	h := testServer(t, sqlDB, nil).Handler()
	c := login(t, h)

	req := httptest.NewRequest(http.MethodPost, "/api/approvals/missing/decision", strings.NewReader(`{"decision":"approve"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(c)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d want 404", rr.Code)
	}
}
