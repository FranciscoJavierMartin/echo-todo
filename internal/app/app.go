// Package app wires repositories, services and handlers into an HTTP server.
// cmd/api and the end-to-end tests share it so tests exercise the real chain.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
	"github.com/redis/go-redis/v9"

	"echo-todo-2/internal/auth"
	authhandler "echo-todo-2/internal/auth/handler"
	authrepo "echo-todo-2/internal/auth/pgrepo"
	"echo-todo-2/internal/config"
	"echo-todo-2/internal/idempotency"
	"echo-todo-2/internal/outbox"
	"echo-todo-2/internal/platform/postgres"
	"echo-todo-2/internal/platform/storage"
	"echo-todo-2/internal/platform/telemetry"
	"echo-todo-2/internal/server"
	"echo-todo-2/internal/todo"
	todohandler "echo-todo-2/internal/todo/handler"
	"echo-todo-2/internal/todo/pgrepo"
	"echo-todo-2/internal/todo/rediscache"
	"echo-todo-2/web"
)

type App struct {
	Echo   *echo.Echo
	Tokens *auth.Tokens
	Auth   *auth.Service
	Todos  *todo.Service
	Worker *outbox.Worker
}

// CacheTTL bounds how long a cached todo may be served.
const CacheTTL = 5 * time.Minute

type options struct {
	redis    *redis.Client
	notifier outbox.Handler
}

type Option func(*options)

// WithRedis enables the todo read cache.
func WithRedis(rdb *redis.Client) Option { return func(o *options) { o.redis = rdb } }

// WithNotifier replaces the outbox delivery handler (default: log).
func WithNotifier(h outbox.Handler) Option { return func(o *options) { o.notifier = h } }

func New(cfg config.Config, log *slog.Logger, pool *pgxpool.Pool, tel *telemetry.Telemetry, opts ...Option) (*App, error) {
	var o options
	for _, fn := range opts {
		fn(&o)
	}
	if o.notifier == nil {
		o.notifier = outbox.LogNotifier{Log: log}
	}
	txm := postgres.NewTxManager(pool)
	tokens := auth.NewTokens(cfg.JWTSecret, cfg.AccessTTL)

	params := auth.DefaultPasswordParams
	params.MemoryKiB, params.Iterations, params.Parallelism = cfg.Argon2MemoryKiB, cfg.Argon2Time, cfg.Argon2Threads
	authSvc, err := auth.NewService(authrepo.New(pool).WithQueryTimeout(cfg.DBQueryTimeout), txm, tokens, cfg.RefreshTTL, params)
	if err != nil {
		return nil, fmt.Errorf("auth service: %w", err)
	}
	todoRepo := pgrepo.New(pool).WithQueryTimeout(cfg.DBQueryTimeout)
	todoSvc := todo.NewService(todoRepo, txm, todoRepo).WithOutbox(outbox.NewStore(pool))
	if o.redis != nil {
		todoSvc.WithCache(rediscache.New(o.redis, CacheTTL, log))
	}
	idem := idempotency.NewStore(pool)
	worker := outbox.NewWorker(pool, o.notifier, log).WithMaintenance(time.Hour,
		func(ctx context.Context) error { _, err := idem.Purge(ctx); return err },
		outbox.PurgeProcessed(pool, 7*24*time.Hour),
	)

	blobs, err := storage.NewLocal(cfg.UploadDir)
	if err != nil {
		return nil, err
	}
	attSvc := todo.NewAttachmentService(todoSvc, todoRepo, blobs, cfg.MaxUploadBytes)

	e := server.New(server.Deps{
		Config:    cfg,
		Logger:    log,
		Tokens:    tokens,
		APIKeys:   authSvc,
		Idem:      idem,
		Telemetry: tel,
		Auth:      authhandler.New(authSvc, cfg.Env != "dev" && cfg.Env != "test", cfg.AccessTTL, cfg.RefreshTTL),
		Todos:     todohandler.New(todoSvc).WithAttachments(attSvc),
		Files:     blobs.FS(),
		UI:        web.FS,
	})
	return &App{Echo: e, Tokens: tokens, Auth: authSvc, Todos: todoSvc, Worker: worker}, nil
}
