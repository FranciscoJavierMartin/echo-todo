package handler_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// FuzzBindCreateTodo feeds arbitrary bodies through binding, validation and the
// service. Invariants: no panic, and the status is 201 or a 4xx — never a 5xx.
func FuzzBindCreateTodo(f *testing.F) {
	seeds := []string{
		`{"title":"ok"}`,
		`{"title":"","priority":9}`,
		`{"title":"x","due_at":"not-a-date"}`,
		`{"title":"x","due_at":null,"status":"done","notes":"n","priority":5}`,
		`{"title":` + strings.Repeat(`"`, 3),
		`[]`, `null`, `{}`, `{"title":"a"} {"title":"b"}`,
		`{"title":"\u0000\ud800"}`,
		`{"title":"x","extra":1}`,
		`{"title":1e309}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	if fixture, err := os.ReadFile("testdata/create_todo.json"); err == nil {
		f.Add(string(fixture))
	}

	en := newEnv(f)
	f.Fuzz(func(t *testing.T, body string) {
		req := httptest.NewRequest(http.MethodPost, "/v1/todos", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		en.e.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated && (rec.Code < 400 || rec.Code > 499) {
			t.Fatalf("body %q → status %d: %s", body, rec.Code, rec.Body.String())
		}
	})
}
