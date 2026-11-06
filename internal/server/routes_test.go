package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/auth"
	"echo-todo-2/internal/config"
	"echo-todo-2/internal/todo"
	todohandler "echo-todo-2/internal/todo/handler"
	"echo-todo-2/internal/todo/todotest"
)

const testSecret = "test-secret-test-secret-test-secret!"

func testConfig() config.Config {
	return config.Config{
		Env: "test", Addr: ":0", JWTSecret: []byte(testSecret),
		AccessTTL: 10 * time.Minute, RefreshTTL: time.Hour,
		MaxUploadBytes: 10 << 20, MaxBodyBytes: 1 << 20, RequestsPerSec: 1000,
		ShutdownTimeout: time.Second,
	}
}

func newTestServer(t *testing.T) (*echo.Echo, *auth.Tokens) {
	t.Helper()
	cfg := testConfig()
	tokens := auth.NewTokens(cfg.JWTSecret, cfg.AccessTTL)
	e := New(Deps{
		Config: cfg,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Tokens: tokens,
		Todos:  todohandler.New(todo.NewService(todotest.NewRepo(), todotest.NoTx{})),
	})
	return e, tokens
}

func bearer(t *testing.T, tokens *auth.Tokens, role string) string {
	t.Helper()
	tok, _, err := tokens.IssueAccess(uuid.New(), role)
	require.NoError(t, err)
	return "Bearer " + tok
}

func serve(e *echo.Echo, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func samplePath(ri echo.RouteInfo) string {
	vals := make([]any, len(ri.Parameters))
	for i, p := range ri.Parameters {
		if p == "*" {
			vals[i] = "sample.csv"
		} else {
			vals[i] = uuid.NewString()
		}
	}
	return ri.Reverse(vals...)
}

func TestEveryRouteIsReachable(t *testing.T) {
	e, _ := newTestServer(t)
	routes := e.Router().Routes()
	require.NotEmpty(t, routes)
	for _, ri := range routes {
		if ri.Method == echo.RouteNotFound {
			continue
		}
		path := samplePath(ri)
		c := e.NewContext(httptest.NewRequest(ri.Method, path, nil), httptest.NewRecorder())
		require.NotNil(t, e.Router().Route(c), "%s %s", ri.Method, path)
		got := c.RouteInfo()
		require.Equal(t, ri.Method, got.Method, "%s %s", ri.Method, path)
		require.Equal(t, ri.Path, got.Path, "%s %s routed to %q", ri.Method, path, got.Path)
	}
}

var cleanPath = regexp.MustCompile(`^(/|(/[^/]+)+)$`)

func TestNoStrayOrMissingSlashes(t *testing.T) {
	e, _ := newTestServer(t)
	for _, ri := range e.Router().Routes() {
		require.Regexp(t, cleanPath, ri.Path, "route path %q", ri.Path)
		require.NotContains(t, ri.Path, "v1todos")
	}
}

func TestNamedRoutesReverse(t *testing.T) {
	e, _ := newTestServer(t)
	id := uuid.New()
	u, err := e.Router().Routes().Reverse(RouteTodosGet, id)
	require.NoError(t, err)
	require.Equal(t, "/v1/todos/"+id.String(), u)

	posts, err := e.Router().Routes().FilterByMethod(http.MethodPost)
	require.NoError(t, err)
	require.NotEmpty(t, posts)
	for _, name := range []string{RouteTodosCreate, RouteTodosList, RouteTodosGet, RouteTodosUpdate, RouteTodosDelete} {
		found, err := e.Router().Routes().FilterByName(name)
		require.NoError(t, err)
		require.Len(t, found, 1, name)
	}
}

func TestUnknownPathsAreJSON404(t *testing.T) {
	e, tokens := newTestServer(t)
	for _, p := range []string{"/nope", "/v1", "/v1/nope", "/v1/todos/a/b/c"} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		req.Header.Set(echo.HeaderAuthorization, bearer(t, tokens, auth.RoleUser))
		rec := serve(e, req)
		require.Equal(t, http.StatusNotFound, rec.Code, p)
		require.Equal(t, echo.MIMEApplicationProblemJSON, rec.Header().Get(echo.HeaderContentType), p)
		var body map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Equal(t, float64(404), body["status"])
	}
}

func TestWrongMethodIs405WithAllow(t *testing.T) {
	e, _ := newTestServer(t)
	rec := serve(e, httptest.NewRequest(http.MethodPut, "/v1/todos/"+uuid.NewString(), nil))
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	allow := rec.Header().Get(echo.HeaderAllow)
	for _, m := range []string{"GET", "PATCH", "DELETE"} {
		require.Contains(t, allow, m)
	}

	rec = serve(e, httptest.NewRequest(http.MethodPut, "/v1/todos", nil))
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	require.Contains(t, rec.Header().Get(echo.HeaderAllow), "POST")
	require.Equal(t, echo.MIMEApplicationProblemJSON, rec.Header().Get(echo.HeaderContentType))
}

func TestTrailingSlashIsRemoved(t *testing.T) {
	e, tokens := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/todos/", nil)
	req.Header.Set(echo.HeaderAuthorization, bearer(t, tokens, auth.RoleUser))
	require.Equal(t, http.StatusOK, serve(e, req).Code)
}

func TestAutoHead(t *testing.T) {
	e, _ := newTestServer(t)
	rec := serve(e, httptest.NewRequest(http.MethodHead, "/healthz", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, rec.Body.String())
}

func TestAuthRequiredOnTodos(t *testing.T) {
	e, tokens := newTestServer(t)
	rec := serve(e, httptest.NewRequest(http.MethodGet, "/v1/todos", nil))
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Contains(t, rec.Body.String(), `"code":"missing_token"`)

	req := httptest.NewRequest(http.MethodGet, "/v1/todos", nil)
	req.Header.Set(echo.HeaderAuthorization, "Bearer garbage")
	rec = serve(e, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Contains(t, rec.Body.String(), `"code":"invalid_token"`)

	expired := tokens.WithClock(func() time.Time { return time.Now().Add(-time.Hour) })
	req = httptest.NewRequest(http.MethodGet, "/v1/todos", nil)
	req.Header.Set(echo.HeaderAuthorization, bearer(t, expired, auth.RoleUser))
	rec = serve(e, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Contains(t, rec.Body.String(), `"code":"token_expired"`)

	req = httptest.NewRequest(http.MethodPost, "/v1/todos", strings.NewReader(`{"title":"x"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set(echo.HeaderAuthorization, bearer(t, tokens, auth.RoleUser))
	rec = serve(e, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.True(t, strings.HasPrefix(rec.Header().Get(echo.HeaderLocation), "/v1/todos/"))
}
