package app_test

import (
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/app/apptest"
	"echo-todo-2/internal/config"
)

// Routes that intentionally have no OpenAPI operation (UI, redirects).
var nonAPIRoutes = map[string]bool{
	"GET /":                       true,
	"GET /app":                    true,
	"GET /app/assets/*":           true,
	"echo_route_not_found /app/*": true,
}

var echoParam = regexp.MustCompile(`:[^/]+|\*`)
var specParam = regexp.MustCompile(`\{[^/]+\}`)

// Every registered route has a spec operation and every spec operation is served.
func TestRoutesMatchSpec(t *testing.T) {
	env := apptest.New(t)
	doc, _ := apptest.Spec(t)

	served := map[string]bool{}
	for _, r := range env.Echo.Router().Routes() {
		key := r.Method + " " + r.Path
		if nonAPIRoutes[key] {
			continue
		}
		require.NotEqual(t, echo.RouteNotFound, r.Method, "unexpected catch-all route %s", r.Path)
		served[r.Method+" "+echoParam.ReplaceAllString(r.Path, "{}")] = true
	}

	documented := map[string]bool{}
	for path, item := range doc.Paths.Map() {
		for method := range item.Operations() {
			documented[method+" "+specParam.ReplaceAllString(path, "{}")] = true
		}
	}

	var missingInSpec, missingInServer []string
	for k := range served {
		if !documented[k] {
			missingInSpec = append(missingInSpec, k)
		}
	}
	for k := range documented {
		if !served[k] {
			missingInServer = append(missingInServer, k)
		}
	}
	slices.Sort(missingInSpec)
	slices.Sort(missingInServer)
	require.Empty(t, missingInSpec, "routes without an OpenAPI operation")
	require.Empty(t, missingInServer, "OpenAPI operations without a route")
}

func TestOpsEndpoints(t *testing.T) {
	env := apptest.New(t)
	rec := env.Do(apptest.Request{Method: http.MethodGet, Path: "/readyz"})
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"status":"ok","checks":{"postgres":"ok"}}`, rec.Body.String())

	rec = env.Do(apptest.Request{Method: http.MethodGet, Path: "/docs/openapi.yaml"})
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, strings.HasPrefix(rec.Body.String(), "openapi: 3"))

	env.Pool.Close()
	rec = env.Do(apptest.Request{Method: http.MethodGet, Path: "/readyz"})
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Body.String(), `"postgres":"down"`)
	require.Equal(t, http.StatusOK, env.Do(apptest.Request{Method: http.MethodGet, Path: "/healthz"}).Code, "liveness ignores dependencies")
}

func TestDocsAdminOnlyInProd(t *testing.T) {
	env := apptest.New(t, apptest.WithConfig(func(c *config.Config) { c.Env = "prod" }))
	require.Equal(t, http.StatusUnauthorized, env.Do(apptest.Request{Method: http.MethodGet, Path: "/docs/openapi.yaml"}).Code)
	user := env.CreateUser("user")
	require.Equal(t, http.StatusForbidden, env.Do(apptest.Request{Method: http.MethodGet, Path: "/docs/openapi.yaml", Token: user.Token}).Code)
	admin := env.CreateAdmin()
	require.Equal(t, http.StatusOK, env.Do(apptest.Request{Method: http.MethodGet, Path: "/docs/openapi.yaml", Token: admin.Token}).Code)
}

// Binding errors from the generated wrapper keep our semantics.
func TestGeneratedBindingErrorsMapToTaxonomy(t *testing.T) {
	env := apptest.New(t)
	u := env.CreateUser("user")
	for path, want := range map[string]int{
		"/v1/todos?limit=abc":           http.StatusUnprocessableEntity,
		"/v1/todos?priority=x":          http.StatusUnprocessableEntity,
		"/v1/todos?due_before=tomorrow": http.StatusUnprocessableEntity,
		"/v1/todos?count=maybe":         http.StatusUnprocessableEntity,
		"/v1/todos/not-a-uuid":          http.StatusNotFound,
		"/v1/todos/not-a-uuid/audit":    http.StatusForbidden, // role gate runs first
	} {
		rec := env.Do(apptest.Request{Method: http.MethodGet, Path: path, Token: u.Token})
		require.Equal(t, want, rec.Code, path+": "+rec.Body.String())
	}
}
