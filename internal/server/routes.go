package server

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"echo-todo-2/internal/auth"
	mw "echo-todo-2/internal/httpx/middleware"
	todohandler "echo-todo-2/internal/todo/handler"
)

// Route names, used for reverse routing (Location headers) and route tests.
const (
	RouteTodosCreate = "todos.create"
	RouteTodosList   = "todos.list"
	RouteTodosGet    = todohandler.RouteGet
	RouteTodosUpdate = "todos.update"
	RouteTodosDelete = "todos.delete"
	RouteTodosAudit  = "todos.audit"

	RouteAuthRegister  = "auth.register"
	RouteAuthLogin     = "auth.login"
	RouteAuthRefresh   = "auth.refresh"
	RouteAuthLogout    = "auth.logout"
	RouteAuthLogoutAll = "auth.logout_all"
	RouteAuthMe        = "auth.me"
	RouteAPIKeysCreate = "auth.api_keys.create"
	RouteAPIKeysList   = "auth.api_keys.list"
	RouteAPIKeysRevoke = "auth.api_keys.revoke"
)

var quietPaths = []string{"/healthz", "/readyz", "/metrics"}

func registerRoutes(e *echo.Echo, h *Handlers) {
	d := h.deps

	// Pre: runs before routing, so it also covers 404/405. Outermost first.
	// AccessLog sits outside Recover so panicking requests are still access-logged.
	e.Pre(
		mw.RequestContext(d.Logger),
		tracing(d.Telemetry.TracerProvider),
		mw.AccessLog(mw.SkipPaths(quietPaths...)),
		mw.Recover(),
		metrics(d.Telemetry.Registry),
		middleware.RemoveTrailingSlashWithConfig(middleware.RemoveTrailingSlashConfig{
			Skipper: func(c *echo.Context) bool { return strings.HasPrefix(c.Request().URL.Path, "/debug/pprof/") },
		}),
		middleware.BodyLimit(d.Config.MaxUploadBytes+(64<<10)), // hard cap incl. multipart overhead
	)
	// Use: after routing. Keyed on c.RealIP(), which honours TRUSTED_PROXIES.
	e.Use(rateLimit(d.Config.RequestsPerSec, 0, skipQuiet))

	e.GET("/healthz", h.Health)
	e.GET("/metrics", metricsHandler(d.Telemetry.Registry, d.Config.MetricsToken))

	authRequired := mw.AuthRequired(d.Tokens, d.APIKeys)

	debug := e.Group("/debug", authRequired, mw.RequireRole(auth.RoleAdmin))
	debug.GET("/pprof/*", pprofHandler())

	v1 := e.Group("/v1")
	jsonLimit := middleware.BodyLimit(d.Config.MaxBodyBytes)

	authG := v1.Group("/auth", jsonLimit)
	add(authG, http.MethodPost, "/register", RouteAuthRegister, d.Auth.Register)
	add(authG, http.MethodPost, "/login", RouteAuthLogin, d.Auth.Login)
	add(authG, http.MethodPost, "/refresh", RouteAuthRefresh, d.Auth.Refresh)
	add(authG, http.MethodPost, "/logout", RouteAuthLogout, d.Auth.Logout)
	session := authG.Group("", authRequired)
	add(session, http.MethodGet, "/me", RouteAuthMe, d.Auth.Me)
	add(session, http.MethodPost, "/logout-all", RouteAuthLogoutAll, d.Auth.LogoutAll)
	add(session, http.MethodPost, "/api-keys", RouteAPIKeysCreate, d.Auth.CreateAPIKey, mw.RequireJWT)
	add(session, http.MethodGet, "/api-keys", RouteAPIKeysList, d.Auth.ListAPIKeys)
	add(session, http.MethodDelete, "/api-keys/:id", RouteAPIKeysRevoke, d.Auth.RevokeAPIKey, mw.RequireJWT)

	todos := v1.Group("/todos", jsonLimit, authRequired)
	add(todos, http.MethodPost, "", RouteTodosCreate, d.Todos.Create)
	add(todos, http.MethodGet, "", RouteTodosList, d.Todos.List)
	add(todos, http.MethodGet, "/:id", RouteTodosGet, d.Todos.Get)
	add(todos, http.MethodPatch, "/:id", RouteTodosUpdate, d.Todos.Update)
	add(todos, http.MethodDelete, "/:id", RouteTodosDelete, d.Todos.Delete)
	add(todos, http.MethodGet, "/:id/audit", RouteTodosAudit, d.Todos.Audit, mw.RequireRole(auth.RoleAdmin))

	// Deliberately no e.RouteNotFound("/*", ...): a root catch-all 404 route wins over
	// the router's 405 detection, so every wrong-method request would become a 404.
	// RouterConfig.NotFoundHandler already renders unknown paths as JSON problems.
}

// add registers a named route. In v5 the RouteInfo returned by g.GET is a copy,
// so assigning info.Name afterwards does nothing; names must go through AddRoute.
func add(g *echo.Group, method, path, name string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) {
	if _, err := g.AddRoute(echo.Route{Method: method, Path: path, Name: name, Handler: h, Middlewares: m}); err != nil {
		panic(err)
	}
}
