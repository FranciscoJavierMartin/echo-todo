package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"echo-todo-2/internal/app"
	"echo-todo-2/internal/config"
	"echo-todo-2/internal/platform/logger"
	"echo-todo-2/internal/platform/postgres"
	"echo-todo-2/internal/platform/redisx"
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

	var opts []app.Option
	if cfg.RedisURL != "" {
		rdb, err := redisx.New(ctx, cfg.RedisURL)
		if err != nil {
			return err
		}
		defer rdb.Close()
		opts = append(opts, app.WithRedis(rdb))
	} else {
		log.Info("REDIS_URL not set; todo cache disabled")
	}

	a, err := app.New(cfg, log, pool, tel, opts...)
	if err != nil {
		return err
	}

	// The worker shares the signal context with the server: SIGTERM stops both.
	var wg sync.WaitGroup
	wg.Go(func() {
		if err := a.Worker.Run(ctx); err != nil {
			log.Error("outbox worker", "err", err)
		}
	})

	sc := server.StartConfig(cfg, log)
	err = sc.Start(ctx, a.Echo)
	stop() // make sure the worker stops if the server failed on its own
	wg.Wait()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	log.Info("server stopped cleanly")
	return nil
}
