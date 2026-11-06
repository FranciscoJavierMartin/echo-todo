package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"echo-todo-2/internal/app"
	"echo-todo-2/internal/config"
	"echo-todo-2/internal/platform/logger"
	"echo-todo-2/internal/platform/postgres"
	"echo-todo-2/internal/platform/telemetry"
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

	tel, err := telemetry.New(ctx, "todo-api", cfg.Env, cfg.OTLPEndpoint)
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tel.Shutdown(shutdownCtx); err != nil {
			log.Error("telemetry shutdown", "err", err)
		}
	}()

	pool, err := postgres.New(ctx, cfg.DatabaseURL, tel.PGTracer(), postgres.WithStatementTimeout(2*cfg.DBQueryTimeout))
	if err != nil {
		return err
	}
	defer pool.Close()

	a, err := app.New(cfg, log, pool, tel)
	if err != nil {
		return err
	}

	sc := server.StartConfig(cfg, log)
	if err := sc.Start(ctx, a.Echo); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	log.Info("server stopped cleanly")
	return nil
}
