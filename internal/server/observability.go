package server

import (
	"crypto/subtle"
	"math"
	"net/http"
	"strconv"
	"time"

	echootel "github.com/labstack/echo-otel/v5"
	echoprometheus "github.com/labstack/echo-prometheus"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"echo-todo-2/internal/apperr"
	"echo-todo-2/internal/config"
)

// proxyAware returns IP and scheme extractors. Without TRUSTED_PROXIES the peer
// address is used and forwarding headers are ignored (v5 default behaviour); with
// it, X-Forwarded-For / X-Forwarded-Proto are honoured only from those networks.
func proxyAware(cfg config.Config) (echo.IPExtractor, echo.SchemeExtractor) {
	if len(cfg.TrustedProxies) == 0 {
		return echo.ExtractIPDirect(), echo.ExtractSchemeDirect()
	}
	opts := []echo.TrustOption{
		echo.TrustLoopback(false), echo.TrustLinkLocal(false), echo.TrustPrivateNet(false),
	}
	for _, p := range cfg.TrustedProxies {
		n, err := config.ParseCIDR(p) // validated in config.Load
		if err == nil {
			opts = append(opts, echo.TrustIPRange(n))
		}
	}
	return echo.ExtractIPFromXFFHeader(opts...), echo.ExtractSchemeFromHeaders(opts...)
}

// tracing starts a server span per request (continuing an incoming traceparent).
func tracing(tp trace.TracerProvider) echo.MiddlewareFunc {
	return echootel.NewMiddlewareWithConfig(echootel.Config{
		TracerProvider: tp,
		Propagators:    propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}),
		Skipper:        skipQuiet,
	})
}

// metrics records request count/latency/sizes labelled by status, method and
// route template. Host is blanked and unmatched paths are not used as labels so
// attackers cannot explode label cardinality.
func metrics(reg prometheus.Registerer) echo.MiddlewareFunc {
	m, err := echoprometheus.MiddlewareConfig{
		Namespace:                 "todo",
		Subsystem:                 "http",
		Registerer:                reg,
		DoNotUseRequestPathFor404: true,
		Skipper:                   skipQuiet,
		LabelFuncs: map[string]echoprometheus.LabelValueFunc{
			"host": func(*echo.Context, error) string { return "" },
		},
	}.ToMiddleware()
	if err != nil {
		panic(err)
	}
	return m
}

func metricsHandler(gatherer prometheus.Gatherer, token string) echo.HandlerFunc {
	h := echoprometheus.NewHandlerWithConfig(echoprometheus.HandlerConfig{Gatherer: gatherer})
	if token == "" {
		return h
	}
	want := []byte("Bearer " + token)
	return func(c *echo.Context) error {
		got := []byte(c.Request().Header.Get(echo.HeaderAuthorization))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			return apperr.Unauthenticated("metrics token required").WithCode("missing_token")
		}
		return h(c)
	}
}

// rateLimit is a per-client-IP token bucket. Identity comes from c.RealIP(), which
// is only trustworthy because IPExtractor is configured above.
func rateLimit(rps float64, burst int, skipper middleware.Skipper) echo.MiddlewareFunc {
	if burst <= 0 {
		burst = int(math.Ceil(rps * 2))
	}
	store := middleware.NewRateLimiterMemoryStoreWithConfig(middleware.RateLimiterMemoryStoreConfig{
		Rate: rps, Burst: burst, ExpiresIn: 3 * time.Minute,
	})
	retryAfter := strconv.Itoa(int(math.Max(1, math.Ceil(1/rps))))
	return middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
		Skipper: skipper,
		Store:   store,
		IdentifierExtractor: func(c *echo.Context) (string, error) {
			return c.RealIP(), nil
		},
		DenyHandler: func(c *echo.Context, _ string, err error) error {
			c.Response().Header().Set(echo.HeaderRetryAfter, retryAfter)
			return apperr.RateLimited("too many requests").Wrap(err)
		},
		ErrorHandler: func(c *echo.Context, err error) error {
			return apperr.Forbidden("unable to identify client").Wrap(err)
		},
	})
}

func skipQuiet(c *echo.Context) bool {
	switch c.Request().URL.Path {
	case "/healthz", "/readyz", "/metrics":
		return true
	}
	return false
}

// pprofHandler serves net/http/pprof (registered on http.DefaultServeMux by the
// blank import in pprof.go).
func pprofHandler() echo.HandlerFunc { return echo.WrapHandler(http.DefaultServeMux) }
