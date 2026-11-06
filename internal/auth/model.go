package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID           uuid.UUID
	Email        string
	PasswordHash string
	DisplayName  string
	Role         string
	CreatedAt    time.Time
}

type RefreshToken struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	FamilyID   uuid.UUID
	Hash       []byte
	ExpiresAt  time.Time
	RevokedAt  *time.Time
	ReplacedBy *uuid.UUID
}

type APIKey struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Name       string
	Prefix     string
	Hash       []byte
	Role       string // owner's role, joined on lookup
	LastUsedAt *time.Time
	RevokedAt  *time.Time
	CreatedAt  time.Time
}

var (
	ErrNotFound  = errors.New("not found")
	ErrDuplicate = errors.New("duplicate")
)

// Repo is the auth storage contract. Methods join the transaction carried in ctx.
type Repo interface {
	CreateUser(ctx context.Context, u *User) error
	UserByEmail(ctx context.Context, email string) (*User, error)
	UserByID(ctx context.Context, id uuid.UUID) (*User, error)
	UpdatePasswordHash(ctx context.Context, id uuid.UUID, hash string) error

	InsertRefreshToken(ctx context.Context, t *RefreshToken, userAgent string) error
	// RefreshTokenByHashForUpdate locks the row for the rest of the transaction.
	RefreshTokenByHashForUpdate(ctx context.Context, hash []byte) (*RefreshToken, error)
	MarkRefreshTokenRotated(ctx context.Context, id, replacedBy uuid.UUID) error
	RevokeRefreshFamily(ctx context.Context, familyID uuid.UUID) error
	RevokeAllRefreshTokensForUser(ctx context.Context, userID uuid.UUID) error

	InsertAPIKey(ctx context.Context, k *APIKey) error
	APIKeyByPrefix(ctx context.Context, prefix string) (*APIKey, error)
	TouchAPIKey(ctx context.Context, id uuid.UUID) error
	ListAPIKeys(ctx context.Context, userID uuid.UUID) ([]APIKey, error)
	RevokeAPIKey(ctx context.Context, userID, id uuid.UUID) error
}

type TxRunner interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}
