// Package rediscache implements todo.Cache on Redis.
//
// Keys: todo:{<id>}:ver → current version, todo:{<id>}:v<n> → JSON payload.
// The braces are a Redis Cluster hash tag, so a todo's keys share a slot and the
// Lua scripts below may touch them atomically.
package rediscache

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"echo-todo-2/internal/todo"
)

const tombstone = "-"

type Cache struct {
	rdb *redis.Client
	ttl time.Duration
	log *slog.Logger
}

var _ todo.Cache = (*Cache)(nil)

func New(rdb *redis.Client, ttl time.Duration, log *slog.Logger) *Cache {
	return &Cache{rdb: rdb, ttl: ttl, log: log}
}

func verKey(id uuid.UUID) string { return "todo:{" + id.String() + "}:ver" }

func dataKey(id uuid.UUID, v string) string { return "todo:{" + id.String() + "}:v" + v }

func (c *Cache) Get(ctx context.Context, id uuid.UUID) (*todo.Todo, bool) {
	ver, err := c.rdb.Get(ctx, verKey(id)).Result()
	if err != nil || ver == tombstone {
		c.logErr(ctx, "get version", err)
		return nil, false
	}
	raw, err := c.rdb.Get(ctx, dataKey(id, ver)).Bytes()
	if err != nil {
		c.logErr(ctx, "get payload", err)
		return nil, false
	}
	var t todo.Todo
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, false
	}
	return &t, true
}

// setScript refuses to cache over a tombstone (a write just happened) or over a
// newer version, which closes the "slow reader repopulates stale data" race.
var setScript = redis.NewScript(`
local cur = redis.call('GET', KEYS[1])
if cur == '-' then return 0 end
if cur and tonumber(cur) and tonumber(cur) > tonumber(ARGV[1]) then return 0 end
redis.call('SET', KEYS[2], ARGV[2], 'PX', ARGV[3])
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[3])
return 1
`)

func (c *Cache) Set(ctx context.Context, t *todo.Todo) {
	raw, err := json.Marshal(t)
	if err != nil {
		return
	}
	v := strconv.Itoa(t.Version)
	err = setScript.Run(ctx, c.rdb, []string{verKey(t.ID), dataKey(t.ID, v)}, v, raw, c.ttl.Milliseconds()).Err()
	c.logErr(ctx, "set", err)
}

// invalidateScript deletes the current payload and leaves a short tombstone.
var invalidateScript = redis.NewScript(`
local cur = redis.call('GET', KEYS[1])
if cur and cur ~= '-' then redis.call('DEL', ARGV[1] .. cur) end
redis.call('SET', KEYS[1], '-', 'PX', ARGV[2])
return 1
`)

func (c *Cache) Invalidate(ctx context.Context, id uuid.UUID) {
	prefix := "todo:{" + id.String() + "}:v"
	err := invalidateScript.Run(context.WithoutCancel(ctx), c.rdb, []string{verKey(id)}, prefix, (2 * time.Second).Milliseconds()).Err()
	c.logErr(ctx, "invalidate", err)
}

func (c *Cache) logErr(ctx context.Context, op string, err error) {
	if err == nil || errors.Is(err, redis.Nil) {
		return
	}
	c.log.WarnContext(ctx, "todo cache degraded", "op", op, "err", err)
}
