package app_test

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/app/apptest"
	"echo-todo-2/internal/httpx"
)

func TestConcurrentPatchesWithSameETag(t *testing.T) {
	env := apptest.New(t)
	u := env.CreateUser("user")
	td := createTodo(t, env, u.Token, `{"title":"contended"}`)

	for round := range 10 {
		get := env.Do(apptest.Request{Method: http.MethodGet, Path: "/v1/todos/" + td.ID, Token: u.Token})
		etag := get.Header().Get("ETag")
		require.NotEmpty(t, etag)

		var wg sync.WaitGroup
		codes := make([]int, 2)
		bodies := make([]string, 2)
		start := make(chan struct{})
		for i := range 2 {
			wg.Go(func() {
				<-start
				rec := env.Do(apptest.Request{Method: http.MethodPatch, Path: "/v1/todos/" + td.ID, Token: u.Token,
					Body: map[string]any{"priority": 1 + i}, Headers: map[string]string{"If-Match": etag}})
				codes[i], bodies[i] = rec.Code, rec.Body.String()
			})
		}
		close(start)
		wg.Wait()

		require.ElementsMatch(t, []int{http.StatusOK, http.StatusConflict}, codes, "round %d: %v", round, bodies)
		loser := 0
		if codes[0] == http.StatusOK {
			loser = 1
		}
		require.Contains(t, bodies[loser], `"current"`, "409 carries the current representation")
	}
}

func TestDeleteRequiresCurrentETag(t *testing.T) {
	env := apptest.New(t)
	u := env.CreateUser("user")
	td := createTodo(t, env, u.Token, `{"title":"x"}`)
	path := "/v1/todos/" + td.ID
	require.Equal(t, http.StatusPreconditionRequired, env.Do(apptest.Request{Method: http.MethodDelete, Path: path, Token: u.Token}).Code)
	env.Do(apptest.Request{Method: http.MethodPatch, Path: path, Token: u.Token, Body: `{"notes":"n"}`, Headers: map[string]string{"If-Match": `"1"`}})
	rec := env.Do(apptest.Request{Method: http.MethodDelete, Path: path, Token: u.Token, Headers: map[string]string{"If-Match": `"1"`}})
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, `"2"`, rec.Header().Get("ETag"))
	require.Equal(t, http.StatusNoContent, env.Do(apptest.Request{Method: http.MethodDelete, Path: path, Token: u.Token, Headers: map[string]string{"If-Match": `"2"`}}).Code)
}

func countTodos(t *testing.T, env *apptest.Env, title string) int {
	t.Helper()
	var n int
	require.NoError(t, env.Pool.QueryRow(context.Background(), "SELECT count(*) FROM todos WHERE title = $1", title).Scan(&n))
	return n
}

func TestIdempotentCreateReplays(t *testing.T) {
	env := apptest.New(t)
	u := env.CreateUser("user")
	post := func(key, body string) *apptestRecorder {
		rec := env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/todos", Token: u.Token, Body: body,
			Headers: map[string]string{"Idempotency-Key": key}})
		return &apptestRecorder{code: rec.Code, body: rec.Body.String(), hdr: rec.Header()}
	}

	first := post("key-1", `{"title":"pay rent"}`)
	require.Equal(t, http.StatusCreated, first.code)
	replay := post("key-1", `{"title":"pay rent"}`)
	require.Equal(t, http.StatusCreated, replay.code)
	require.Equal(t, first.body, replay.body, "identical body")
	require.Equal(t, first.hdr.Get("Location"), replay.hdr.Get("Location"))
	require.Equal(t, first.hdr.Get("ETag"), replay.hdr.Get("ETag"))
	require.Equal(t, "true", replay.hdr.Get("Idempotency-Replayed"))
	require.Equal(t, 1, countTodos(t, env, "pay rent"), "no duplicate row")

	mismatch := post("key-1", `{"title":"something else"}`)
	require.Equal(t, http.StatusUnprocessableEntity, mismatch.code)
	require.Contains(t, mismatch.body, "idempotency_key_mismatch")

	// Keys are per user.
	other := env.CreateUser("user")
	rec := env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/todos", Token: other.Token, Body: `{"title":"pay rent"}`,
		Headers: map[string]string{"Idempotency-Key": "key-1"}})
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Empty(t, rec.Header().Get("Idempotency-Replayed"))

	// Failed attempts release the key so a corrected retry can succeed.
	bad := post("key-2", `{"title":""}`)
	require.Equal(t, http.StatusUnprocessableEntity, bad.code)
	good := post("key-2", `{"title":""}`)
	require.Equal(t, http.StatusUnprocessableEntity, good.code, "same invalid payload fails again rather than replaying")

	require.Equal(t, http.StatusUnprocessableEntity, post("bad key with spaces", `{"title":"x"}`).code)
}

func TestIdempotentConcurrentDuplicates(t *testing.T) {
	env := apptest.New(t)
	u := env.CreateUser("user")
	var wg sync.WaitGroup
	codes := make([]int, 8)
	for i := range codes {
		wg.Go(func() {
			rec := env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/todos", Token: u.Token, Body: `{"title":"once"}`,
				Headers: map[string]string{"Idempotency-Key": "burst"}})
			codes[i] = rec.Code
			if rec.Code == http.StatusConflict {
				require.Equal(t, "idempotency_in_progress", apptest.JSON[httpx.Problem](t, rec).Code)
			}
		})
	}
	wg.Wait()
	for _, c := range codes {
		require.Contains(t, []int{http.StatusCreated, http.StatusConflict}, c)
	}
	require.Equal(t, 1, countTodos(t, env, "once"), "exactly one row despite concurrent duplicates")
}

type apptestRecorder struct {
	code int
	body string
	hdr  http.Header
}
