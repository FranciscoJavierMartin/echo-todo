package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/apperr"
	"echo-todo-2/internal/auth"
	"echo-todo-2/internal/auth/authtest"
)

var cheap = auth.PasswordParams{MemoryKiB: 1024, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}

const pw = "correct horse battery"

func newSvc(t *testing.T) (*auth.Service, *authtest.Repo) {
	t.Helper()
	repo := authtest.NewRepo()
	svc, err := auth.NewService(repo, authtest.NoTx{}, auth.NewTokens([]byte("0123456789abcdef0123456789abcdef"), time.Minute), time.Hour, cheap)
	require.NoError(t, err)
	return svc, repo
}

func code(t *testing.T, err error) string {
	t.Helper()
	var ae *apperr.Error
	require.ErrorAs(t, err, &ae)
	return ae.MachineCode()
}

func TestRegister(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	u, err := svc.Register(ctx, "  Alice@Example.COM ", pw, " Alice ")
	require.NoError(t, err)
	require.Equal(t, "alice@example.com", u.Email)
	require.Equal(t, "Alice", u.DisplayName)
	require.Equal(t, auth.RoleUser, u.Role)
	require.True(t, strings.HasPrefix(u.PasswordHash, "$argon2id$"))

	_, err = svc.Register(ctx, "ALICE@example.com", pw, "Dup")
	require.True(t, apperr.Is(err, apperr.KindConflict))
	require.Equal(t, "email_taken", code(t, err))

	_, err = svc.Register(ctx, "bad", "short", "")
	var ae *apperr.Error
	require.ErrorAs(t, err, &ae)
	require.Len(t, ae.Fields, 3)
}

func TestLoginGenericFailure(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	_, err := svc.Register(ctx, "bob@example.com", pw, "Bob")
	require.NoError(t, err)

	_, errUnknown := svc.Login(ctx, "nobody@example.com", pw, "")
	_, errWrong := svc.Login(ctx, "bob@example.com", "wrong password!", "")
	require.Equal(t, "invalid_credentials", code(t, errUnknown))
	require.Equal(t, "invalid_credentials", code(t, errWrong))
	var a, b *apperr.Error
	errors.As(errUnknown, &a)
	errors.As(errWrong, &b)
	require.Equal(t, a.Message, b.Message, "no user enumeration through messages")
}

func TestLoginRefreshRotationAndReuse(t *testing.T) {
	svc, repo := newSvc(t)
	ctx := context.Background()
	u, _ := svc.Register(ctx, "carol@example.com", pw, "Carol")

	p1, err := svc.Login(ctx, "CAROL@example.com", pw, "ua")
	require.NoError(t, err)
	require.NotEmpty(t, p1.AccessToken)
	require.GreaterOrEqual(t, len(p1.RefreshToken), 52, "≥ 32 bytes of entropy")

	p2, err := svc.Refresh(ctx, p1.RefreshToken, "ua")
	require.NoError(t, err)
	require.NotEqual(t, p1.RefreshToken, p2.RefreshToken)
	require.Equal(t, 1, repo.ActiveRefreshTokens(u.ID))

	// Reusing the rotated token revokes the whole family, including p2.
	_, err = svc.Refresh(ctx, p1.RefreshToken, "ua")
	require.Equal(t, "refresh_token_reused", code(t, err))
	require.Equal(t, 0, repo.ActiveRefreshTokens(u.ID))
	_, err = svc.Refresh(ctx, p2.RefreshToken, "ua")
	require.True(t, apperr.Is(err, apperr.KindUnauthenticated))

	_, err = svc.Refresh(ctx, "", "")
	require.Equal(t, "invalid_refresh_token", code(t, err))
	_, err = svc.Refresh(ctx, "unknown", "")
	require.Equal(t, "invalid_refresh_token", code(t, err))
}

func TestLogoutAndLogoutAll(t *testing.T) {
	svc, repo := newSvc(t)
	ctx := context.Background()
	u, _ := svc.Register(ctx, "dan@example.com", pw, "Dan")
	a, _ := svc.Login(ctx, "dan@example.com", pw, "")
	b, _ := svc.Login(ctx, "dan@example.com", pw, "")
	require.Equal(t, 2, repo.ActiveRefreshTokens(u.ID))

	require.NoError(t, svc.Logout(ctx, a.RefreshToken))
	require.NoError(t, svc.Logout(ctx, a.RefreshToken), "idempotent")
	require.NoError(t, svc.Logout(ctx, ""))
	_, err := svc.Refresh(ctx, a.RefreshToken, "")
	require.Equal(t, "invalid_refresh_token", code(t, err))
	require.Equal(t, 1, repo.ActiveRefreshTokens(u.ID))

	require.NoError(t, svc.LogoutAll(ctx, u.ID))
	_, err = svc.Refresh(ctx, b.RefreshToken, "")
	require.True(t, apperr.Is(err, apperr.KindUnauthenticated))
}

