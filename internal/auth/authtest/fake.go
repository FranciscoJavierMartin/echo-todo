// Package authtest provides an in-memory auth.Repo for unit tests.
package authtest

import (
	"bytes"
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"echo-todo-2/internal/auth"
)

type Repo struct {
	mu      sync.Mutex
	users   map[uuid.UUID]*auth.User
	tokens  map[uuid.UUID]*auth.RefreshToken
	keys    map[uuid.UUID]*auth.APIKey
	Err     error // forced failure for every call
	Touched int
}

var _ auth.Repo = (*Repo)(nil)

func NewRepo() *Repo {
	return &Repo{users: map[uuid.UUID]*auth.User{}, tokens: map[uuid.UUID]*auth.RefreshToken{}, keys: map[uuid.UUID]*auth.APIKey{}}
}

func (r *Repo) lock() func() { r.mu.Lock(); return r.mu.Unlock }

func (r *Repo) CreateUser(_ context.Context, u *auth.User) error {
	defer r.lock()()
	if r.Err != nil {
		return r.Err
	}
	for _, e := range r.users {
		if e.Email == u.Email {
			return auth.ErrDuplicate
		}
	}
	u.ID, u.CreatedAt = uuid.New(), time.Now()
	c := *u
	r.users[u.ID] = &c
	return nil
}

func (r *Repo) UserByEmail(_ context.Context, email string) (*auth.User, error) {
	defer r.lock()()
	if r.Err != nil {
		return nil, r.Err
	}
	for _, u := range r.users {
		if u.Email == email {
			c := *u
			return &c, nil
		}
	}
	return nil, auth.ErrNotFound
}

func (r *Repo) UserByID(_ context.Context, id uuid.UUID) (*auth.User, error) {
	defer r.lock()()
	if r.Err != nil {
		return nil, r.Err
	}
	u, ok := r.users[id]
	if !ok {
		return nil, auth.ErrNotFound
	}
	c := *u
	return &c, nil
}

func (r *Repo) UpdatePasswordHash(_ context.Context, id uuid.UUID, hash string) error {
	defer r.lock()()
	if u, ok := r.users[id]; ok {
		u.PasswordHash = hash
	}
	return r.Err
}

// SetRole changes a user's role (admin fixtures).
func (r *Repo) SetRole(id uuid.UUID, role string) {
	defer r.lock()()
	r.users[id].Role = role
}

func (r *Repo) InsertRefreshToken(_ context.Context, t *auth.RefreshToken, _ string) error {
	defer r.lock()()
	if r.Err != nil {
		return r.Err
	}
	t.ID = uuid.New()
	c := *t
	r.tokens[t.ID] = &c
	return nil
}

func (r *Repo) RefreshTokenByHashForUpdate(_ context.Context, hash []byte) (*auth.RefreshToken, error) {
	defer r.lock()()
	if r.Err != nil {
		return nil, r.Err
	}
	for _, t := range r.tokens {
		if bytes.Equal(t.Hash, hash) {
			c := *t
			return &c, nil
		}
	}
	return nil, auth.ErrNotFound
}

func (r *Repo) MarkRefreshTokenRotated(_ context.Context, id, replacedBy uuid.UUID) error {
	defer r.lock()()
	now := time.Now()
	t := r.tokens[id]
	t.RevokedAt, t.ReplacedBy = &now, &replacedBy
	return nil
}

func (r *Repo) RevokeRefreshFamily(_ context.Context, familyID uuid.UUID) error {
	defer r.lock()()
	now := time.Now()
	for _, t := range r.tokens {
		if t.FamilyID == familyID && t.RevokedAt == nil {
			t.RevokedAt = &now
		}
	}
	return nil
}

func (r *Repo) RevokeAllRefreshTokensForUser(_ context.Context, userID uuid.UUID) error {
	defer r.lock()()
	if r.Err != nil {
		return r.Err
	}
	now := time.Now()
	for _, t := range r.tokens {
		if t.UserID == userID && t.RevokedAt == nil {
			t.RevokedAt = &now
		}
	}
	return nil
}

// ActiveRefreshTokens counts unrevoked tokens of a user.
func (r *Repo) ActiveRefreshTokens(userID uuid.UUID) int {
	defer r.lock()()
	n := 0
	for _, t := range r.tokens {
		if t.UserID == userID && t.RevokedAt == nil {
			n++
		}
	}
	return n
}

func (r *Repo) InsertAPIKey(_ context.Context, k *auth.APIKey) error {
	defer r.lock()()
	if r.Err != nil {
		return r.Err
	}
	k.ID, k.CreatedAt = uuid.New(), time.Now()
	c := *k
	r.keys[k.ID] = &c
	return nil
}

func (r *Repo) APIKeyByPrefix(_ context.Context, prefix string) (*auth.APIKey, error) {
	defer r.lock()()
	if r.Err != nil {
		return nil, r.Err
	}
	for _, k := range r.keys {
		if k.Prefix == prefix {
			c := *k
			if u, ok := r.users[k.UserID]; ok {
				c.Role = u.Role
			}
			return &c, nil
		}
	}
	return nil, auth.ErrNotFound
}

func (r *Repo) TouchAPIKey(context.Context, uuid.UUID) error {
	defer r.lock()()
	r.Touched++
	return nil
}

func (r *Repo) ListAPIKeys(_ context.Context, userID uuid.UUID) ([]auth.APIKey, error) {
	defer r.lock()()
	if r.Err != nil {
		return nil, r.Err
	}
	var out []auth.APIKey
	for _, k := range r.keys {
		if k.UserID == userID {
			out = append(out, *k)
		}
	}
	return out, nil
}

func (r *Repo) RevokeAPIKey(_ context.Context, userID, id uuid.UUID) error {
	defer r.lock()()
	if r.Err != nil {
		return r.Err
	}
	k, ok := r.keys[id]
	if !ok || k.UserID != userID || k.RevokedAt != nil {
		return auth.ErrNotFound
	}
	now := time.Now()
	k.RevokedAt = &now
	return nil
}

// NoTx runs fn directly.
type NoTx struct{}

func (NoTx) WithTx(ctx context.Context, fn func(ctx context.Context) error) error { return fn(ctx) }
