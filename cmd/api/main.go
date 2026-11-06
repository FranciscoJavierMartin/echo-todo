package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"echo-todo-2/internal/app"
	"echo-todo-2/internal/config"
	"echo-todo-2/internal/platform/logger"
	"echo-todo-2/internal/platform/postgres"
	"echo-todo-2/internal/server"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := logger.New(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	a := app.New(cfg, log, pool)

	sc := server.StartConfig(cfg, log)
	if err := sc.Start(ctx, a.Echo); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	log.Info("server stopped cleanly")
	return nil
}
