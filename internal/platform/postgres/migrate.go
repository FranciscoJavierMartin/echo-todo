package postgres

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"echo-todo-2/db/migrations"
)

func newProvider(pool *pgxpool.Pool) (*goose.Provider, func() error, error) {
	sqlDB := stdlib.OpenDBFromPool(pool)
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		_ = sqlDB.Close()
		return nil, nil, fmt.Errorf("goose provider: %w", err)
	}
	return p, sqlDB.Close, nil
}

// MigrateUp applies all pending migrations.
func MigrateUp(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	p, closeFn, err := newProvider(pool)
	if err != nil {
		return err
	}
	defer closeFn()
	results, err := p.Up(ctx)
	for _, r := range results {
		log.Info("migration applied", "version", r.Source.Version, "file", r.Source.Path, "duration", r.Duration)
	}
	return err
}

// MigrateDown rolls back the most recent migration.
func MigrateDown(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	p, closeFn, err := newProvider(pool)
	if err != nil {
		return err
	}
	defer closeFn()
	r, err := p.Down(ctx)
	if r != nil {
		log.Info("migration rolled back", "version", r.Source.Version, "file", r.Source.Path)
	}
	return err
}

// MigrateReset rolls back every migration.
func MigrateReset(ctx context.Context, pool *pgxpool.Pool) error {
	p, closeFn, err := newProvider(pool)
	if err != nil {
		return err
	}
	defer closeFn()
	_, err = p.DownTo(ctx, 0)
	return err
}

// MigrationStatus logs applied/pending migrations.
func MigrationStatus(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	p, closeFn, err := newProvider(pool)
	if err != nil {
		return err
	}
	defer closeFn()
	statuses, err := p.Status(ctx)
	if err != nil {
		return err
	}
	for _, s := range statuses {
		log.Info("migration", "version", s.Source.Version, "file", s.Source.Path, "state", s.State, "applied_at", s.AppliedAt)
	}
	return nil
}
