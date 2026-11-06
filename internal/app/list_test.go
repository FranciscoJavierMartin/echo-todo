package app_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/app/apptest"
	"echo-todo-2/internal/httpx"
	todohandler "echo-todo-2/internal/todo/handler"
)

func TestListPaginationOverHTTP(t *testing.T) {
	env := apptest.New(t)
	u := env.CreateUser("user")
	for i := range 25 {
		createTodo(t, env, u.Token, fmt.Sprintf(`{"title":"task %02d","priority":%d}`, i, 1+i%5))
	}
	ids := map[string]bool{}
	next := ""
	pages := 0
	for {
		q := url.Values{"limit": {"10"}, "sort": {"-priority,title"}, "count": {"true"}}
		if next != "" {
			q.Set("cursor", next)
		}
		rec := env.Do(apptest.Request{Method: http.MethodGet, Path: "/v1/todos?" + q.Encode(), Token: u.Token})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		page := apptest.JSON[todohandler.ListResponse](t, rec)
		require.Equal(t, 25, *page.Total)
		for _, it := range page.Items {
			require.False(t, ids[it.ID])
			ids[it.ID] = true
		}
		pages++
		if page.NextCursor == nil {
			break
		}
		next = *page.NextCursor
	}
	require.Len(t, ids, 25)
	require.Equal(t, 3, pages)

	rec := env.Do(apptest.Request{Method: http.MethodGet, Path: "/v1/todos?sort=owner_id", Token: u.Token})
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Contains(t, apptest.JSON[httpx.Problem](t, rec).Errors, "sort")

	// A cursor minted for one sort cannot be replayed with another.
	rec = env.Do(apptest.Request{Method: http.MethodGet, Path: "/v1/todos?sort=title&cursor=" + url.QueryEscape(next), Token: u.Token})
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}
