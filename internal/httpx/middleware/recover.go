package middleware

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/labstack/echo/v5"

	"echo-todo-2/internal/apperr"
)

// Recover turns panics into a 500 problem response and logs the stack with the
// request-scoped logger, so the log line carries request_id (and user_id when known).
// http.ErrAbortHandler is re-panicked: it is net/http's deliberate abort signal.
func Recover() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) (err error) {
			defer func() {
				r := recover()
				if r == nil {
					return
				}
				if r == http.ErrAbortHandler {
					panic(r)
				}
				perr, ok := r.(error)
				if !ok {
					perr = fmt.Errorf("%v", r)
				}
				if errors.Is(perr, http.ErrAbortHandler) {
					panic(r)
				}
				c.Logger().LogAttrs(c.Request().Context(), slog.LevelError, "panic recovered",
					slog.Any("panic", perr),
					slog.String("route", c.Path()),
					slog.String("stack", string(debug.Stack())),
				)
				err = apperr.Internal("panic", perr).WithCode("panic")
			}()
			return next(c)
		}
	}
}
