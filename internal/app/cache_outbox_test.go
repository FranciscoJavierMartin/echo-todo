package app_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/app"
	"echo-todo-2/internal/app/apptest"
	"echo-todo-2/internal/outbox"
	todohandler "echo-todo-2/internal/todo/handler"
)

func withRedis(t *testing.T) (*miniredis.Miniredis, apptest.Option) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, apptest.WithApp(app.WithRedis(rdb))
}

func TestConditionalGet304(t *testing.T) {
	env := apptest.New(t)
	u := env.CreateUser("user")
	td := createTodo(t, env, u.Token, `{"title":"etag me"}`)
	path := "/v1/todos/" + td.ID

	rec := env.Do(apptest.Request{Method: http.MethodGet, Path: path, Token: u.Token, Headers: map[string]string{"If-None-Match": `"1"`}})
	require.Equal(t, http.StatusNotModified, rec.Code)
	require.Empty(t, rec.Body.String())
	require.Equal(t, `"1"`, rec.Header().Get("ETag"))

	rec = env.Do(apptest.Request{Method: http.MethodGet, Path: path, Token: u.Token, Headers: map[string]string{"If-None-Match": `W/"1", "7"`}})
	require.Equal(t, http.StatusNotModified, rec.Code, "weak comparison and lists")

	env.Do(apptest.Request{Method: http.MethodPatch, Path: path, Token: u.Token, Body: `{"notes":"changed"}`, Headers: map[string]string{"If-Match": `"1"`}})
	rec = env.Do(apptest.Request{Method: http.MethodGet, Path: path, Token: u.Token, Headers: map[string]string{"If-None-Match": `"1"`}})
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, `"2"`, rec.Header().Get("ETag"))
}

func TestCachedReadsAndInvalidation(t *testing.T) {
	mr, opt := withRedis(t)
	env := apptest.New(t, opt)
	u, other := env.CreateUser("user"), env.CreateUser("user")
	td := createTodo(t, env, u.Token, `{"title":"cache me"}`)
	path := "/v1/todos/" + td.ID
	dataKey := "todo:{" + td.ID + "}:v1"

	require.Equal(t, http.StatusOK, env.Do(apptest.Request{Method: http.MethodGet, Path: path, Token: u.Token}).Code)
	require.True(t, mr.Exists(dataKey), "first read populates the cache")

	// Prove the next read is served from Redis: change the row behind the cache's back.
	_, err := env.Pool.Exec(context.Background(), "UPDATE todos SET title = 'db-only' WHERE id = $1", td.ID)
	require.NoError(t, err)
	rec := env.Do(apptest.Request{Method: http.MethodGet, Path: path, Token: u.Token})
	require.Equal(t, "cache me", apptest.JSON[todohandler.TodoResponse](t, rec).Title)

	// The cache never bypasses ownership.
	require.Equal(t, http.StatusNotFound, env.Do(apptest.Request{Method: http.MethodGet, Path: path, Token: other.Token}).Code)

	// 304 from cache.
	rec = env.Do(apptest.Request{Method: http.MethodGet, Path: path, Token: u.Token, Headers: map[string]string{"If-None-Match": `"1"`}})
	require.Equal(t, http.StatusNotModified, rec.Code)

	// A write invalidates.
	rec = env.Do(apptest.Request{Method: http.MethodPatch, Path: path, Token: u.Token, Body: `{"priority":1}`, Headers: map[string]string{"If-Match": `"1"`}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.False(t, mr.Exists(dataKey))

	// Deleting purges the key.
	mr.FastForward(5e9)
	env.Do(apptest.Request{Method: http.MethodGet, Path: path, Token: u.Token}) // repopulate v2
	require.True(t, mr.Exists("todo:{"+td.ID+"}:v2"))
	rec = env.Do(apptest.Request{Method: http.MethodDelete, Path: path, Token: u.Token, Headers: map[string]string{"If-Match": `"2"`}})
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.False(t, mr.Exists("todo:{"+td.ID+"}:v2"), "delete purges the cached payload")
	require.Equal(t, http.StatusNotFound, env.Do(apptest.Request{Method: http.MethodGet, Path: path, Token: u.Token}).Code)
}

type recorder struct {
	mu   sync.Mutex
	msgs []outbox.Message
}

func (r *recorder) Handle(_ context.Context, m outbox.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, m)
	return nil
}

func (r *recorder) topics() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, m := range r.msgs {
		out = append(out, m.Topic)
	}
	return out
}

