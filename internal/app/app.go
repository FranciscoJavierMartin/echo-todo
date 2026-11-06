// Package app wires repositories, services and handlers into an HTTP server.
// cmd/api and the end-to-end tests share it so tests exercise the real chain.
package app

import (
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"echo-todo-2/internal/auth"
	authhandler "echo-todo-2/internal/auth/handler"
	authrepo "echo-todo-2/internal/auth/pgrepo"
	"echo-todo-2/internal/config"
	"echo-todo-2/internal/platform/postgres"
	"echo-todo-2/internal/platform/telemetry"
	"echo-todo-2/internal/server"
	"echo-todo-2/internal/todo"
	todohandler "echo-todo-2/internal/todo/handler"
	"echo-todo-2/internal/todo/pgrepo"
)

type App struct {
	Echo   *echo.Echo
	Tokens *auth.Tokens
	Auth   *auth.Service
	Todos  *todo.Service
}

func New(cfg config.Config, log *slog.Logger, pool *pgxpool.Pool, tel *telemetry.Telemetry) (*App, error) {
	txm := postgres.NewTxManager(pool)
	tokens := auth.NewTokens(cfg.JWTSecret, cfg.AccessTTL)

	params := auth.DefaultPasswordParams
	params.MemoryKiB, params.Iterations, params.Parallelism = cfg.Argon2MemoryKiB, cfg.Argon2Time, cfg.Argon2Threads
	authSvc, err := auth.NewService(authrepo.New(pool), txm, tokens, cfg.RefreshTTL, params)
	if err != nil {
		return nil, fmt.Errorf("auth service: %w", err)
	}
	todoRepo := pgrepo.New(pool)
	todoSvc := todo.NewService(todoRepo, txm, todoRepo)

	e := server.New(server.Deps{
		Config:    cfg,
		Logger:    log,
		Tokens:    tokens,
		APIKeys:   authSvc,
		Telemetry: tel,
		Auth:      authhandler.New(authSvc, cfg.Env != "dev" && cfg.Env != "test", cfg.AccessTTL, cfg.RefreshTTL),
		Todos:     todohandler.New(todoSvc),
	})
	return &App{Echo: e, Tokens: tokens, Auth: authSvc, Todos: todoSvc}, nil
}
