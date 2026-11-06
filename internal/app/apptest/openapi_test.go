package apptest

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

type captureTB struct {
	testing.TB
	errors []string
}

func (c *captureTB) Errorf(format string, args ...any) {
	c.errors = append(c.errors, fmt.Sprintf(format, args...))
}
func (c *captureTB) Helper() {}

func check(t *testing.T, method, path string, status int, ctype, body string) []string {
	t.Helper()
	Spec(t)
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	if ctype != "" {
		rec.Header().Set("Content-Type", ctype)
	}
	rec.WriteHeader(status)
	_, _ = rec.WriteString(body)
	tb := &captureTB{TB: t}
	validateResponse(tb, req, rec)
	return tb.errors
}

// The validator must actually catch drift, or every contract assertion is vacuous.
func TestValidatorCatchesDrift(t *testing.T) {
	ok := check(t, http.MethodGet, "/healthz", 200, "application/json", `{"status":"ok"}`)
	if len(ok) != 0 {
		t.Fatalf("valid response flagged: %v", ok)
	}
	for _, c := range []struct {
		name, method, path string
		status             int
		body               string
	}{
		{"undocumented status", http.MethodGet, "/healthz", http.StatusTeapot, `{"status":"ok"}`},
		{"undocumented 404 on create", http.MethodPost, "/v1/todos", http.StatusNotFound, `{"type":"about:blank","title":"x","status":404}`},
		{"extra field", http.MethodGet, "/healthz", 200, `{"status":"ok","uptime":3}`},
		{"wrong enum", http.MethodGet, "/v1/todos/00000000-0000-4000-8000-000000000000", 200,
			`{"id":"00000000-0000-4000-8000-000000000000","title":"t","notes":"","status":"maybe","due_at":null,"priority":3,"version":1,"created_at":"2030-01-01T00:00:00Z","updated_at":"2030-01-01T00:00:00Z"}`},
		{"problem missing status", http.MethodGet, "/v1/auth/me", 401, `{"type":"about:blank","title":"Unauthorized"}`},
	} {
		ctype := "application/json"
		if c.status >= 400 {
			ctype = "application/problem+json"
		}
		if errs := check(t, c.method, c.path, c.status, ctype, c.body); len(errs) == 0 {
			t.Errorf("%s: drift not detected", c.name)
		}
	}
}
