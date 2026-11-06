// Package server assembles the Echo instance: configuration, middleware chain and routes.
package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"

	"echo-todo-2/internal/auth"
	"echo-todo-2/internal/config"
	"echo-todo-2/internal/httpx"
	todohandler "echo-todo-2/internal/todo/handler"
)

// Deps are the collaborators the HTTP layer needs. internal/app builds them.
type Deps struct {
	Config config.Config
	Logger *slog.Logger
	Tokens *auth.Tokens
	Todos  *todohandler.Handler
}

// New builds a fully configured *echo.Echo. It never starts listening.
func New(d Deps) *echo.Echo {
	h := &Handlers{deps: d}
	e := echo.NewWithConfig(echo.Config{
		Logger:           d.Logger,
		HTTPErrorHandler: httpx.ErrorHandler(d.Logger, d.Config.ExposeErrors),
		Validator:        httpx.NewValidator(),
		JSONSerializer:   httpx.StrictJSONSerializer{},
		// Groups would otherwise register catch-all 404 routes that shadow 405s
		// (PUT /v1/todos would answer 404 instead of 405 + Allow).
		NoGroupAutoRegister404Routes: true,
		Router: echo.NewRouter(echo.RouterConfig{
			AllowOverwritingRoute:   false,
			AutoHandleHEAD:          true,
			NotFoundHandler:         h.NotFound,
			MethodNotAllowedHandler: h.MethodNotAllowed,
		}),
	})
	registerRoutes(e, h)
	return e
}

// StartConfig returns the hardened server start configuration.
func StartConfig(cfg config.Config, log *slog.Logger) echo.StartConfig {
	return echo.StartConfig{
		Address:         cfg.Addr,
		HideBanner:      cfg.Env == "prod",
		GracefulTimeout: cfg.ShutdownTimeout,
		OnShutdownError: func(err error) {
			log.Error("graceful shutdown did not complete; in-flight requests were cut off", "err", err)
		},
		BeforeServeFunc: func(s *http.Server) error {
			s.ReadHeaderTimeout = 5 * time.Second
			s.IdleTimeout = 120 * time.Second
			s.MaxHeaderBytes = 32 << 10
			s.RegisterOnShutdown(func() { log.Info("shutdown signal received; draining in-flight requests") })
			return nil
		},
	}
}
