package app_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/app/apptest"
	"echo-todo-2/internal/config"
)

func TestScalarUI(t *testing.T) {
	env := apptest.New(t)
	rec := env.Do(apptest.Request{Method: http.MethodGet, Path: "/docs"})
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	body := rec.Body.String()
	require.Contains(t, body, `<script id="api-reference"`)
	require.Contains(t, body, `data-url="/docs/openapi.yaml"`)
	require.Contains(t, body, `src="/docs/scalar.js"`)
	// Entities are decoded by the browser before Scalar JSON.parses the value.
	require.Contains(t, body, "&#34;servers&#34;:[{&#34;url&#34;:&#34;http://example.com&#34;}]")

	// Relaxed for the page only: styles inline, scripts still same-origin.
	csp := rec.Header().Get("Content-Security-Policy")
	require.Contains(t, csp, "script-src 'self'")
	require.Contains(t, csp, "style-src 'self' 'unsafe-inline'")
	require.Contains(t, csp, "font-src 'self'")
	require.NotContains(t, csp, "script-src 'self' 'unsafe-inline'")
	require.NotContains(t, csp, "https://") // no third-party origin allowed
	// Other responses keep the strict policy.
	strict := env.Do(apptest.Request{Method: http.MethodGet, Path: "/healthz"}).Header()
	require.Contains(t, strict.Get("Content-Security-Policy"), "default-src 'self'")
	require.NotContains(t, strict.Get("Content-Security-Policy"), "style-src")
}

func TestScalarBundle(t *testing.T) {
	env := apptest.New(t)
	rec := env.Do(apptest.Request{Method: http.MethodGet, Path: "/docs/scalar.js"})
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "gzip", rec.Header().Get("Content-Encoding"))
	require.Contains(t, rec.Header().Get("Content-Type"), "javascript")

	n, err := strconv.Atoi(rec.Header().Get("Content-Length"))
	require.NoError(t, err)
	require.Len(t, rec.Body.Bytes(), n)

	zr, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
	require.NoError(t, err)
	js, err := io.ReadAll(zr)
	require.NoError(t, err)
	require.True(t, strings.Contains(string(js), "createApiReference"))
	// The stylesheet's font URLs must point at us, not at the CDN.
	require.True(t, strings.Contains(string(js), "/docs/fonts/inter-latin.woff2"))
	require.False(t, strings.Contains(string(js), "fonts.scalar.com"))
}

func TestScalarFonts(t *testing.T) {
	env := apptest.New(t)
	rec := env.Do(apptest.Request{Method: http.MethodGet, Path: "/docs/fonts/inter-latin.woff2"})
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "font/woff2")
	n, err := strconv.Atoi(rec.Header().Get("Content-Length"))
	require.NoError(t, err)
	require.Len(t, rec.Body.Bytes(), n)
	require.True(t, strings.HasPrefix(rec.Body.String(), "wOF2"))

	// The licence text ships with the fonts and is reachable; every other
	// file in the embedded directory stays out of reach.
	o := env.Do(apptest.Request{Method: http.MethodGet, Path: "/docs/fonts/OFL.txt"})
	require.Equal(t, http.StatusOK, o.Code)
	require.Contains(t, o.Header().Get("Content-Type"), "text/plain")
	require.Contains(t, o.Body.String(), "JetBrains Mono Project Authors")
	require.NotContains(t, o.Body.String(), "traversal")

	for _, name := range []string{"missing.woff2", "..", "no/escapes", "OFL-notes.txt"} {
		require.Equal(t, http.StatusNotFound,
			env.Do(apptest.Request{Method: http.MethodGet, Path: "/docs/fonts/" + name}).Code, name)
	}
}

func TestScalarDocsAdminOnlyInProd(t *testing.T) {
	env := apptest.New(t, apptest.WithConfig(func(c *config.Config) { c.Env = "prod" }))
	paths := []string{"/docs", "/docs/scalar.js", "/docs/fonts/inter-latin.woff2", "/docs/fonts/OFL.txt"}
	for _, path := range paths {
		require.Equal(t, http.StatusUnauthorized, env.Do(apptest.Request{Method: http.MethodGet, Path: path}).Code, path)
	}
	user := env.CreateUser("user")
	for _, path := range paths {
		require.Equal(t, http.StatusForbidden, env.Do(apptest.Request{Method: http.MethodGet, Path: path, Token: user.Token}).Code, path)
	}
	admin := env.CreateAdmin()
	for _, path := range paths {
		require.Equal(t, http.StatusOK, env.Do(apptest.Request{Method: http.MethodGet, Path: path, Token: admin.Token}).Code, path)
	}
}
