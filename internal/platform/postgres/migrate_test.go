package postgres_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/platform/postgres"
	"echo-todo-2/internal/platform/postgres/pgtest"
)

// Every migration must be reversible: down to zero, then up again, on a migrated database.
func TestMigrations_Reversible(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	require.NoError(t, postgres.MigrateReset(ctx, pool))
	var n int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM pg_tables WHERE schemaname='public' AND tablename='todos'").Scan(&n))
	require.Zero(t, n)

	require.NoError(t, postgres.MigrateUp(ctx, pool, log))
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM pg_tables WHERE schemaname='public' AND tablename='todos'").Scan(&n))
	require.Equal(t, 1, n)
}

func TestErrCode(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, "INSERT INTO users (email, password_hash, display_name) VALUES ('a@b.c','x','')")
	require.Equal(t, postgres.CodeCheckViolation, postgres.ErrCode(err))
	require.Empty(t, postgres.ErrCode(nil))
}
