package server

import (
	"net/http"
	"strings"
	"time"

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
	RouteAuthCSRF      = "auth.csrf"
	RouteAPIKeysCreate = "auth.api_keys.create"
	RouteAPIKeysList   = "auth.api_keys.list"
	RouteAPIKeysRevoke = "auth.api_keys.revoke"
)

var quietPaths = []string{"/healthz", "/readyz", "/metrics"}

func registerRoutes(e *echo.Echo, h *Handlers) {
	d := h.deps
	cfg := d.Config

	// Pre: runs before routing, so it also covers 404/405. Outermost first.
	// AccessLog sits outside Recover so panicking requests are still access-logged.
	pre := []echo.MiddlewareFunc{
		mw.RequestContext(d.Logger),
		tracing(d.Telemetry.TracerProvider),
		mw.AccessLog(mw.SkipPaths(quietPaths...)),
		mw.Recover(),
		metrics(d.Telemetry.Registry),
		middleware.RemoveTrailingSlashWithConfig(middleware.RemoveTrailingSlashConfig{
			Skipper: func(c *echo.Context) bool { return strings.HasPrefix(c.Request().URL.Path, "/debug/pprof/") },
		}),
		secureHeaders(),
	}
	if c := cors(cfg); c != nil {
		pre = append(pre, c)
	}
	pre = append(pre,
		middleware.BodyLimit(cfg.MaxUploadBytes+(64<<10)), // hard cap incl. multipart overhead
		requestTimeout(cfg),
	)
	e.Pre(pre...)

	// Use: after routing. Per-IP limit honouring TRUSTED_PROXIES; CSRF for cookie sessions.
	e.Use(
		rateLimit(cfg.RequestsPerSec, 0, skipQuiet, byIP),
		csrf(cfg),
	)

	e.GET("/healthz", h.Health)
	e.GET("/metrics", metricsHandler(d.Telemetry.Registry, cfg.MetricsToken))

	authRequired := mw.AuthRequired(d.Tokens, d.APIKeys)

	debug := e.Group("/debug", authRequired, mw.RequireRole(auth.RoleAdmin))
	debug.GET("/pprof/*", pprofHandler())

	v1 := e.Group("/v1")
	jsonLimit := middleware.BodyLimit(cfg.MaxBodyBytes)

	// Credential endpoints get a tight per-IP budget (brute force, credential stuffing).
	authG := v1.Group("/auth", jsonLimit, rateLimit(cfg.AuthRPS, int(cfg.AuthRPS*2), nil, byIP))
	add(authG, http.MethodPost, "/register", RouteAuthRegister, d.Auth.Register)
	add(authG, http.MethodPost, "/login", RouteAuthLogin, d.Auth.Login)
	add(authG, http.MethodPost, "/refresh", RouteAuthRefresh, d.Auth.Refresh)
	add(authG, http.MethodPost, "/logout", RouteAuthLogout, d.Auth.Logout)
	add(authG, http.MethodGet, "/csrf", RouteAuthCSRF, csrfToken)
	session := authG.Group("", authRequired)
	add(session, http.MethodGet, "/me", RouteAuthMe, d.Auth.Me)
	add(session, http.MethodPost, "/logout-all", RouteAuthLogoutAll, d.Auth.LogoutAll)
	add(session, http.MethodPost, "/api-keys", RouteAPIKeysCreate, d.Auth.CreateAPIKey, mw.RequireJWT)
	add(session, http.MethodGet, "/api-keys", RouteAPIKeysList, d.Auth.ListAPIKeys)
	add(session, http.MethodDelete, "/api-keys/:id", RouteAPIKeysRevoke, d.Auth.RevokeAPIKey, mw.RequireJWT)

	// Writes are limited per user (after auth), reads only by the global per-IP budget.
	todos := v1.Group("/todos", jsonLimit, authRequired, rateLimit(cfg.WriteRPS, 0, onlyWrites, byUser))
	add(todos, http.MethodPost, "", RouteTodosCreate, d.Todos.Create, idempotent(d.Idem))
	add(todos, http.MethodGet, "", RouteTodosList, d.Todos.List)
	add(todos, http.MethodGet, "/:id", RouteTodosGet, d.Todos.Get)
	add(todos, http.MethodPatch, "/:id", RouteTodosUpdate, d.Todos.Update)
	add(todos, http.MethodDelete, "/:id", RouteTodosDelete, d.Todos.Delete)
	add(todos, http.MethodGet, "/:id/audit", RouteTodosAudit, d.Todos.Audit, mw.RequireRole(auth.RoleAdmin))

	// Deliberately no e.RouteNotFound("/*", ...): a root catch-all 404 route wins over
	// the router's 405 detection, so every wrong-method request would become a 404.
	// RouterConfig.NotFoundHandler already renders unknown paths as JSON problems.
}

// IdempotencyTTL is how long a stored response can be replayed.
const IdempotencyTTL = 24 * time.Hour

func idempotent(store mw.IdempotencyStore) echo.MiddlewareFunc {
	if store == nil {
		return func(next echo.HandlerFunc) echo.HandlerFunc { return next }
	}
	return mw.Idempotency(store, IdempotencyTTL)
}

// add registers a named route. In v5 the RouteInfo returned by g.GET is a copy,
// so assigning info.Name afterwards does nothing; names must go through AddRoute.
func add(g *echo.Group, method, path, name string, h echo.HandlerFunc, m ...echo.MiddlewareFunc) {
	if _, err := g.AddRoute(echo.Route{Method: method, Path: path, Name: name, Handler: h, Middlewares: m}); err != nil {
		panic(err)
	}
}
