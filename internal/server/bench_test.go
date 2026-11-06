package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"echo-todo-2/internal/auth"
	"echo-todo-2/internal/todo"
)

func noop(c *echo.Context) error { return nil }

func benchRoute(b *testing.B, e *echo.Echo, method, path string) {
	b.Helper()
	req := httptest.NewRequest(method, path, nil)
	c := e.NewContext(req, httptest.NewRecorder())
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if e.Router().Route(c) == nil {
			b.Fatal("no handler")
		}
	}
}

func apiRouter(cfg echo.RouterConfig, concurrent bool, any bool) *echo.Echo {
	var r echo.Router = echo.NewRouter(cfg)
	if concurrent {
		r = echo.NewConcurrentRouter(r)
	}
	e := echo.NewWithConfig(echo.Config{Router: r})
	if any {
		e.Any("/v1/todos/:id", noop)
	} else {
		e.GET("/v1/todos/:id", noop)
		e.PATCH("/v1/todos/:id", noop)
		e.DELETE("/v1/todos/:id", noop)
	}
	e.GET("/v1/todos", noop)
	e.POST("/v1/todos", noop)
	e.GET("/v1/todos/:id/attachments/:aid", noop)
	e.GET("/healthz", noop)
	return e
}

// Router hot path: must be 0 allocs/op.
func BenchmarkRouter(b *testing.B) {
	id := "/v1/todos/" + uuid.NewString()
	b.Run("static", func(b *testing.B) {
		benchRoute(b, apiRouter(echo.RouterConfig{}, false, false), http.MethodGet, "/healthz")
	})
	b.Run("param", func(b *testing.B) { benchRoute(b, apiRouter(echo.RouterConfig{}, false, false), http.MethodGet, id) })
	b.Run("two-params", func(b *testing.B) {
		benchRoute(b, apiRouter(echo.RouterConfig{}, false, false), http.MethodGet, id+"/attachments/"+uuid.NewString())
	})
	b.Run("any", func(b *testing.B) { benchRoute(b, apiRouter(echo.RouterConfig{}, false, true), http.MethodGet, id) })
	b.Run("head-auto", func(b *testing.B) {
		benchRoute(b, apiRouter(echo.RouterConfig{AutoHandleHEAD: true}, false, false), http.MethodHead, id)
	})
	b.Run("head-405", func(b *testing.B) { benchRoute(b, apiRouter(echo.RouterConfig{}, false, false), http.MethodHead, id) })
	b.Run("concurrent-router", func(b *testing.B) { benchRoute(b, apiRouter(echo.RouterConfig{}, true, false), http.MethodGet, id) })
	b.Run("concurrent-router-parallel", func(b *testing.B) {
		e := apiRouter(echo.RouterConfig{}, true, false)
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			c := e.NewContext(httptest.NewRequest(http.MethodGet, id, nil), httptest.NewRecorder())
			for pb.Next() {
				e.Router().Route(c)
			}
		})
	})
	b.Run("default-router-parallel", func(b *testing.B) {
		e := apiRouter(echo.RouterConfig{}, false, false)
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			c := e.NewContext(httptest.NewRequest(http.MethodGet, id, nil), httptest.NewRecorder())
			for pb.Next() {
				e.Router().Route(c)
			}
		})
	})
}

// Full production chain (all Pre/Use middleware, JWT, handler, JSON) with an
// in-memory service, so the number reflects framework + our code, not Postgres.
func BenchmarkFullChain(b *testing.B) {
	cfg := testConfig()
	cfg.RequestsPerSec, cfg.WriteRPS = 1e9, 1e9
	tokens := auth.NewTokens(cfg.JWTSecret, cfg.AccessTTL)
	svc := newFakeTodoService()
	e := New(Deps{Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Tokens: tokens, Todos: todoHandler(svc)})
	uid := uuid.New()
	tok, _, _ := tokens.IssueAccess(uid, auth.RoleUser)
	td, _ := svc.Create(b.Context(), uid, todo.CreateInput{Title: "bench"})
	for range 50 {
		_, _ = svc.Create(b.Context(), uid, todo.CreateInput{Title: "row"})
	}

	run := func(b *testing.B, path string) {
		b.ReportAllocs()
		for b.Loop() {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer "+tok)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				b.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
		}
	}
	b.Run("get", func(b *testing.B) { run(b, "/v1/todos/"+td.ID.String()) })
	b.Run("list-20", func(b *testing.B) { run(b, "/v1/todos?limit=20&sort=-priority,due_at") })
	b.Run("healthz", func(b *testing.B) { run(b, "/healthz") })
}
