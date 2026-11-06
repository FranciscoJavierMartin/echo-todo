package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/echotest"
	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/auth"
	"echo-todo-2/internal/httpx"
	"echo-todo-2/internal/todo"
	"echo-todo-2/internal/todo/handler"
	"echo-todo-2/internal/todo/todotest"
)

var goldenID = uuid.MustParse("11111111-1111-4111-8111-111111111111")

func goldenTodo(owner uuid.UUID) todo.Todo {
	due := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	return todo.Todo{
		ID: goldenID, OwnerID: owner, Title: "Golden", Notes: "fixed fixture", Status: todo.StatusDone,
		DueAt: &due, Priority: 1, Version: 4,
		CreatedAt: time.Date(2029, 12, 1, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2029, 12, 2, 0, 0, 0, 0, time.UTC),
	}
}

// Direct handler invocation with echotest.ContextConfig: no router, no middleware.
func TestGet_GoldenResponse(t *testing.T) {
	svc, repo := todotest.NewService()
	owner := uuid.New()
	repo.Put(goldenTodo(owner))
	h := handler.New(svc)

	c, rec := echotest.ContextConfig{
		Request:    httptest.NewRequest(http.MethodGet, "/v1/todos/"+goldenID.String(), nil),
		PathValues: echo.PathValues{{Name: "id", Value: goldenID.String()}},
	}.ToContextRecorder(t)
	httpx.SetPrincipal(c, &auth.Principal{UserID: owner, Role: auth.RoleUser})

	require.NoError(t, h.Get(c))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, `"4"`, rec.Header().Get("ETag"))
	golden := echotest.LoadBytes(t, "testdata/get_todo.golden.json", echotest.TrimNewlineEnd)
	require.Equal(t, string(golden), strings.TrimSuffix(rec.Body.String(), "\n"))
}

func TestGet_NotModifiedAndErrors(t *testing.T) {
	svc, repo := todotest.NewService()
	owner := uuid.New()
	repo.Put(goldenTodo(owner))
	h := handler.New(svc)

	rec := echotest.ContextConfig{
		Headers:    http.Header{"If-None-Match": {`"4"`}},
		PathValues: echo.PathValues{{Name: "id", Value: goldenID.String()}},
	}.ServeWithHandler(t, func(c *echo.Context) error {
		httpx.SetPrincipal(c, &auth.Principal{UserID: owner, Role: auth.RoleUser})
		return h.Get(c)
	})
	require.Equal(t, http.StatusNotModified, rec.Code)

	// No principal → 401 through the RFC 9457 handler.
	rec = echotest.ContextConfig{
		PathValues: echo.PathValues{{Name: "id", Value: goldenID.String()}},
	}.ServeWithHandler(t, h.Get, httpx.ErrorHandler(discardLogger(), false))
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, echo.MIMEApplicationProblemJSON, rec.Header().Get(echo.HeaderContentType))
}

// Fixture file through the full mini server (validator + strict JSON).
func TestCreate_FromFixture(t *testing.T) {
	en := newEnv(t)
	body := echotest.LoadBytes(t, "testdata/create_todo.json")
	rec := en.do(http.MethodPost, "/v1/todos", string(body))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var got handler.TodoResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "Write the quarterly report", got.Title)
	require.Equal(t, 2, got.Priority)
	require.Equal(t, "2030-03-31T17:00:00Z", got.DueAt.Format(time.RFC3339))
}

func TestAudit(t *testing.T) {
	svc, _ := todotest.NewService()
	h := handler.New(svc)
	owner := uuid.New()
	td, err := svc.Create(t.Context(), owner, todo.CreateInput{Title: "audited"})
	require.NoError(t, err)

	rec := echotest.ContextConfig{
		PathValues: echo.PathValues{{Name: "id", Value: td.ID.String()}},
	}.ServeWithHandler(t, h.Audit)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"action":"create"`)

	rec = echotest.ContextConfig{
		PathValues: echo.PathValues{{Name: "id", Value: "nope"}},
	}.ServeWithHandler(t, h.Audit, httpx.ErrorHandler(discardLogger(), false))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestExportFormats(t *testing.T) {
	svc, repo := todotest.NewService()
	owner := uuid.New()
	repo.Put(goldenTodo(owner))
	h := handler.New(svc)
	serve := func(name string) *httptest.ResponseRecorder {
		return echotest.ContextConfig{
			PathValues: echo.PathValues{{Name: "*", Value: name}},
		}.ServeWithHandler(t, func(c *echo.Context) error {
			httpx.SetPrincipal(c, &auth.Principal{UserID: owner, Role: auth.RoleUser})
			return h.Export(c)
		}, httpx.ErrorHandler(discardLogger(), false))
	}
	rec := serve("mine.csv")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "id,title,notes,status,priority,due_at,version,created_at,updated_at\n"+
		goldenID.String()+",Golden,fixed fixture,done,1,2030-01-02T03:04:05Z,4,2029-12-01T00:00:00Z,2029-12-02T00:00:00Z\n", rec.Body.String())

	rec = serve("mine.ndjson")
	require.Equal(t, "application/x-ndjson", rec.Header().Get(echo.HeaderContentType))
	require.Contains(t, rec.Body.String(), `"title":"Golden"`)

	require.Equal(t, http.StatusNotFound, serve("mine.pdf").Code)
}
