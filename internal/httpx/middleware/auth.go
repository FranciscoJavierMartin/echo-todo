// Package middleware contains the service's hand-written Echo middleware.
package middleware

import (
	"errors"

	"github.com/golang-jwt/jwt/v5"
	echojwt "github.com/labstack/echo-jwt/v5"
	"github.com/labstack/echo/v5"

	"echo-todo-2/internal/apperr"
	"echo-todo-2/internal/auth"
	"echo-todo-2/internal/httpx"
	"echo-todo-2/internal/platform/logger"
)

// TokenContextKey is where echo-jwt stores the verified *jwt.Token.
const TokenContextKey = "token"

// AuthRequired verifies the access token (Bearer header or access_token cookie)
// with echo-jwt, then puts a typed *auth.Principal in the context and tags the
// request-scoped logger and context with user_id. Failures are 401 problems with
// a machine-readable code: missing_token, token_expired or invalid_token.
func AuthRequired(tokens *auth.Tokens) echo.MiddlewareFunc {
	return echojwt.WithConfig(echojwt.Config{
		TokenLookup: "header:Authorization:Bearer ,cookie:access_token",
		ContextKey:  TokenContextKey,
		ParseTokenFunc: func(c *echo.Context, raw string) (any, error) {
			return tokens.Parse(raw)
		},
		ErrorHandler: func(c *echo.Context, err error) error {
			return unauthenticated(err)
		},
		SuccessHandler: func(c *echo.Context) error {
			tok, err := echo.ContextGet[*jwt.Token](c, TokenContextKey)
			if err != nil {
				return unauthenticated(err)
			}
			p, err := auth.PrincipalFromToken(tok)
			if err != nil {
				return apperr.Unauthenticated("invalid token").WithCode("invalid_token").Wrap(err)
			}
			SetPrincipal(c, p)
			return nil
		},
	})
}

// SetPrincipal stores p in the echo context and propagates user_id to the request
// context (for services' logs) and the request-scoped logger.
func SetPrincipal(c *echo.Context, p *auth.Principal) {
	httpx.SetPrincipal(c, p)
	uid := p.UserID.String()
	c.SetRequest(c.Request().WithContext(logger.WithUserID(c.Request().Context(), uid)))
	c.SetLogger(c.Logger().With("user_id", uid))
}

func unauthenticated(err error) error {
	var extraction *echojwt.TokenExtractionError
	switch {
	case errors.As(err, &extraction):
		return apperr.Unauthenticated("missing bearer token").WithCode("missing_token").Wrap(err)
	case errors.Is(err, jwt.ErrTokenExpired):
		return apperr.Unauthenticated("access token expired").WithCode("token_expired").Wrap(err)
	default:
		return apperr.Unauthenticated("invalid token").WithCode("invalid_token").Wrap(err)
	}
}

// RequireRole is the coarse role gate; it must run after AuthRequired.
func RequireRole(role string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			u, err := httpx.CurrentUser(c)
			if err != nil {
				return err
			}
			if u.Role != role {
				return apperr.Forbidden("requires role " + role)
			}
			return next(c)
		}
	}
}
