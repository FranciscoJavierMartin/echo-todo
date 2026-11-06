package server

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"echo-todo-2/internal/apperr"
	authhandler "echo-todo-2/internal/auth/handler"
	"echo-todo-2/internal/config"
	"echo-todo-2/internal/httpx"
	mw "echo-todo-2/internal/httpx/middleware"
)

const csrfPath = "/v1/auth/csrf"

// secureHeaders sets CSP, nosniff, frame and referrer policies on every response.
// HSTS is only emitted when the request scheme is https (SchemeExtractor-aware),
// so it switches on by itself once TLS is terminated in front of us.
func secureHeaders() echo.MiddlewareFunc {
	return middleware.SecureWithConfig(middleware.SecureConfig{
		XSSProtection:         "0", // legacy auditor off; CSP is the real defence
		ContentTypeNosniff:    "nosniff",
		XFrameOptions:         "SAMEORIGIN",
		HSTSMaxAge:            31536000,
		ContentSecurityPolicy: "default-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'self'; frame-ancestors 'self'; form-action 'self'",
		ReferrerPolicy:        "strict-origin-when-cross-origin",
	})
}

// cors allows only the configured origins, with credentials. Nil when no origin
// is configured: the API is then same-origin only and browsers block cross-origin reads.
func cors(cfg config.Config) echo.MiddlewareFunc {
	if len(cfg.AllowedOrigins) == 0 {
		return nil
	}
	return middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins:     cfg.AllowedOrigins,
		AllowCredentials: true,
		AllowMethods:     []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPatch, http.MethodDelete},
		AllowHeaders: []string{
			echo.HeaderAuthorization, echo.HeaderContentType, echo.HeaderXCSRFToken, echo.HeaderXRequestID,
			"If-Match", "If-None-Match", "Idempotency-Key", authhandler.ClientHeader, mw.APIKeyHeader,
		},
		ExposeHeaders: []string{"ETag", echo.HeaderLocation, echo.HeaderXRequestID, echo.HeaderRetryAfter, "Idempotency-Replayed"},
		MaxAge:        600,
	})
}

// csrf protects cookie-authenticated requests. Requests that authenticate with a
// header (Bearer or API key) cannot be forged cross-site and are skipped. Modern
// browsers are also accepted via Sec-Fetch-Site same-origin; everything else must
// echo the _csrf cookie in X-CSRF-Token (double-submit). GET /v1/auth/csrf hands
// the token to SPAs.
func csrf(cfg config.Config) echo.MiddlewareFunc {
	return middleware.CSRFWithConfig(middleware.CSRFConfig{
		Skipper:        func(c *echo.Context) bool { return c.Request().URL.Path != csrfPath && !cookieAuthenticated(c) },
		TokenLookup:    "header:" + echo.HeaderXCSRFToken,
		CookieName:     "_csrf",
		CookiePath:     "/",
		CookieSameSite: http.SameSiteLaxMode,
		CookieSecure:   cfg.Env == "prod",
		TrustedOrigins: cfg.AllowedOrigins,
		ErrorHandler: func(c *echo.Context, err error) error {
			return apperr.Forbidden("missing or invalid CSRF token").WithCode("csrf_invalid").Wrap(err)
		},
		// State-changing cross-site/same-site requests from fetch-metadata browsers.
		AllowSecFetchSiteFunc: func(c *echo.Context) (bool, error) {
			return false, apperr.Forbidden("cross-site request blocked").WithCode("csrf_invalid")
		},
	})
}

func cookieAuthenticated(c *echo.Context) bool {
	h := c.Request().Header
	if h.Get(echo.HeaderAuthorization) != "" || h.Get(mw.APIKeyHeader) != "" {
		return false
	}
	for _, name := range []string{authhandler.AccessCookie, authhandler.RefreshCookie} {
		if _, err := c.Cookie(name); err == nil {
			return true
		}
	}
	return false
}

// csrfToken returns the token the CSRF middleware placed in the context.
func csrfToken(c *echo.Context) error {
	tok, _ := c.Get("csrf").(string)
	c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
	return c.JSON(http.StatusOK, map[string]string{"csrf_token": tok})
}

// requestTimeout puts a deadline on the request context; DB calls inherit it.
// Profiling endpoints are exempt (they legitimately run for 30s+).
func requestTimeout(cfg config.Config) echo.MiddlewareFunc {
	return middleware.ContextTimeoutWithConfig(middleware.ContextTimeoutConfig{
		Timeout: cfg.RequestTimeout,
		Skipper: func(c *echo.Context) bool { return strings.HasPrefix(c.Request().URL.Path, "/debug/pprof") },
	})
}

func byIP(c *echo.Context) (string, error) { return c.RealIP(), nil }

func byUser(c *echo.Context) (string, error) {
	if p, err := httpx.CurrentUser(c); err == nil {
		return "user:" + p.UserID.String(), nil
	}
	return "ip:" + c.RealIP(), nil
}

func onlyWrites(c *echo.Context) bool {
	switch c.Request().Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return false
	}
	return true
}
