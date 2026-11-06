package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const Issuer = "echo-todo"

// Claims are the access token claims: sub, role, jti, exp (+ iat, iss).
type Claims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

// Tokens issues and verifies HS256 access tokens.
type Tokens struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

func NewTokens(secret []byte, ttl time.Duration) *Tokens {
	return &Tokens{secret: secret, ttl: ttl, now: time.Now}
}

// WithClock returns a copy using now as the time source (tests).
func (t *Tokens) WithClock(now func() time.Time) *Tokens {
	c := *t
	c.now = now
	return &c
}

func (t *Tokens) TTL() time.Duration { return t.ttl }

// IssueAccess returns a signed access token and its expiry.
func (t *Tokens) IssueAccess(userID uuid.UUID, role string) (string, time.Time, error) {
	now := t.now()
	exp := now.Add(t.ttl)
	claims := &Claims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			ID:        uuid.NewString(),
			Issuer:    Issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign access token: %w", err)
	}
	return s, exp, nil
}

// Parse verifies signature, algorithm, issuer and expiry.
func (t *Tokens) Parse(raw string) (*jwt.Token, error) {
	return jwt.ParseWithClaims(raw, new(Claims), func(tok *jwt.Token) (any, error) {
		return t.secret, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(Issuer),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithTimeFunc(t.now),
		jwt.WithLeeway(5*time.Second),
	)
}

// PrincipalFromToken extracts the caller from a verified token.
func PrincipalFromToken(tok *jwt.Token) (*Principal, error) {
	claims, ok := tok.Claims.(*Claims)
	if !ok {
		return nil, errors.New("unexpected claims type")
	}
	id, err := uuid.Parse(claims.Subject)
	if err != nil {
		return nil, fmt.Errorf("invalid subject: %w", err)
	}
	switch claims.Role {
	case RoleUser, RoleAdmin:
	default:
		return nil, fmt.Errorf("invalid role %q", claims.Role)
	}
	return &Principal{UserID: id, Role: claims.Role, Via: "jwt"}, nil
}
