package server

import (
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

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
)

var quietPaths = []string{"/healthz", "/readyz", "/metrics"}

func registerRoutes(e *echo.Echo, h *Handlers) {
	d := h.deps

	// Pre: runs before routing, so it also covers 404/405. Outermost first.
	// AccessLog sits outside Recover so panicking requests are still access-logged.
	e.Pre(
		mw.RequestContext(d.Logger),
		mw.AccessLog(mw.SkipPaths(quietPaths...)),
		mw.Recover(),
		middleware.RemoveTrailingSlash(),
		middleware.BodyLimit(d.Config.MaxUploadBytes+(64<<10)), // hard cap incl. multipart overhead
	)

	e.GET("/healthz", h.Health)

	v1 := e.Group("/v1")
	jsonLimit := middleware.BodyLimit(d.Config.MaxBodyBytes)

	todos := v1.Group("/todos", jsonLimit, mw.AuthRequired(d.Tokens))
	add(todos, http.MethodPost, "", RouteTodosCreate, d.Todos.Create)
	add(todos, http.MethodGet, "", RouteTodosList, d.Todos.List)
	add(todos, http.MethodGet, "/:id", RouteTodosGet, d.Todos.Get)
	add(todos, http.MethodPatch, "/:id", RouteTodosUpdate, d.Todos.Update)
	add(todos, http.MethodDelete, "/:id", RouteTodosDelete, d.Todos.Delete)

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
