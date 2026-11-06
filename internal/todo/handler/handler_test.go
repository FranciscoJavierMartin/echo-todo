package handler_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/auth"
	"echo-todo-2/internal/httpx"
	"echo-todo-2/internal/todo/handler"
	"echo-todo-2/internal/todo/todotest"
)

type env struct {
	e     *echo.Echo
	owner uuid.UUID
}

func newEnv(t *testing.T) *env {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	e := echo.NewWithConfig(echo.Config{
		Logger:           log,
		HTTPErrorHandler: httpx.ErrorHandler(log, false),
		Validator:        httpx.NewValidator(),
		JSONSerializer:   httpx.StrictJSONSerializer{},
	})
	owner := uuid.New()
	asUser := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if c.Request().Header.Get("X-Test-Anon") == "" {
				httpx.SetPrincipal(c, &auth.Principal{UserID: owner, Role: auth.RoleUser})
			}
			return next(c)
		}
	}
	svc, _ := todotest.NewService()
	h := handler.New(svc)
	g := e.Group("/v1/todos", middleware.BodyLimit(1<<20), asUser)
	g.POST("", h.Create)
	g.GET("", h.List)
	g.GET("/:id", h.Get)
	g.PATCH("/:id", h.Update)
	g.DELETE("/:id", h.Delete)
	return &env{e: e, owner: owner}
}

func (en *env) do(method, path, body string) *httptest.ResponseRecorder {
	return en.doH(method, path, body, nil)
}

func (en *env) doH(method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if body != "" {
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	en.e.ServeHTTP(rec, req)
	return rec
}

func problem(t *testing.T, rec *httptest.ResponseRecorder) httpx.Problem {
	t.Helper()
	var p httpx.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p), rec.Body.String())
	return p
}

func TestCreate_PerFieldValidation(t *testing.T) {
	en := newEnv(t)
	rec := en.do(http.MethodPost, "/v1/todos", `{"title":"  ","priority":9}`)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	p := problem(t, rec)
	require.Equal(t, "is required", p.Errors["title"])
	require.Equal(t, "must be at most 5", p.Errors["priority"])
}

func TestCreate_TypeAndSyntaxErrorsAre422(t *testing.T) {
	en := newEnv(t)
	rec := en.do(http.MethodPost, "/v1/todos", `{"title":"a","priority":"high"}`)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Equal(t, "must be of type number", problem(t, rec).Errors["priority"])

	rec = en.do(http.MethodPost, "/v1/todos", `{"title":`)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Contains(t, problem(t, rec).Errors, "body")

	rec = en.do(http.MethodPost, "/v1/todos", `{"title":"a","titel":"typo"}`)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Equal(t, "unknown field", problem(t, rec).Errors["titel"])
}

func TestCreate_UnsupportedMediaType(t *testing.T) {
	en := newEnv(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/todos", strings.NewReader("title=x"))
	req.Header.Set(echo.HeaderContentType, "text/plain")
	rec := httptest.NewRecorder()
	en.e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnsupportedMediaType, rec.Code)
}

func TestCreate_BodyTooLarge(t *testing.T) {
	en := newEnv(t)
	big := `{"title":"` + strings.Repeat("a", 10<<20) + `"}`
	rec := en.do(http.MethodPost, "/v1/todos", big)
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)

	// Without Content-Length the limited reader still trips during decoding.
	req := httptest.NewRequest(http.MethodPost, "/v1/todos", io.MultiReader(strings.NewReader(big)))
	req.ContentLength = -1
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec = httptest.NewRecorder()
	en.e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