func TestMe(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	u, _ := svc.Register(ctx, "eve@example.com", pw, "Eve")
	got, err := svc.Me(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, u.Email, got.Email)
	_, err = svc.Me(ctx, uuid.New())
	require.True(t, apperr.Is(err, apperr.KindNotFound))
}

func TestAPIKeys(t *testing.T) {
	svc, repo := newSvc(t)
	ctx := context.Background()
	u, _ := svc.Register(ctx, "frank@example.com", pw, "Frank")
	repo.SetRole(u.ID, auth.RoleAdmin)

	raw, k, err := svc.CreateAPIKey(ctx, u.ID, " ci ")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(raw, "todo_"+k.Prefix+"_"))
	require.Equal(t, "ci", k.Name)

	p, err := svc.AuthenticateAPIKey(ctx, raw)
	require.NoError(t, err)
	require.Equal(t, &auth.Principal{UserID: u.ID, Role: auth.RoleAdmin, Via: "api_key"}, p)
	require.Equal(t, 1, repo.Touched)

	for _, bad := range []string{"", "nope", "todo_" + k.Prefix + "_wrongsecret", "todo_unknown_x", "xxx_" + k.Prefix + "_y"} {
		p, err := svc.AuthenticateAPIKey(ctx, bad)
		require.NoError(t, err)
		require.Nil(t, p, bad)
	}

	keys, err := svc.ListAPIKeys(ctx, u.ID)
	require.NoError(t, err)
	require.Len(t, keys, 1)

	require.True(t, apperr.Is(svc.RevokeAPIKey(ctx, uuid.New(), k.ID), apperr.KindNotFound), "other users cannot revoke")
	require.NoError(t, svc.RevokeAPIKey(ctx, u.ID, k.ID))
	p, err = svc.AuthenticateAPIKey(ctx, raw)
	require.NoError(t, err)
	require.Nil(t, p, "revoked keys stop working")

	_, _, err = svc.CreateAPIKey(ctx, u.ID, "  ")
	require.True(t, apperr.Is(err, apperr.KindInvalid))
}

func TestRehashOnLogin(t *testing.T) {
	repo := authtest.NewRepo()
	weak, _ := auth.NewService(repo, authtest.NoTx{}, auth.NewTokens([]byte("0123456789abcdef0123456789abcdef"), time.Minute), time.Hour, cheap)
	ctx := context.Background()
	u, _ := weak.Register(ctx, "gina@example.com", pw, "Gina")
	stronger := cheap
	stronger.Iterations = 2
	strong, _ := auth.NewService(repo, authtest.NoTx{}, auth.NewTokens([]byte("0123456789abcdef0123456789abcdef"), time.Minute), time.Hour, stronger)
	_, err := strong.Login(ctx, "gina@example.com", pw, "")
	require.NoError(t, err)
	got, _ := repo.UserByID(ctx, u.ID)
	require.Contains(t, got.PasswordHash, "t=2", "hash upgraded to current parameters")
}

func TestStorageFailuresAreInternal(t *testing.T) {
	svc, repo := newSvc(t)
	ctx := context.Background()
	u, _ := svc.Register(ctx, "hal@example.com", pw, "Hal")
	pair, _ := svc.Login(ctx, "hal@example.com", pw, "")
	repo.Err = errors.New("db down")

	_, err := svc.Register(ctx, "x@example.com", pw, "X")
	require.True(t, apperr.Is(err, apperr.KindInternal))
	_, err = svc.Login(ctx, "hal@example.com", pw, "")
	require.True(t, apperr.Is(err, apperr.KindInternal))
	_, err = svc.Refresh(ctx, pair.RefreshToken, "")
	require.True(t, apperr.Is(err, apperr.KindInternal))
	require.True(t, apperr.Is(svc.Logout(ctx, pair.RefreshToken), apperr.KindInternal))
	require.True(t, apperr.Is(svc.LogoutAll(ctx, u.ID), apperr.KindInternal))
	_, err = svc.Me(ctx, u.ID)
	require.True(t, apperr.Is(err, apperr.KindInternal))
	_, _, err = svc.CreateAPIKey(ctx, u.ID, "k")
	require.True(t, apperr.Is(err, apperr.KindInternal))
	_, err = svc.ListAPIKeys(ctx, u.ID)
	require.True(t, apperr.Is(err, apperr.KindInternal))
	require.True(t, apperr.Is(svc.RevokeAPIKey(ctx, u.ID, uuid.New()), apperr.KindInternal))
	_, err = svc.AuthenticateAPIKey(ctx, "todo_abc_def")
	require.True(t, apperr.Is(err, apperr.KindInternal))
}
