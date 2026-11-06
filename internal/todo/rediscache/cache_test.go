package rediscache_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/todo"
	"echo-todo-2/internal/todo/rediscache"
)

func newCache(t *testing.T) (*rediscache.Cache, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1, DialTimeout: 100 * time.Millisecond})
	t.Cleanup(func() { _ = rdb.Close() })
	return rediscache.New(rdb, time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil))), mr
}

func TestSetGetUsesVersionedKeys(t *testing.T) {
	c, mr := newCache(t)
	ctx := context.Background()
	td := &todo.Todo{ID: uuid.New(), OwnerID: uuid.New(), Title: "cached", Version: 3}

	_, ok := c.Get(ctx, td.ID)
	require.False(t, ok)
	c.Set(ctx, td)
	got, ok := c.Get(ctx, td.ID)
	require.True(t, ok)
	require.Equal(t, td.Title, got.Title)
	require.True(t, mr.Exists("todo:{"+td.ID.String()+"}:v3"))
	ttl := mr.TTL("todo:{" + td.ID.String() + "}:v3")
	require.Positive(t, ttl)

	// An older version never overwrites a newer one.
	old := *td
	old.Version, old.Title = 2, "stale"
	c.Set(ctx, &old)
	got, _ = c.Get(ctx, td.ID)
	require.Equal(t, "cached", got.Title)
}

func TestInvalidatePurgesAndBlocksLateWrites(t *testing.T) {
	c, mr := newCache(t)
	ctx := context.Background()
	td := &todo.Todo{ID: uuid.New(), Title: "v1", Version: 1}
	c.Set(ctx, td)

	c.Invalidate(ctx, td.ID)
	require.False(t, mr.Exists("todo:{"+td.ID.String()+"}:v1"), "payload purged")
	_, ok := c.Get(ctx, td.ID)
	require.False(t, ok)

	// A slow reader that fetched v1 before the write must not resurrect it.
	c.Set(ctx, td)
	_, ok = c.Get(ctx, td.ID)
	require.False(t, ok, "tombstone rejects late repopulation")

	mr.FastForward(3 * time.Second)
	td.Version, td.Title = 2, "v2"
	c.Set(ctx, td)
	got, ok := c.Get(ctx, td.ID)
	require.True(t, ok)
	require.Equal(t, "v2", got.Title)
}

func TestRedisOutageDegradesToMiss(t *testing.T) {
	c, mr := newCache(t)
	ctx := context.Background()
	mr.Close()
	td := &todo.Todo{ID: uuid.New(), Version: 1}
	c.Set(ctx, td)
	c.Invalidate(ctx, td.ID)
	_, ok := c.Get(ctx, td.ID)
	require.False(t, ok)
}
