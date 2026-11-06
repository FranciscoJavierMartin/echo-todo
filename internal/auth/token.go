package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
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
//
// Rotation: tokens carry a "kid" derived from the signing secret. Verification
// accepts the current secret plus any previous secrets (JWT_PREVIOUS_SECRETS),
// so a secret can be rotated without logging everyone out; drop the previous
// secret after one ACCESS_TTL. See docs/runbook.md.
type Tokens struct {
	secret []byte
	kid    string
	keys   map[string][]byte
	ttl    time.Duration
	now    func() time.Time
}

func NewTokens(secret []byte, ttl time.Duration) *Tokens {
	kid := KeyID(secret)
	return &Tokens{secret: secret, kid: kid, keys: map[string][]byte{kid: secret}, ttl: ttl, now: time.Now}
}

// WithPreviousSecrets returns a copy that also verifies tokens signed with old secrets.
func (t *Tokens) WithPreviousSecrets(old ...[]byte) *Tokens {
	c := *t
	c.keys = maps.Clone(t.keys)
	for _, s := range old {
		c.keys[KeyID(s)] = s
	}
	return &c
}

// KeyID is a non-reversible identifier for a secret (first 8 bytes of SHA-256).
func KeyID(secret []byte) string {
	sum := sha256.Sum256(secret)
	return hex.EncodeToString(sum[:8])
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
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tok.Header["kid"] = t.kid
	s, err := tok.SignedString(t.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign access token: %w", err)
	}
	return s, exp, nil
}

// Parse verifies signature, algorithm, issuer and expiry.
func (t *Tokens) Parse(raw string) (*jwt.Token, error) {
	return jwt.ParseWithClaims(raw, new(Claims), func(tok *jwt.Token) (any, error) {
		kid, _ := tok.Header["kid"].(string)
		if kid == "" {
			return t.secret, nil // tokens minted before kid existed
		}
		key, ok := t.keys[kid]
		if !ok {
			return nil, fmt.Errorf("unknown key id %q", kid)
		}
		return key, nil
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
