package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

var secret = []byte("0123456789abcdef0123456789abcdef")

func TestIssueAndParse(t *testing.T) {
	tk := NewTokens(secret, 10*time.Minute)
	require.Equal(t, 10*time.Minute, tk.TTL())
	uid := uuid.New()
	raw, exp, err := tk.IssueAccess(uid, RoleAdmin)
	require.NoError(t, err)
	require.WithinDuration(t, time.Now().Add(10*time.Minute), exp, time.Second)

	tok, err := tk.Parse(raw)
	require.NoError(t, err)
	claims := tok.Claims.(*Claims)
	require.Equal(t, uid.String(), claims.Subject)
	require.Equal(t, RoleAdmin, claims.Role)
	require.NotEmpty(t, claims.ID, "jti")
	require.Equal(t, Issuer, claims.Issuer)

	p, err := PrincipalFromToken(tok)
	require.NoError(t, err)
	require.Equal(t, &Principal{UserID: uid, Role: RoleAdmin, Via: "jwt"}, p)
}

func TestParseRejects(t *testing.T) {
	tk := NewTokens(secret, time.Minute)
	sign := func(m jwt.SigningMethod, key any, c jwt.Claims) string {
		s, err := jwt.NewWithClaims(m, c).SignedString(key)
		require.NoError(t, err)
		return s
	}
	valid := func() *Claims {
		return &Claims{Role: RoleUser, RegisteredClaims: jwt.RegisteredClaims{
			Subject: uuid.NewString(), Issuer: Issuer,
			IssuedAt: jwt.NewNumericDate(time.Now()), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		}}
	}

	expired, _, _ := tk.WithClock(func() time.Time { return time.Now().Add(-time.Hour) }).IssueAccess(uuid.New(), RoleUser)
	_, err := tk.Parse(expired)
	require.ErrorIs(t, err, jwt.ErrTokenExpired)

	_, err = tk.Parse(sign(jwt.SigningMethodHS256, []byte("another-secret-another-secret-xx"), valid()))
	require.ErrorIs(t, err, jwt.ErrTokenSignatureInvalid)

	_, err = tk.Parse(sign(jwt.SigningMethodHS512, secret, valid()))
	require.Error(t, err, "algorithm must be pinned to HS256")

	none := sign(jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, valid())
	_, err = tk.Parse(none)
	require.Error(t, err)

	c := valid()
	c.Issuer = "evil"
	_, err = tk.Parse(sign(jwt.SigningMethodHS256, secret, c))
	require.ErrorIs(t, err, jwt.ErrTokenInvalidIssuer)

	c = valid()
	c.ExpiresAt = nil
	_, err = tk.Parse(sign(jwt.SigningMethodHS256, secret, c))
	require.Error(t, err, "exp is required")
}

func TestPrincipalFromTokenValidation(t *testing.T) {
	_, err := PrincipalFromToken(&jwt.Token{Claims: jwt.MapClaims{}})
	require.Error(t, err)
	_, err = PrincipalFromToken(&jwt.Token{Claims: &Claims{Role: RoleUser, RegisteredClaims: jwt.RegisteredClaims{Subject: "nope"}}})
	require.Error(t, err)
	_, err = PrincipalFromToken(&jwt.Token{Claims: &Claims{Role: "root", RegisteredClaims: jwt.RegisteredClaims{Subject: uuid.NewString()}}})
	require.Error(t, err)
}

func TestSecretRotation(t *testing.T) {
	oldSecret := []byte("old-secret-old-secret-old-secret-0")
	newSecret := []byte("new-secret-new-secret-new-secret-0")
	oldTokens := NewTokens(oldSecret, time.Minute)
	legacy, _, err := oldTokens.IssueAccess(uuid.New(), RoleUser)
	require.NoError(t, err)

	rotated := NewTokens(newSecret, time.Minute).WithPreviousSecrets(oldSecret)
	_, err = rotated.Parse(legacy)
	require.NoError(t, err, "tokens signed with the previous secret stay valid during rotation")

	fresh, _, _ := rotated.IssueAccess(uuid.New(), RoleUser)
	tok, err := rotated.Parse(fresh)
	require.NoError(t, err)
	require.Equal(t, KeyID(newSecret), tok.Header["kid"])

	_, err = NewTokens(newSecret, time.Minute).Parse(legacy)
	require.Error(t, err, "after the grace window old tokens are rejected")

	noKid := jwt.NewWithClaims(jwt.SigningMethodHS256, &Claims{Role: RoleUser, RegisteredClaims: jwt.RegisteredClaims{
		Subject: uuid.NewString(), Issuer: Issuer, IssuedAt: jwt.NewNumericDate(time.Now()), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
	}})
	raw, _ := noKid.SignedString(newSecret)
	_, err = rotated.Parse(raw)
	require.NoError(t, err, "pre-rotation tokens without kid verify against the current secret")
}
