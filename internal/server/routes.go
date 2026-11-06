package server

import (
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

func registerRoutes(e *echo.Echo, h *Handlers) {
	e.Use(middleware.RequestLogger())
	e.Use(middleware.Recover())
	e.GET("/healthz", h.Health)
}
