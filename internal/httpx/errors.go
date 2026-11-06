// Package httpx holds HTTP-layer plumbing shared by every feature: the central
// error handler, validator and response helpers.
package httpx

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v5"

	"echo-todo-2/internal/apperr"
	"echo-todo-2/internal/platform/logger"
)

// Problem is the RFC 9457 body for application errors, with extension members.
type Problem struct {
	Type      string            `json:"type"`
	Title     string            `json:"title"`
	Status    int               `json:"status"`
	Detail    string            `json:"detail,omitempty"`
	Instance  string            `json:"instance,omitempty"`
	Code      string            `json:"code"`
	Errors    map[string]string `json:"errors,omitempty"`
	RequestID string            `json:"request_id,omitempty"`
	Current   any               `json:"current,omitempty"`
}

// ErrorHandler is the single place that turns errors into responses and logs them once.
//
//   - *apperr.Error → problem+json with code/errors extensions
//   - anything else → echo's RFC 9457 handler (HTTPStatusCoder aware, hides causes unless expose)
func ErrorHandler(log *slog.Logger, expose bool) echo.HTTPErrorHandler {
	problem := echo.ProblemDetailsHTTPErrorHandler(expose)
	return func(c *echo.Context, err error) {
		ctx := c.Request().Context()
		status := echo.StatusCode(err)
		if status == 0 {
			status = http.StatusInternalServerError
		}

		if isCommitted(c) {
			log.LogAttrs(ctx, slog.LevelWarn, "error after response was committed", slog.Any("err", err))
			return
		}

		var appErr *apperr.Error
		if errors.As(err, &appErr) {
			lvl := slog.LevelWarn
			if appErr.Kind == apperr.KindInternal {
				lvl = slog.LevelError
			}
			log.LogAttrs(ctx, lvl, "request failed",
				slog.String("op", appErr.Op),
				slog.Int("status", appErr.StatusCode()),
				slog.String("code", appErr.MachineCode()),
				slog.String("route", c.Path()),
				slog.Any("err", appErr.Err),
			)
			writeProblem(c, appErr, expose)
			return
		}

		lvl := slog.LevelDebug
		if status >= 500 {
			lvl = slog.LevelError
		}
		log.LogAttrs(ctx, lvl, "request failed",
			slog.Int("status", status),
			slog.String("route", c.Path()),
			slog.Any("err", err),
		)
		problem(c, err)
	}
}

func writeProblem(c *echo.Context, e *apperr.Error, expose bool) {
	pe := e.ProblemError()
	p := Problem{
		Type:      "about:blank",
		Title:     pe.Title,
		Status:    pe.Status,
		Detail:    pe.Detail,
		Instance:  c.Request().URL.Path,
		Code:      e.MachineCode(),
		Errors:    e.Fields,
		RequestID: logger.RequestID(c.Request().Context()),
	}
	if cur, ok := e.Extensions["current"]; ok {
		p.Current = cur
	}
	if expose && e.Err != nil {
		p.Detail += ": " + e.Err.Error()
	}
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMEApplicationProblemJSON)
	var err error
	if c.Request().Method == http.MethodHead {
		err = c.NoContent(p.Status)
	} else {
		err = c.JSON(p.Status, p)
	}
	if err != nil {
		c.Logger().Error("failed to write problem response", "err", err)
	}
}

func isCommitted(c *echo.Context) bool {
	if r, err := echo.UnwrapResponse(c.Response()); err == nil {
		return r.Committed
	}
	return false
}