func TestList_InvalidLimitIs422(t *testing.T) {
	en := newEnv(t)
	rec := en.do(http.MethodGet, "/v1/todos?limit=abc", "")
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Contains(t, problem(t, rec).Errors, "limit")

	require.Equal(t, http.StatusUnprocessableEntity, en.do(http.MethodGet, "/v1/todos?limit=1000", "").Code)
	require.Equal(t, http.StatusUnprocessableEntity, en.do(http.MethodGet, "/v1/todos?priority=9", "").Code)
	require.Equal(t, http.StatusUnprocessableEntity, en.do(http.MethodGet, "/v1/todos?priority=x", "").Code)
	require.Equal(t, http.StatusUnprocessableEntity, en.do(http.MethodGet, "/v1/todos?due_before=tomorrow", "").Code)
	require.Equal(t, http.StatusUnprocessableEntity, en.do(http.MethodGet, "/v1/todos?sort=password", "").Code)
	require.Equal(t, http.StatusUnprocessableEntity, en.do(http.MethodGet, "/v1/todos?cursor=garbage", "").Code)
	require.Equal(t, http.StatusUnprocessableEntity, en.do(http.MethodGet, "/v1/todos?count=maybe", "").Code)
	require.Equal(t, http.StatusUnprocessableEntity, en.do(http.MethodGet, "/v1/todos?status=bogus", "").Code)
	require.Equal(t, http.StatusOK, en.do(http.MethodGet, "/v1/todos?limit=", "").Code, "present-but-empty uses the default")
}

func TestCRUDHappyPath(t *testing.T) {
	en := newEnv(t)
	rec := en.do(http.MethodPost, "/v1/todos", `{"title":"write tests","priority":2,"due_at":"2030-01-02T03:04:05Z"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var created handler.TodoResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	require.Equal(t, "/v1/todos/"+created.ID, rec.Header().Get(echo.HeaderLocation))
	require.Equal(t, 2, created.Priority)

	require.Equal(t, `"1"`, rec.Header().Get("ETag"))

	rec = en.do(http.MethodGet, "/v1/todos/"+created.ID, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, `"1"`, rec.Header().Get("ETag"))

	rec = en.do(http.MethodPatch, "/v1/todos/"+created.ID, `{"status":"done"}`)
	require.Equal(t, http.StatusPreconditionRequired, rec.Code, "If-Match is mandatory")
	rec = en.doH(http.MethodPatch, "/v1/todos/"+created.ID, `{"status":"done"}`, map[string]string{"If-Match": "3"})
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "malformed ETag")

	rec = en.doH(http.MethodPatch, "/v1/todos/"+created.ID, `{"status":"done","due_at":null}`, map[string]string{"If-Match": `"1"`})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var updated handler.TodoResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &updated))
	require.Equal(t, "done", updated.Status)
	require.Nil(t, updated.DueAt)
	require.Equal(t, 2, updated.Version)
	require.Equal(t, `"2"`, rec.Header().Get("ETag"))

	// Stale ETag → 409 with the current representation.
	rec = en.doH(http.MethodPatch, "/v1/todos/"+created.ID, `{"title":"lost update"}`, map[string]string{"If-Match": `"1"`})
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, `"2"`, rec.Header().Get("ETag"))
	var conflict struct {
		Current handler.TodoResponse `json:"current"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &conflict))
	require.Equal(t, "done", conflict.Current.Status)

	rec = en.doH(http.MethodPatch, "/v1/todos/"+created.ID, `{"title":""}`, map[string]string{"If-Match": "*"})
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	rec = en.do(http.MethodGet, "/v1/todos?status=done,open&limit=5&count=true", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var list handler.ListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Equal(t, 1, *list.Total)
	require.Nil(t, list.NextCursor)

	require.Equal(t, http.StatusPreconditionRequired, en.do(http.MethodDelete, "/v1/todos/"+created.ID, "").Code)
	require.Equal(t, http.StatusConflict, en.doH(http.MethodDelete, "/v1/todos/"+created.ID, "", map[string]string{"If-Match": `"1"`}).Code)
	require.Equal(t, http.StatusNoContent, en.doH(http.MethodDelete, "/v1/todos/"+created.ID, "", map[string]string{"If-Match": `"2"`}).Code)
	require.Equal(t, http.StatusNotFound, en.do(http.MethodGet, "/v1/todos/"+created.ID, "").Code)
	require.Equal(t, http.StatusNotFound, en.do(http.MethodGet, "/v1/todos/not-a-uuid", "").Code)
}

func TestUnauthenticated(t *testing.T) {
	en := newEnv(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/todos", nil)
	req.Header.Set("X-Test-Anon", "1")
	rec := httptest.NewRecorder()
	en.e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
