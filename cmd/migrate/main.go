// Command migrate applies the embedded goose migrations: migrate [up|down|status|reset].
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"echo-todo-2/internal/config"
	"echo-todo-2/internal/platform/postgres"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(log, os.Args[1:]); err != nil {
		log.Error("migrate failed", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, args []string) error {
	cmd := "up"
	if len(args) > 0 {
		cmd = args[0]
	}
	url, err := config.DatabaseURL()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var pool, connErr = postgres.New(ctx, url)
	for i := 0; connErr != nil && i < 10; i++ { // tolerate a database that is still starting (compose)
		log.Warn("database not ready, retrying", "err", connErr)
		time.Sleep(2 * time.Second)
		pool, connErr = postgres.New(ctx, url)
	}
	if connErr != nil {
		return connErr
	}
	defer pool.Close()

	switch cmd {
	case "up":
		return postgres.MigrateUp(ctx, pool, log)
	case "down":
		return postgres.MigrateDown(ctx, pool, log)
	case "reset":
		return postgres.MigrateReset(ctx, pool)
	case "status":
		return postgres.MigrationStatus(ctx, pool, log)
	default:
		return fmt.Errorf("unknown command %q (want up|down|status|reset)", cmd)
	}
}
