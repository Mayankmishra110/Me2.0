package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuth_loginSetsHttpOnlyStrictCookie(t *testing.T) {
	sqlDB := testDB(t)
	h := testServer(t, sqlDB, nil).Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"token":"`+testToken+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	cookies := rr.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%d want 1", len(cookies))
	}
	c := cookies[0]
	if c.Name != sessionCookieName {
		t.Fatalf("cookie name %q", c.Name)
	}
	if !c.HttpOnly {
		t.Fatal("cookie must be HttpOnly")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Fatalf("SameSite=%v want Strict", c.SameSite)
	}
	if c.Path != "/" {
		t.Fatalf("Path=%q want /", c.Path)
	}
	if c.Value == "" {
		t.Fatal("empty session id")
	}
}

func TestAuth_badTokenUnauthorized(t *testing.T) {
	sqlDB := testDB(t)
	h := testServer(t, sqlDB, nil).Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"token":"wrong"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status %d want 401", rr.Code)
	}
}

func TestAuth_protectedRoutesRequireSession(t *testing.T) {
	sqlDB := testDB(t)
	h := testServer(t, sqlDB, nil).Handler()

	paths := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/agents"},
		{http.MethodGet, "/api/approvals"},
		{http.MethodGet, "/api/jobs"},
		{http.MethodGet, "/media/x"},
	}
	for _, tc := range paths {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(tc.method, tc.path, nil))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s: status %d want 401", tc.method, tc.path, rr.Code)
		}
	}
}

func TestAuth_healthIsPublic(t *testing.T) {
	sqlDB := testDB(t)
	h := testServer(t, sqlDB, nil).Handler()

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["ok"] != true {
		t.Fatalf("body=%v", body)
	}
}

func TestAuth_sessionAllowsAPI(t *testing.T) {
	sqlDB := testDB(t)
	h := testServer(t, sqlDB, nil).Handler()
	c := login(t, h)

	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	req.AddCookie(c)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
}

func TestSPA_servesIndex(t *testing.T) {
	sqlDB := testDB(t)
	h := testServer(t, sqlDB, nil).Handler()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "ok") {
		t.Fatalf("body=%q", rr.Body.String())
	}
}
