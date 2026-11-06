package app_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/app/apptest"
	"echo-todo-2/internal/config"
)

func assertSecurityHeaders(t *testing.T, h http.Header, label string) {
	t.Helper()
	require.Equal(t, "nosniff", h.Get("X-Content-Type-Options"), label)
	require.Equal(t, "SAMEORIGIN", h.Get("X-Frame-Options"), label)
	require.Equal(t, "strict-origin-when-cross-origin", h.Get("Referrer-Policy"), label)
	require.Contains(t, h.Get("Content-Security-Policy"), "default-src 'self'", label)
	require.NotEmpty(t, h.Get("X-Request-Id"), label)
}

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	env := apptest.New(t)
	u := env.CreateUser("user")
	for _, r := range []apptest.Request{
		{Method: http.MethodGet, Path: "/healthz"},
		{Method: http.MethodGet, Path: "/v1/todos", Token: u.Token},             // 200
		{Method: http.MethodGet, Path: "/v1/todos"},                             // 401
		{Method: http.MethodGet, Path: "/does-not-exist"},                       // 404
		{Method: http.MethodPut, Path: "/v1/todos"},                             // 405
		{Method: http.MethodPost, Path: "/v1/todos", Token: u.Token, Body: "{"}, // 422
	} {
		rec := env.Do(r)
		assertSecurityHeaders(t, rec.Header(), r.Method+" "+r.Path)
		require.Empty(t, rec.Header().Get("Strict-Transport-Security"), "no HSTS over plain http")
	}
}

func TestHSTSOnlyBehindTrustedTLSProxy(t *testing.T) {
	env := apptest.New(t, apptest.WithConfig(func(c *config.Config) { c.TrustedProxies = []string{"10.0.0.1"} }))
	rec := env.Do(apptest.Request{Method: http.MethodGet, Path: "/healthz", Remote: "10.0.0.1:1234",
		Headers: map[string]string{"X-Forwarded-Proto": "https"}})
	require.Contains(t, rec.Header().Get("Strict-Transport-Security"), "max-age=31536000")

	rec = env.Do(apptest.Request{Method: http.MethodGet, Path: "/healthz", Remote: "192.0.2.1:1234",
		Headers: map[string]string{"X-Forwarded-Proto": "https"}})
	require.Empty(t, rec.Header().Get("Strict-Transport-Security"), "untrusted peers cannot claim https")
}

func TestCSRFRejectsCookieWritesWithoutToken(t *testing.T) {
	env := apptest.New(t)
	u := env.CreateUser("user")
	cookie := map[string]string{"Cookie": "access_token=" + u.Token}

	// Cross-site form post with the session cookie and no token → 403.
	rec := env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/todos", Body: `{"title":"forged"}`,
		Headers: map[string]string{"Cookie": "access_token=" + u.Token, "Sec-Fetch-Site": "cross-site"}})
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), `"code":"csrf_invalid"`)

	// Old browsers (no Sec-Fetch-Site) with a wrong token → 403.
	rec = env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/todos", Body: `{"title":"forged"}`,
		Headers: map[string]string{"Cookie": "access_token=" + u.Token + "; _csrf=right", "X-CSRF-Token": "wrong"}})
	require.Equal(t, http.StatusForbidden, rec.Code)

	// Double-submit succeeds.
	rec = env.Do(apptest.Request{Method: http.MethodGet, Path: "/v1/auth/csrf", Headers: cookie})
	tok := apptest.JSON[map[string]string](t, rec)["csrf_token"]
	rec = env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/todos", Body: `{"title":"legit"}`,
		Headers: map[string]string{"Cookie": "access_token=" + u.Token + "; _csrf=" + tok, "X-CSRF-Token": tok}})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	// Same-origin fetch metadata is accepted without a token.
	rec = env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/todos", Body: `{"title":"same-origin"}`,
		Headers: map[string]string{"Cookie": "access_token=" + u.Token, "Sec-Fetch-Site": "same-origin"}})
	require.Equal(t, http.StatusCreated, rec.Code)

	// Header-authenticated (Bearer) requests are not CSRF-able and need no token.
	rec = env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/todos", Body: `{"title":"api"}`, Token: u.Token})
	require.Equal(t, http.StatusCreated, rec.Code)

	// Safe methods with the cookie are fine.
	rec = env.Do(apptest.Request{Method: http.MethodGet, Path: "/v1/todos", Headers: cookie})
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestAuthRateLimit(t *testing.T) {
	env := apptest.New(t, apptest.WithConfig(func(c *config.Config) { c.AuthRPS = 5 }))
	attempt := func() int {
		return env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/auth/login", Remote: "198.51.100.1:1000",
			Body: `{"email":"x@example.com","password":"wrong-password"}`}).Code
	}
	for i := range 10 { // burst 10
		require.Equal(t, http.StatusUnauthorized, attempt(), "attempt %d", i+1)
	}
	require.Equal(t, http.StatusTooManyRequests, attempt())

	// The todo reads of another client are unaffected by the auth bucket.
	u := env.CreateUser("user")
	require.Equal(t, http.StatusOK, env.Do(apptest.Request{Method: http.MethodGet, Path: "/v1/todos", Token: u.Token}).Code)
}

func TestWriteRateLimitPerUser(t *testing.T) {
	env := apptest.New(t, apptest.WithConfig(func(c *config.Config) { c.WriteRPS = 1 }))
	a, b := env.CreateUser("user"), env.CreateUser("user")
	post := func(u apptest.User) int {
		return env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/todos", Token: u.Token, Body: `{"title":"w"}`}).Code
	}
	require.Equal(t, http.StatusCreated, post(a))
	require.Equal(t, http.StatusCreated, post(a))
	require.Equal(t, http.StatusTooManyRequests, post(a))
	require.Equal(t, http.StatusCreated, post(b), "buckets are per user")
	require.Equal(t, http.StatusOK, env.Do(apptest.Request{Method: http.MethodGet, Path: "/v1/todos", Token: a.Token}).Code, "reads are not write-limited")
}

func TestBodyCaps(t *testing.T) {
	env := apptest.New(t)
	u := env.CreateUser("user")
	huge := `{"title":"` + strings.Repeat("x", 2<<20) + `"}` // > 1 MiB JSON cap
	rec := env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/todos", Token: u.Token, Body: huge})
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)

	enormous := strings.Repeat("x", 11<<20) // > global cap, rejected in Pre even on unknown routes
	rec = env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/nowhere", Body: enormous})
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

func TestCORS(t *testing.T) {
	env := apptest.New(t, apptest.WithConfig(func(c *config.Config) { c.AllowedOrigins = []string{"https://app.example.com"} }))
	rec := env.Do(apptest.Request{Method: http.MethodOptions, Path: "/v1/todos", Headers: map[string]string{
		"Origin": "https://app.example.com", "Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "content-type,if-match",
	}})
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, "https://app.example.com", rec.Header().Get("Access-Control-Allow-Origin"))
	require.Equal(t, "true", rec.Header().Get("Access-Control-Allow-Credentials"))

	rec = env.Do(apptest.Request{Method: http.MethodGet, Path: "/healthz", Headers: map[string]string{"Origin": "https://evil.example"}})
	require.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
}
