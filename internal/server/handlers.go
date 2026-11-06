package server

import (
	"net/http"

	"github.com/labstack/echo/v5"
)

// Handlers holds framework-level handlers (health, 404/405). Feature handlers live in their own packages.
type Handlers struct {
	deps Deps
}

func (h *Handlers) Health(c *echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
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
