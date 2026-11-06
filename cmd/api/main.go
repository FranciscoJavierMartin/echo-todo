package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"echo-todo-2/internal/config"
	"echo-todo-2/internal/platform/logger"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := logger.New(cfg)

	e := echo.NewWithConfig(echo.Config{Logger: log})
	e.Use(middleware.RequestLogger())
	e.Use(middleware.Recover())
	e.GET("/healthz", func(c *echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})
	if err := e.Start(cfg.Addr); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	return nil
}
