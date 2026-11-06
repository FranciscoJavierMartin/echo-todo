package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"echo-todo-2/internal/apperr"
)

const (
	MinPasswordLen = 10
	MaxPasswordLen = 128
	apiKeyPrefix   = "todo"
)

// TokenPair is what login and refresh return.
type TokenPair struct {
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
	User             *User
}

type Service struct {
	repo       Repo
	tx         TxRunner
	tokens     *Tokens
	refreshTTL time.Duration
	params     PasswordParams
	dummyHash  string
	now        func() time.Time
}

func NewService(repo Repo, tx TxRunner, tokens *Tokens, refreshTTL time.Duration, params PasswordParams) (*Service, error) {
	// A real hash of a random password, verified on unknown-email logins so both
	// branches cost the same argon2 work (no user enumeration through timing).
	dummy, err := params.Hash(rand.Text())
	if err != nil {
		return nil, err
	}
	return &Service{repo: repo, tx: tx, tokens: tokens, refreshTTL: refreshTTL, params: params, dummyHash: dummy, now: time.Now}, nil
}

// NormalizeEmail trims and lower-cases an address.
func NormalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func (s *Service) Register(ctx context.Context, email, password, displayName string) (*User, error) {
	const op = "auth.Service.Register"
	email = NormalizeEmail(email)
	displayName = strings.TrimSpace(displayName)
	fields := map[string]string{}
	if n := len(password); n < MinPasswordLen || n > MaxPasswordLen {
		fields["password"] = "must be between 10 and 128 characters"
	}
	if displayName == "" || len([]rune(displayName)) > 80 {
		fields["display_name"] = "must be between 1 and 80 characters"
	}
	if !strings.Contains(email, "@") {
		fields["email"] = "must be a valid email address"
	}
	if len(fields) > 0 {
		return nil, apperr.InvalidFields(fields).WithOp(op)
	}
	hash, err := s.params.Hash(password)
	if err != nil {
		return nil, apperr.Internal(op, err)
	}
	u := &User{Email: email, PasswordHash: hash, DisplayName: displayName, Role: RoleUser}
	if err := s.repo.CreateUser(ctx, u); err != nil {
		if errors.Is(err, ErrDuplicate) {
			return nil, apperr.Conflict("email already registered").WithOp(op).WithCode("email_taken").Wrap(err)
		}
		return nil, apperr.Internal(op, err)
	}
	return u, nil
}

var errInvalidCredentials = apperr.Unauthenticated("invalid credentials").WithCode("invalid_credentials")

func (s *Service) Login(ctx context.Context, email, password, userAgent string) (*TokenPair, error) {
	const op = "auth.Service.Login"
	u, err := s.repo.UserByEmail(ctx, NormalizeEmail(email))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			_, _, _ = VerifyPassword(s.dummyHash, password, s.params)
			return nil, errInvalidCredentials.WithOp(op)
		}
		return nil, apperr.Internal(op, err)
	}
	ok, rehash, err := VerifyPassword(u.PasswordHash, password, s.params)
	if err != nil {
		return nil, apperr.Internal(op, err)
	}
	if !ok {
		return nil, errInvalidCredentials.WithOp(op)
	}
	if rehash {
		if h, herr := s.params.Hash(password); herr == nil {
			_ = s.repo.UpdatePasswordHash(ctx, u.ID, h) // best effort upgrade
		}
	}
	var pair *TokenPair
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		var err error
		pair, _, err = s.issue(ctx, u, uuid.New(), userAgent)
		return err
	})
	if err != nil {
		return nil, apperr.Internal(op, err)
	}
	return pair, nil
}

// issue creates a new refresh token in family plus an access token.
func (s *Service) issue(ctx context.Context, u *User, family uuid.UUID, userAgent string) (*TokenPair, uuid.UUID, error) {
	raw := rand.Text() + rand.Text() // ≥ 32 random bytes, base32
	rt := &RefreshToken{
		UserID:    u.ID,
		FamilyID:  family,
		Hash:      hashToken(raw),
		ExpiresAt: s.now().Add(s.refreshTTL),
	}
	if err := s.repo.InsertRefreshToken(ctx, rt, truncate(userAgent, 200)); err != nil {
		return nil, uuid.Nil, err
	}
	access, exp, err := s.tokens.IssueAccess(u.ID, u.Role)
	if err != nil {
		return nil, uuid.Nil, err
	}
	return &TokenPair{AccessToken: access, AccessExpiresAt: exp, RefreshToken: raw, RefreshExpiresAt: rt.ExpiresAt, User: u}, rt.ID, nil
}

var errInvalidRefresh = apperr.Unauthenticated("invalid refresh token").WithCode("invalid_refresh_token")

