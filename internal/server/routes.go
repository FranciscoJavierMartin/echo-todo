package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"echo-todo-2/internal/api"
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
	RouteTodosExport = "todos.export"

	RouteAttachmentsUpload = "attachments.upload"
	RouteAttachmentsList   = "attachments.list"
	RouteAttachmentsGet    = "attachments.get"
	RouteAttachmentsDelete = "attachments.delete"

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
	h.metrics = metricsHandler(d.Telemetry.Registry, cfg.MetricsToken)

	// Pre: runs before routing, so it also covers 404/405. Outermost first.
	// AccessLog sits outside Recover so panicking requests are still access-logged.
	pre := []echo.MiddlewareFunc{
		mw.RequestContext(d.Logger),
		tracing(d.Telemetry.TracerProvider),
		mw.AccessLog(mw.AccessLogConfig{Skipper: mw.SkipPaths(quietPaths...), IncludeClientIP: cfg.LogClientIP}),
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

	// Every API route goes through the wrapper generated from api/openapi.yaml.
	impl := &API{h: h}
	w := &api.ServerInterfaceWrapper{Handler: impl}
	authRequired := mw.AuthRequired(d.Tokens, d.APIKeys)
	adminOnly := mw.RequireRole(auth.RoleAdmin)

	e.GET("/healthz", spec(w.GetHealth))
	e.GET("/readyz", spec(w.GetReadiness))
	e.GET("/metrics", spec(w.GetMetrics))
	docs := e.Group("/docs")
	if cfg.Env == "prod" {
		docs.Use(authRequired, adminOnly)
	}
	docs.GET("", scalarUI, scalarCSP)
	docs.GET("/scalar.js", scalarJS)
	docs.GET("/openapi.yaml", spec(w.GetOpenAPISpec))

	// Wildcard routes have no OpenAPI equivalent of "*"; the API method is called directly.
	debug := e.Group("/debug", authRequired, adminOnly)
	debug.GET("/pprof/*", func(c *echo.Context) error { return impl.GetPprof(c, c.Param("*")) })

	v1 := e.Group("/v1")
	jsonLimit := middleware.BodyLimit(cfg.MaxBodyBytes)

	// Credential endpoints get a tight per-IP budget (brute force, credential stuffing).
	authG := v1.Group("/auth", jsonLimit, rateLimit(cfg.AuthRPS, int(cfg.AuthRPS*2), nil, byIP))
	add(authG, http.MethodPost, "/register", RouteAuthRegister, spec(w.RegisterUser))
	add(authG, http.MethodPost, "/login", RouteAuthLogin, spec(w.Login))
	add(authG, http.MethodPost, "/refresh", RouteAuthRefresh, spec(w.RefreshTokens))
	add(authG, http.MethodPost, "/logout", RouteAuthLogout, spec(w.Logout))
	add(authG, http.MethodGet, "/csrf", RouteAuthCSRF, spec(w.GetCsrfToken))
	session := authG.Group("", authRequired)
	add(session, http.MethodGet, "/me", RouteAuthMe, spec(w.GetMe))
	add(session, http.MethodPost, "/logout-all", RouteAuthLogoutAll, spec(w.LogoutAll))
	add(session, http.MethodPost, "/api-keys", RouteAPIKeysCreate, spec(w.CreateApiKey), mw.RequireJWT)
	add(session, http.MethodGet, "/api-keys", RouteAPIKeysList, spec(w.ListApiKeys))
	add(session, http.MethodDelete, "/api-keys/:id", RouteAPIKeysRevoke, spec(w.RevokeApiKey), mw.RequireJWT)

	// Writes are limited per user (after auth), reads only by the global per-IP budget.
	// The JSON cap is skipped for uploads, which get their own (larger) route-level cap.
	todosLimit := middleware.BodyLimitWithConfig(middleware.BodyLimitConfig{
		LimitBytes: cfg.MaxBodyBytes,
		Skipper: func(c *echo.Context) bool {
			return c.Request().Method == http.MethodPost && strings.HasSuffix(c.Path(), "/attachments")
		},
	})
	todos := v1.Group("/todos", todosLimit, authRequired, rateLimit(cfg.WriteRPS, 0, onlyWrites, byUser))
	add(todos, http.MethodPost, "", RouteTodosCreate, spec(w.CreateTodo), idempotent(d.Idem))
	add(todos, http.MethodGet, "", RouteTodosList, spec(w.ListTodos))
	add(todos, http.MethodGet, "/:id", RouteTodosGet, spec(w.GetTodo))
	add(todos, http.MethodPatch, "/:id", RouteTodosUpdate, spec(w.UpdateTodo))
	add(todos, http.MethodDelete, "/:id", RouteTodosDelete, spec(w.DeleteTodo))
	add(todos, http.MethodGet, "/:id/audit", RouteTodosAudit, spec(w.GetTodoAudit), adminOnly)
	add(todos, http.MethodGet, "/export/*", RouteTodosExport, func(c *echo.Context) error { return impl.ExportTodos(c, c.Param("*")) })
	add(todos, http.MethodPost, "/:id/attachments", RouteAttachmentsUpload, spec(w.UploadAttachment),
		middleware.BodyLimit(cfg.MaxUploadBytes+(64<<10))) // + multipart framing
	add(todos, http.MethodGet, "/:id/attachments", RouteAttachmentsList, spec(w.ListAttachments))
	add(todos, http.MethodGet, "/:id/attachments/:aid", RouteAttachmentsGet, spec(w.DownloadAttachment))
	add(todos, http.MethodDelete, "/:id/attachments/:aid", RouteAttachmentsDelete, spec(w.DeleteAttachment))

	if d.UI != nil {
		registerSPA(e, h, d.UI)
	}

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
