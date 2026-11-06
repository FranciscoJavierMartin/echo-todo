package postgres_test

import (
	"context"
	"io/fs"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"echo-todo-2/db/migrations"
	"echo-todo-2/internal/platform/postgres/pgtest"
)

// One subtest per migration: from the previous version, apply it, roll it back,
// and apply it again. Cheap insurance that every Down really undoes its Up.
func TestEachMigrationUpDownUp(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = sqlDB.Close() })
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	require.NoError(t, err)

	_, err = p.DownTo(ctx, 0)
	require.NoError(t, err)

	files, err := fs.Glob(migrations.FS, "*.sql")
	require.NoError(t, err)
	sources := p.ListSources()
	require.Len(t, sources, len(files))

	for _, src := range sources {
		t.Run(src.Path, func(t *testing.T) {
			_, err := p.UpByOne(ctx)
			require.NoError(t, err, "up")
			_, err = p.Down(ctx)
			require.NoError(t, err, "down")
			r, err := p.UpByOne(ctx)
			require.NoError(t, err, "up again")
			require.Equal(t, src.Version, r.Source.Version)
		})
	}
	v, err := p.GetDBVersion(ctx)
	require.NoError(t, err)
	require.Equal(t, sources[len(sources)-1].Version, v)
}
