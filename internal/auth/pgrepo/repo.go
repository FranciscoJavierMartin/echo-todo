// Package pgrepo implements auth.Repo on PostgreSQL.
package pgrepo

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"echo-todo-2/internal/auth"
	"echo-todo-2/internal/db"
	"echo-todo-2/internal/platform/postgres"
)

type Repo struct {
	pool *pgxpool.Pool
}

var _ auth.Repo = (*Repo)(nil)

func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

func (r *Repo) q(ctx context.Context) *db.Queries { return db.New(postgres.Conn(ctx, r.pool)) }

func notFound(err error, what string) error {
	if postgres.IsNoRows(err) {
		return auth.ErrNotFound
	}
	return fmt.Errorf("%s: %w", what, err)
}

func userFromRow(u db.User) *auth.User {
	return &auth.User{ID: u.ID, Email: u.Email, PasswordHash: u.PasswordHash, DisplayName: u.DisplayName, Role: u.Role, CreatedAt: u.CreatedAt}
}

func (r *Repo) CreateUser(ctx context.Context, u *auth.User) error {
	row, err := r.q(ctx).CreateUser(ctx, db.CreateUserParams{
		Email: u.Email, PasswordHash: u.PasswordHash, DisplayName: u.DisplayName, Role: u.Role,
	})
	if err != nil {
		if postgres.ErrCode(err) == postgres.CodeUniqueViolation {
			return auth.ErrDuplicate
		}
		return fmt.Errorf("create user: %w", err)
	}
	*u = *userFromRow(row)
	return nil
}

func (r *Repo) UserByEmail(ctx context.Context, email string) (*auth.User, error) {
	row, err := r.q(ctx).GetUserByEmail(ctx, email)
	if err != nil {
		return nil, notFound(err, "user by email")
	}
	return userFromRow(row), nil
}

func (r *Repo) UserByID(ctx context.Context, id uuid.UUID) (*auth.User, error) {
	row, err := r.q(ctx).GetUserByID(ctx, id)
	if err != nil {
		return nil, notFound(err, "user by id")
	}
	return userFromRow(row), nil
}

func (r *Repo) UpdatePasswordHash(ctx context.Context, id uuid.UUID, hash string) error {
	return r.q(ctx).UpdateUserPasswordHash(ctx, db.UpdateUserPasswordHashParams{ID: id, PasswordHash: hash})
}

func (r *Repo) InsertRefreshToken(ctx context.Context, t *auth.RefreshToken, userAgent string) error {
	row, err := r.q(ctx).InsertRefreshToken(ctx, db.InsertRefreshTokenParams{
		UserID: t.UserID, FamilyID: t.FamilyID, TokenHash: t.Hash, ExpiresAt: t.ExpiresAt, UserAgent: userAgent,
	})
	if err != nil {
		return fmt.Errorf("insert refresh token: %w", err)
	}
	t.ID = row.ID
	return nil
}

func (r *Repo) RefreshTokenByHashForUpdate(ctx context.Context, hash []byte) (*auth.RefreshToken, error) {
	row, err := r.q(ctx).GetRefreshTokenByHashForUpdate(ctx, hash)
	if err != nil {
		return nil, notFound(err, "refresh token")
	}
	return &auth.RefreshToken{
		ID: row.ID, UserID: row.UserID, FamilyID: row.FamilyID, Hash: row.TokenHash,
		ExpiresAt: row.ExpiresAt, RevokedAt: row.RevokedAt, ReplacedBy: row.ReplacedBy,
	}, nil
}

func (r *Repo) MarkRefreshTokenRotated(ctx context.Context, id, replacedBy uuid.UUID) error {
	return r.q(ctx).MarkRefreshTokenRotated(ctx, db.MarkRefreshTokenRotatedParams{ID: id, ReplacedBy: &replacedBy})
}

func (r *Repo) RevokeRefreshFamily(ctx context.Context, familyID uuid.UUID) error {
	_, err := r.q(ctx).RevokeRefreshFamily(ctx, familyID)
	return err
}

func (r *Repo) RevokeAllRefreshTokensForUser(ctx context.Context, userID uuid.UUID) error {
	_, err := r.q(ctx).RevokeAllRefreshTokensForUser(ctx, userID)
	return err
}

func (r *Repo) InsertAPIKey(ctx context.Context, k *auth.APIKey) error {
	row, err := r.q(ctx).InsertAPIKey(ctx, db.InsertAPIKeyParams{UserID: k.UserID, Name: k.Name, Prefix: k.Prefix, KeyHash: k.Hash})
	if err != nil {
		return fmt.Errorf("insert api key: %w", err)
	}
	k.ID, k.CreatedAt = row.ID, row.CreatedAt
	return nil
}

func (r *Repo) APIKeyByPrefix(ctx context.Context, prefix string) (*auth.APIKey, error) {
	row, err := r.q(ctx).GetAPIKeyByPrefix(ctx, prefix)
	if err != nil {
		return nil, notFound(err, "api key")
	}
	return &auth.APIKey{
		ID: row.ID, UserID: row.UserID, Name: row.Name, Prefix: row.Prefix, Hash: row.KeyHash,
		Role: row.Role, LastUsedAt: row.LastUsedAt, RevokedAt: row.RevokedAt, CreatedAt: row.CreatedAt,
	}, nil
}

func (r *Repo) TouchAPIKey(ctx context.Context, id uuid.UUID) error {
	return r.q(ctx).TouchAPIKey(ctx, id)
}

func (r *Repo) ListAPIKeys(ctx context.Context, userID uuid.UUID) ([]auth.APIKey, error) {
	rows, err := r.q(ctx).ListAPIKeys(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	out := make([]auth.APIKey, len(rows))
	for i, row := range rows {
		out[i] = auth.APIKey{ID: row.ID, UserID: row.UserID, Name: row.Name, Prefix: row.Prefix,
			LastUsedAt: row.LastUsedAt, RevokedAt: row.RevokedAt, CreatedAt: row.CreatedAt}
	}
	return out, nil
}

func (r *Repo) RevokeAPIKey(ctx context.Context, userID, id uuid.UUID) error {
	n, err := r.q(ctx).RevokeAPIKey(ctx, db.RevokeAPIKeyParams{ID: id, UserID: userID})
	if err != nil {
		return fmt.Errorf("revoke api key: %w", err)
	}
	if n == 0 {
		return auth.ErrNotFound
	}
	return nil
}