// Refresh rotates a refresh token. Presenting a token that was already rotated
// means it leaked (or a client raced): the whole family is revoked.
func (s *Service) Refresh(ctx context.Context, raw, userAgent string) (*TokenPair, error) {
	const op = "auth.Service.Refresh"
	if raw == "" {
		return nil, errInvalidRefresh.WithOp(op)
	}
	var (
		pair  *TokenPair
		reuse bool
	)
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		rt, err := s.repo.RefreshTokenByHashForUpdate(ctx, hashToken(raw))
		if err != nil {
			return err
		}
		if rt.RevokedAt != nil {
			if rt.ReplacedBy != nil {
				// Commit the family revocation; report the error after the tx.
				reuse = true
				return s.repo.RevokeRefreshFamily(ctx, rt.FamilyID)
			}
			return ErrNotFound
		}
		if !s.now().Before(rt.ExpiresAt) {
			return ErrNotFound
		}
		u, err := s.repo.UserByID(ctx, rt.UserID)
		if err != nil {
			return err
		}
		var newID uuid.UUID
		pair, newID, err = s.issue(ctx, u, rt.FamilyID, userAgent)
		if err != nil {
			return err
		}
		return s.repo.MarkRefreshTokenRotated(ctx, rt.ID, newID)
	})
	switch {
	case reuse && err == nil:
		return nil, apperr.Unauthenticated("refresh token reuse detected; session revoked").WithOp(op).WithCode("refresh_token_reused")
	case errors.Is(err, ErrNotFound):
		return nil, errInvalidRefresh.WithOp(op).Wrap(err)
	case err != nil:
		return nil, apperr.Internal(op, err)
	}
	return pair, nil
}

// Logout revokes the refresh token's whole family (the device session).
// Unknown tokens are ignored so logout is idempotent.
func (s *Service) Logout(ctx context.Context, raw string) error {
	const op = "auth.Service.Logout"
	if raw == "" {
		return nil
	}
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		rt, err := s.repo.RefreshTokenByHashForUpdate(ctx, hashToken(raw))
		if err != nil {
			return err
		}
		return s.repo.RevokeRefreshFamily(ctx, rt.FamilyID)
	})
	if err != nil && !errors.Is(err, ErrNotFound) {
		return apperr.Internal(op, err)
	}
	return nil
}

// LogoutAll revokes every session of a user.
func (s *Service) LogoutAll(ctx context.Context, userID uuid.UUID) error {
	if err := s.repo.RevokeAllRefreshTokensForUser(ctx, userID); err != nil {
		return apperr.Internal("auth.Service.LogoutAll", err)
	}
	return nil
}

func (s *Service) Me(ctx context.Context, userID uuid.UUID) (*User, error) {
	u, err := s.repo.UserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, apperr.NotFound("user not found").Wrap(err)
		}
		return nil, apperr.Internal("auth.Service.Me", err)
	}
	return u, nil
}

// CreateAPIKey returns the raw key exactly once; only its hash is stored.
// Format: todo_<prefix>_<secret> — the prefix is a lookup handle, not a secret.
func (s *Service) CreateAPIKey(ctx context.Context, userID uuid.UUID, name string) (string, *APIKey, error) {
	const op = "auth.Service.CreateAPIKey"
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 80 {
		return "", nil, apperr.Invalid("name", "must be between 1 and 80 characters").WithOp(op)
	}
	prefix := randomHex(6)
	secret := base64.RawURLEncoding.EncodeToString(randomBytes(32))
	raw := apiKeyPrefix + "_" + prefix + "_" + secret
	k := &APIKey{UserID: userID, Name: name, Prefix: prefix, Hash: hashToken(raw)}
	if err := s.repo.InsertAPIKey(ctx, k); err != nil {
		return "", nil, apperr.Internal(op, err)
	}
	return raw, k, nil
}

func (s *Service) ListAPIKeys(ctx context.Context, userID uuid.UUID) ([]APIKey, error) {
	keys, err := s.repo.ListAPIKeys(ctx, userID)
	if err != nil {
		return nil, apperr.Internal("auth.Service.ListAPIKeys", err)
	}
	return keys, nil
}

func (s *Service) RevokeAPIKey(ctx context.Context, userID, id uuid.UUID) error {
	if err := s.repo.RevokeAPIKey(ctx, userID, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return apperr.NotFound("api key not found").Wrap(err)
		}
		return apperr.Internal("auth.Service.RevokeAPIKey", err)
	}
	return nil
}

// AuthenticateAPIKey looks the key up by prefix and compares hashes in constant time.
func (s *Service) AuthenticateAPIKey(ctx context.Context, raw string) (*Principal, error) {
	const op = "auth.Service.AuthenticateAPIKey"
	parts := strings.SplitN(raw, "_", 3)
	if len(parts) != 3 || parts[0] != apiKeyPrefix {
		return nil, nil
	}
	k, err := s.repo.APIKeyByPrefix(ctx, parts[1])
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, apperr.Internal(op, err)
	}
	if subtle.ConstantTimeCompare(k.Hash, hashToken(raw)) != 1 || k.RevokedAt != nil {
		return nil, nil
	}
	_ = s.repo.TouchAPIKey(ctx, k.ID)
	return &Principal{UserID: k.UserID, Role: k.Role, Via: "api_key"}, nil
}

func hashToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

func randomHex(n int) string { return hex.EncodeToString(randomBytes(n)) }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
