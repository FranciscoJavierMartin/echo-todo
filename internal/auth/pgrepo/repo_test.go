package pgrepo_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/auth"
	"echo-todo-2/internal/auth/pgrepo"
	"echo-todo-2/internal/platform/postgres/pgtest"
)

func TestUsersAndPasswordHash(t *testing.T) {
	pool := pgtest.New(t)
	repo := pgrepo.New(pool).WithQueryTimeout(2 * time.Second)
	ctx := context.Background()

	u := &auth.User{Email: "a@example.com", PasswordHash: "h1", DisplayName: "A", Role: auth.RoleUser}
	require.NoError(t, repo.CreateUser(ctx, u))
	require.ErrorIs(t, repo.CreateUser(ctx, &auth.User{Email: "A@EXAMPLE.COM", PasswordHash: "h", DisplayName: "B", Role: auth.RoleUser}),
		auth.ErrDuplicate, "citext makes emails case-insensitive")

	require.NoError(t, repo.UpdatePasswordHash(ctx, u.ID, "h2"))
	got, err := repo.UserByEmail(ctx, "a@example.com")
	require.NoError(t, err)
	require.Equal(t, "h2", got.PasswordHash)

	_, err = repo.UserByEmail(ctx, "nobody@example.com")
	require.ErrorIs(t, err, auth.ErrNotFound)
	_, err = repo.UserByID(ctx, uuid.New())
	require.ErrorIs(t, err, auth.ErrNotFound)
	_, err = repo.RefreshTokenByHashForUpdate(ctx, []byte("missing"))
	require.ErrorIs(t, err, auth.ErrNotFound)
	_, err = repo.APIKeyByPrefix(ctx, "missing")
	require.ErrorIs(t, err, auth.ErrNotFound)
	require.ErrorIs(t, repo.RevokeAPIKey(ctx, u.ID, uuid.New()), auth.ErrNotFound)
}

func TestErrorsWhenDatabaseIsGone(t *testing.T) {
	pool := pgtest.New(t)
	repo := pgrepo.New(pool)
	pool.Close()
	ctx := context.Background()
	id := uuid.New()

	require.Error(t, repo.CreateUser(ctx, &auth.User{Email: "x@example.com", PasswordHash: "h", DisplayName: "X", Role: auth.RoleUser}))
	_, err := repo.UserByEmail(ctx, "x@example.com")
	require.Error(t, err)
	require.NotErrorIs(t, err, auth.ErrNotFound)
	require.Error(t, repo.InsertRefreshToken(ctx, &auth.RefreshToken{UserID: id, FamilyID: id, Hash: []byte("h"), ExpiresAt: time.Now()}, ""))
	require.Error(t, repo.InsertAPIKey(ctx, &auth.APIKey{UserID: id, Name: "k", Prefix: "p", Hash: []byte("h")}))
	_, err = repo.ListAPIKeys(ctx, id)
	require.Error(t, err)
	require.Error(t, repo.RevokeAPIKey(ctx, id, id))
}
