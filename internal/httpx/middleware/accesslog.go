package middleware

import (
	"log/slog"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"go.opentelemetry.io/otel/trace"
)

// AccessLogConfig configures AccessLog.
type AccessLogConfig struct {
	Skipper middleware.Skipper
	// IncludeClientIP logs the client address. IPs are personal data, so this is
	// opt-in (LOG_CLIENT_IP); rate limiting does not need it in logs.
	IncludeClientIP bool
}

// AccessLog writes one structured line per request after the handler chain returns:
// method, route template (never the raw path with IDs), status, latency, bytes,
// request_id and user_id (both carried by the request-scoped logger).
func AccessLog(cfg AccessLogConfig) echo.MiddlewareFunc {
	skipper := cfg.Skipper
	if skipper == nil {
		skipper = middleware.DefaultSkipper
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if skipper(c) {
				return next(c)
			}
			start := time.Now()
			err := next(c)
			latency := time.Since(start)

			resp, status := echo.ResolveResponseStatus(c.Response(), err)
			var size int64
			if resp != nil {
				size = resp.Size
			}
			route := c.Path()
			if route == "" {
				route = "<unmatched>"
			}
			attrs := []slog.Attr{
				slog.String("method", c.Request().Method),
				slog.String("route", route),
				slog.Int("status", status),
				slog.Duration("latency", latency),
				slog.Int64("bytes_out", size),
			}
			if cfg.IncludeClientIP {
				attrs = append(attrs, slog.String("remote_ip", c.RealIP()))
			}
			if sc := trace.SpanContextFromContext(c.Request().Context()); sc.IsValid() {
				attrs = append(attrs, slog.String("trace_id", sc.TraceID().String()))
			}
			lvl := slog.LevelInfo
			if status >= 500 {
				lvl = slog.LevelError
			}
			// c.Logger() is the request-scoped logger: RequestContext added request_id and
			// AuthRequired (deeper in the chain, same *echo.Context) added user_id.
			c.Logger().LogAttrs(c.Request().Context(), lvl, "access", attrs...)
			return err
		}
	}
}

// SkipPaths returns a Skipper matching exact request paths (e.g. health checks).
func SkipPaths(paths ...string) middleware.Skipper {
	set := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		set[p] = struct{}{}
	}
	return func(c *echo.Context) bool {
		_, ok := set[c.Request().URL.Path]
		return ok
	}
}
