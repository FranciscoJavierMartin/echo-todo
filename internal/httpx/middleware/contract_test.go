package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/auth"
	mw "echo-todo-2/internal/httpx/middleware"
)

// Contract test: echo-jwt stores a *jwt.Token (golang-jwt/v5) under our context
// key with our Claims type. echo-jwt warns this breaks across library majors; if a
// dependency bump changes it, this test fails before production does.
func TestJWTContextContract(t *testing.T) {
	tokens := auth.NewTokens([]byte("contract-secret-contract-secret-123"), time.Minute)
	uid := uuid.New()
	raw, _, err := tokens.IssueAccess(uid, auth.RoleAdmin)
	require.NoError(t, err)

	e := echo.New()
	var called bool
	e.GET("/x", func(c *echo.Context) error {
		called = true
		tok, ok := c.Get(mw.TokenContextKey).(*jwt.Token)
		require.True(t, ok, "c.Get(\"token\") must cast to *jwt.Token")
		claims, ok := tok.Claims.(*auth.Claims)
		require.True(t, ok, "claims must be *auth.Claims")
		require.Equal(t, uid.String(), claims.Subject)

		generic, err := echo.ContextGet[*jwt.Token](c, mw.TokenContextKey)
		require.NoError(t, err)
		require.Same(t, tok, generic)
		return c.NoContent(http.StatusOK)
	}, mw.JWT(tokens))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, called)
}
