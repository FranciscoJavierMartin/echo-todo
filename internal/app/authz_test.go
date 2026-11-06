package app_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/app/apptest"
	todohandler "echo-todo-2/internal/todo/handler"
)

func createTodo(t *testing.T, env *apptest.Env, token, body string) todohandler.TodoResponse {
	t.Helper()
	rec := env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/todos", Token: token, Body: body})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	return apptest.JSON[todohandler.TodoResponse](t, rec)
}

// User B must not be able to tell whether user A's todo exists: every verb is a 404.
func TestOwnershipMatrix(t *testing.T) {
	env := apptest.New(t)
	a, b := env.CreateUser("user"), env.CreateUser("user")
	td := createTodo(t, env, a.Token, `{"title":"A's secret"}`)
	path := "/v1/todos/" + td.ID

	cases := []struct {
		method string
		body   string
	}{
		{http.MethodGet, ""},
		{http.MethodPatch, `{"title":"pwned"}`},
		{http.MethodDelete, ""},
	}
	for _, c := range cases {
		t.Run(c.method, func(t *testing.T) {
			req := apptest.Request{Method: c.method, Path: path, Token: b.Token, Headers: map[string]string{"If-Match": `"1"`}}
			if c.body != "" {
				req.Body = c.body
			}
			rec := env.Do(req)
			require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
			require.NotContains(t, rec.Body.String(), "secret")
		})
	}

	// B's list does not include A's todo.
	rec := env.Do(apptest.Request{Method: http.MethodGet, Path: "/v1/todos", Token: b.Token})
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), td.ID)

	// A's todo is untouched.
	rec = env.Do(apptest.Request{Method: http.MethodGet, Path: path, Token: a.Token})
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "A's secret", apptest.JSON[todohandler.TodoResponse](t, rec).Title)
}

func TestAdminOnlyRoutes(t *testing.T) {
	env := apptest.New(t)
	user, admin := env.CreateUser("user"), env.CreateAdmin()
	td := createTodo(t, env, user.Token, `{"title":"audited"}`)
	rec := env.Do(apptest.Request{Method: http.MethodPatch, Path: "/v1/todos/" + td.ID, Token: user.Token,
		Body: `{"status":"done"}`, Headers: map[string]string{"If-Match": `"1"`}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	for _, path := range []string{"/v1/todos/" + td.ID + "/audit", "/debug/pprof/cmdline"} {
		rec := env.Do(apptest.Request{Method: http.MethodGet, Path: path, Token: user.Token})
		require.Equal(t, http.StatusForbidden, rec.Code, path)
		require.Contains(t, rec.Body.String(), `"code":"forbidden"`)
	}

	// Admins see the audit trail of todos they do not own.
	rec = env.Do(apptest.Request{Method: http.MethodGet, Path: "/v1/todos/" + td.ID + "/audit", Token: admin.Token})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := apptest.JSON[struct {
		Items []todohandler.AuditEventResponse `json:"items"`
	}](t, rec)
	require.Len(t, body.Items, 2)
	require.Equal(t, "create", body.Items[0].Action)
	require.Equal(t, "update", body.Items[1].Action)
	require.Equal(t, "done", body.Items[1].Changes["status"].To)
	require.Equal(t, user.ID.String(), body.Items[1].ActorID)

	rec = env.Do(apptest.Request{Method: http.MethodGet, Path: "/v1/todos/00000000-0000-0000-0000-000000000000/audit", Token: admin.Token})
	require.Equal(t, http.StatusNotFound, rec.Code)
}
