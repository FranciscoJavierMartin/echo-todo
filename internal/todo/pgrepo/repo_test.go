package pgrepo_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/db"
	"echo-todo-2/internal/platform/postgres"
	"echo-todo-2/internal/platform/postgres/pgtest"
	"echo-todo-2/internal/todo"
	"echo-todo-2/internal/todo/pgrepo"
)

func newUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	u, err := db.New(pool).CreateUser(context.Background(), db.CreateUserParams{
		Email: uuid.NewString() + "@example.com", PasswordHash: "x", DisplayName: "Tester", Role: "user",
	})
	require.NoError(t, err)
	return u.ID
}

func newTodo(owner uuid.UUID, title string) *todo.Todo {
	return &todo.Todo{OwnerID: owner, Title: title, Status: todo.StatusOpen, Priority: 3}
}

func TestRepo_CreateGet(t *testing.T) {
	pool := pgtest.New(t)
	repo := pgrepo.New(pool)
	ctx := context.Background()
	owner := newUser(t, pool)

	due := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Microsecond)
	in := newTodo(owner, "buy milk")
	in.DueAt = &due
	require.NoError(t, repo.Create(ctx, in))
	require.NotEqual(t, uuid.Nil, in.ID)
	require.Equal(t, 1, in.Version)

	got, err := repo.Get(ctx, owner, in.ID)
	require.NoError(t, err)
	require.Equal(t, "buy milk", got.Title)
	require.True(t, due.Equal(*got.DueAt))

	_, err = repo.Get(ctx, newUser(t, pool), in.ID)
	require.ErrorIs(t, err, todo.ErrNotFound, "other owners must not see the todo")
}

func TestRepo_UpdateOptimisticLock(t *testing.T) {
	pool := pgtest.New(t)
	repo := pgrepo.New(pool)
	ctx := context.Background()
	owner := newUser(t, pool)
	in := newTodo(owner, "v1")
	require.NoError(t, repo.Create(ctx, in))

	in.Title = "v2"
	updated, err := repo.Update(ctx, in, 1)
	require.NoError(t, err)
	require.Equal(t, 2, updated.Version)
	require.Equal(t, "v2", updated.Title)

	in.Title = "stale"
	_, err = repo.Update(ctx, in, 1)
	require.ErrorIs(t, err, todo.ErrVersionConflict)

	missing := newTodo(owner, "x")
	missing.ID = uuid.New()
	_, err = repo.Update(ctx, missing, 1)
	require.ErrorIs(t, err, todo.ErrNotFound)
}

func TestRepo_SoftDelete(t *testing.T) {
	pool := pgtest.New(t)
	repo := pgrepo.New(pool)
	ctx := context.Background()
	owner := newUser(t, pool)
	in := newTodo(owner, "gone soon")
	require.NoError(t, repo.Create(ctx, in))

	require.ErrorIs(t, repo.SoftDelete(ctx, newUser(t, pool), in.ID), todo.ErrNotFound)
	require.NoError(t, repo.SoftDelete(ctx, owner, in.ID))
	_, err := repo.Get(ctx, owner, in.ID)
	require.ErrorIs(t, err, todo.ErrNotFound)
	require.ErrorIs(t, repo.SoftDelete(ctx, owner, in.ID), todo.ErrNotFound)
}

func TestRepo_ListFiltersByOwnerAndStatus(t *testing.T) {
	pool := pgtest.New(t)
	repo := pgrepo.New(pool)
	ctx := context.Background()
	owner, other := newUser(t, pool), newUser(t, pool)
	for i, s := range []todo.Status{todo.StatusOpen, todo.StatusOpen, todo.StatusDone} {
		td := newTodo(owner, "t")
		td.Status = s
		td.Priority = i + 1
		require.NoError(t, repo.Create(ctx, td))
	}
	require.NoError(t, repo.Create(ctx, newTodo(other, "not mine")))

	items, total, err := repo.List(ctx, todo.Filter{OwnerID: owner, Limit: 10})
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.Len(t, items, 3)

	items, total, err = repo.List(ctx, todo.Filter{OwnerID: owner, Statuses: []todo.Status{todo.StatusDone}, Limit: 10})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Equal(t, todo.StatusDone, items[0].Status)
}

func TestTxManager_RollbackOnError(t *testing.T) {
	pool := pgtest.New(t)
	repo := pgrepo.New(pool)
	txm := postgres.NewTxManager(pool)
	ctx := context.Background()
	owner := newUser(t, pool)

	var id uuid.UUID
	boom := errors.New("boom")
	err := txm.WithTx(ctx, func(ctx context.Context) error {
		td := newTodo(owner, "rolled back")
		if err := repo.Create(ctx, td); err != nil {
			return err
		}
		id = td.ID
		return boom
	})
	require.ErrorIs(t, err, boom)
	_, err = repo.Get(ctx, owner, id)
	require.ErrorIs(t, err, todo.ErrNotFound)
}

func TestRepo_QueryTimeout(t *testing.T) {
	pool := pgtest.New(t)
	repo := pgrepo.New(pool).WithQueryTimeout(time.Nanosecond)
	_, err := repo.Get(context.Background(), uuid.New(), uuid.New())
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
