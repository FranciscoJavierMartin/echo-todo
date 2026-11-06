package middleware

import (
	"log/slog"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"echo-todo-2/internal/platform/logger"
)

// RequestContext assigns a request ID (reusing a well-formed incoming X-Request-Id),
// echoes it in the response, stores it in the request context.Context so services
// can log with it, and installs a request-scoped child logger via c.SetLogger.
//
// Registered with e.Pre so it also covers 404/405 responses.
func RequestContext(log *slog.Logger) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			rid := c.Request().Header.Get(echo.HeaderXRequestID)
			if !validRequestID(rid) {
				rid = uuid.NewString()
			}
			c.Response().Header().Set(echo.HeaderXRequestID, rid)

			ctx := logger.WithRequestID(c.Request().Context(), rid)
			c.SetRequest(c.Request().WithContext(ctx))
			c.SetLogger(log.With("request_id", rid))

			return next(c)
		}
	}
}

// validRequestID accepts short IDs made of URL-safe characters, so clients cannot
// inject newlines or huge values into our logs.
func validRequestID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}
