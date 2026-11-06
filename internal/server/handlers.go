package server

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
)

// Handlers holds framework-level handlers (health, 404/405). Feature handlers live in their own packages.
type Handlers struct {
	deps    Deps
	metrics echo.HandlerFunc
}

// Health is liveness: the process can serve HTTP. It never touches dependencies,
// so a database outage does not get the pod killed and restarted in a loop.
func (h *Handlers) Health(c *echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// ReadinessCheck reports whether a dependency is usable.
type ReadinessCheck func(ctx context.Context) error

// Ready is readiness: every dependency answers within a second. Load balancers
// stop routing to an instance that is not ready (e.g. during shutdown or a DB outage).
func (h *Handlers) Ready(c *echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), time.Second)
	defer cancel()
	status, code := "ok", http.StatusOK
	checks := map[string]string{}
	for name, check := range h.deps.Ready {
		if err := check(ctx); err != nil {
			checks[name] = "down"
			status, code = "unavailable", http.StatusServiceUnavailable
			c.Logger().WarnContext(ctx, "readiness check failed", "check", name, "err", err)
			continue
		}
		checks[name] = "ok"
	}
	c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
	return c.JSON(code, map[string]any{"status": status, "checks": checks})
}

// NotFound is the router-level 404. Returning the error lets the central error handler render it.
func (h *Handlers) NotFound(c *echo.Context) error {
	return echo.ErrNotFound
}

// MethodNotAllowed mirrors echo's default: the router stored the allowed methods in the context.
func (h *Handlers) MethodNotAllowed(c *echo.Context) error {
	if allow, ok := c.Get(echo.ContextKeyHeaderAllow).(string); ok && allow != "" {
		c.Response().Header().Set(echo.HeaderAllow, allow)
	}
	return echo.ErrMethodNotAllowed
}
