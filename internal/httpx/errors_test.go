package httpx_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/apperr"
	"echo-todo-2/internal/httpx"
	"echo-todo-2/internal/platform/logger"
	"echo-todo-2/internal/todo"
	"echo-todo-2/internal/todo/todotest"
)

func newEcho(expose bool) (*echo.Echo, *bytes.Buffer) {
	var logs bytes.Buffer
	log := logger.NewWithWriter("prod", &logs)
	e := echo.NewWithConfig(echo.Config{Logger: log, HTTPErrorHandler: httpx.ErrorHandler(log, expose)})
	return e, &logs
}

func do(e *echo.Echo, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m), rec.Body.String())
	return m
}

func TestNotFoundFromServiceIsProblemJSON(t *testing.T) {
	e, _ := newEcho(false)
	svc, _ := todotest.NewService()
	e.GET("/todos/:id", func(c *echo.Context) error {
		_, err := svc.Get(c.Request().Context(), uuid.New(), uuid.Nil)
		return err
	})

	rec := do(e, http.MethodGet, "/todos/00000000-0000-0000-0000-000000000000")
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, echo.MIMEApplicationProblemJSON, rec.Header().Get(echo.HeaderContentType))
	body := decode(t, rec)
	require.Equal(t, "not_found", body["code"])
	require.Equal(t, "Not Found", body["title"])
	require.Equal(t, float64(404), body["status"])
	require.Equal(t, "todo not found", body["detail"])
	require.Equal(t, "/todos/00000000-0000-0000-0000-000000000000", body["instance"])
}

func TestDatabaseOutageDoesNotLeak(t *testing.T) {
	e, logs := newEcho(false)
	repo := todotest.NewRepo()
	repo.Err = errors.New(`ERROR: relation "todos" does not exist (SQLSTATE 42P01) SELECT id, owner_id FROM todos`)
	svc := todo.NewService(repo, todotest.NoTx{}, repo)
	e.GET("/todos/:id", func(c *echo.Context) error {
		_, err := svc.Get(c.Request().Context(), uuid.New(), uuid.New())
		return err
	})

	rec := do(e, http.MethodGet, "/todos/x")
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.NotContains(t, rec.Body.String(), "SELECT")
	require.NotContains(t, rec.Body.String(), "SQLSTATE")
	require.Equal(t, "internal error", decode(t, rec)["detail"])

	require.Contains(t, logs.String(), "SQLSTATE 42P01", "the cause must be logged")
	require.Contains(t, logs.String(), `"level":"ERROR"`)
}

func TestExposeErrorsIncludesCause(t *testing.T) {
	e, _ := newEcho(true)
	e.GET("/x", func(c *echo.Context) error { return apperr.Internal("op", errors.New("boom")) })
	require.Contains(t, do(e, http.MethodGet, "/x").Body.String(), "boom")
}

func TestValidationFieldsAndRequestID(t *testing.T) {
	e, _ := newEcho(false)
	e.GET("/x", func(c *echo.Context) error {
		c.SetRequest(c.Request().WithContext(logger.WithRequestID(c.Request().Context(), "rid-9")))
		return apperr.InvalidFields(map[string]string{"title": "required"})
	})
	rec := do(e, http.MethodGet, "/x")
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := decode(t, rec)
	require.Equal(t, map[string]any{"title": "required"}, body["errors"])
	require.Equal(t, "rid-9", body["request_id"])
}

func TestEchoSentinelsFlowThrough(t *testing.T) {
	e, _ := newEcho(false)
	e.GET("/only-get", func(c *echo.Context) error { return c.NoContent(http.StatusOK) })
	e.GET("/teapot", func(c *echo.Context) error {
		return echo.NewHTTPError(http.StatusTeapot, "short and stout").Wrap(errors.New("hidden"))
	})

	rec := do(e, http.MethodGet, "/missing")
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, echo.MIMEApplicationProblemJSON, rec.Header().Get(echo.HeaderContentType))

	rec = do(e, http.MethodPost, "/only-get")
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)

	rec = do(e, http.MethodGet, "/teapot")
	require.Equal(t, http.StatusTeapot, rec.Code)
	require.Contains(t, rec.Body.String(), "short and stout")
	require.NotContains(t, rec.Body.String(), "hidden")
}

func TestCommittedResponseIsOnlyLogged(t *testing.T) {
	e, logs := newEcho(false)
	e.GET("/x", func(c *echo.Context) error {
		_ = c.String(http.StatusOK, "partial")
		return errors.New("late failure")
	})
	rec := do(e, http.MethodGet, "/x")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "partial", rec.Body.String())
	require.Contains(t, logs.String(), "committed")
}

func TestHeadGetsNoBody(t *testing.T) {
	e, _ := newEcho(false)
	e.HEAD("/x", func(c *echo.Context) error { return apperr.NotFound("nope") })
	rec := do(e, http.MethodHead, "/x")
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Empty(t, rec.Body.String())
}