func TestOutboxIsTransactionalAndDrained(t *testing.T) {
	rec := &recorder{}
	env := apptest.New(t, apptest.WithApp(app.WithNotifier(rec)))
	u := env.CreateUser("user")
	ctx := context.Background()

	td := createTodo(t, env, u.Token, `{"title":"notify"}`)
	env.Do(apptest.Request{Method: http.MethodPatch, Path: "/v1/todos/" + td.ID, Token: u.Token,
		Body: `{"status":"done"}`, Headers: map[string]string{"If-Match": `"1"`}})

	// A rejected write produces no event.
	env.Do(apptest.Request{Method: http.MethodPatch, Path: "/v1/todos/" + td.ID, Token: u.Token,
		Body: `{"status":"open"}`, Headers: map[string]string{"If-Match": `"1"`}})

	pending, err := outbox.Pending(ctx, env.Pool)
	require.NoError(t, err)
	require.EqualValues(t, 3, pending, "created + updated + completed, nothing for the 409")

	n, err := env.App.Worker.ProcessBatch(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, n)
	require.Equal(t, []string{"todo.created", "todo.updated", "todo.completed"}, rec.topics())
	pending, _ = outbox.Pending(ctx, env.Pool)
	require.Zero(t, pending)
}

func TestOutboxSurvivesWorkerCrash(t *testing.T) {
	env := apptest.New(t)
	u := env.CreateUser("user")
	ctx := context.Background()
	for range 5 {
		createTodo(t, env, u.Token, `{"title":"crash test"}`)
	}

	// Worker 1 dies (panics) while delivering the 3rd message of its batch.
	var delivered int
	crashing := outbox.NewWorker(env.Pool, outbox.HandlerFunc(func(context.Context, outbox.Message) error {
		delivered++
		if delivered == 3 {
			panic("worker crashed")
		}
		return nil
	}), env.Logger())
	require.Panics(t, func() { _, _ = crashing.ProcessBatch(ctx) })

	pending, err := outbox.Pending(ctx, env.Pool)
	require.NoError(t, err)
	require.EqualValues(t, 5, pending, "the crashed batch rolled back; no row lost")

	// Worker 2 hits a transient error on one message: it is retried later, not dropped.
	failOnce := true
	flaky := outbox.NewWorker(env.Pool, outbox.HandlerFunc(func(_ context.Context, m outbox.Message) error {
		if failOnce {
			failOnce = false
			return errors.New("smtp timeout")
		}
		return nil
	}), env.Logger())
	n, err := flaky.ProcessBatch(ctx)
	require.NoError(t, err)
	require.Equal(t, 4, n)
	pending, _ = outbox.Pending(ctx, env.Pool)
	require.EqualValues(t, 1, pending, "failed message rescheduled with backoff")

	var attempts int
	var lastErr string
	require.NoError(t, env.Pool.QueryRow(ctx, "SELECT attempts, last_error FROM outbox WHERE processed_at IS NULL").Scan(&attempts, &lastErr))
	require.Equal(t, 1, attempts)
	require.Equal(t, "smtp timeout", lastErr)
}

func TestWorkerStopsWithContext(t *testing.T) {
	env := apptest.New(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- env.App.Worker.Run(ctx) }()
	cancel()
	require.NoError(t, <-done)
}
