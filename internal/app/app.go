// Package app wires repositories, services and handlers into an HTTP server.
// cmd/api and the end-to-end tests share it so tests exercise the real chain.
package app

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"echo-todo-2/internal/auth"
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
}

func New(cfg config.Config, log *slog.Logger, pool *pgxpool.Pool, tel *telemetry.Telemetry) *App {
	txm := postgres.NewTxManager(pool)
	tokens := auth.NewTokens(cfg.JWTSecret, cfg.AccessTTL)

	todoSvc := todo.NewService(pgrepo.New(pool), txm)

	e := server.New(server.Deps{
		Config:    cfg,
		Logger:    log,
		Tokens:    tokens,
		Telemetry: tel,
		Todos:     todohandler.New(todoSvc),
	})
	return &App{Echo: e, Tokens: tokens}
}
