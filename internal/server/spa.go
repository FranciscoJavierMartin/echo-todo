package server

import (
	"io/fs"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

// registerSPA serves the embedded UI under /app:
//
//	/app/assets/*  hashed files, StaticFS, cached for a year (immutable)
//	/app           index.html, never cached
//	/app/*         deep links: HTML5 mode falls back to index.html on a router 404
//
// It is mounted on routes, not with e.Use(middleware.Static...), because Static
// registered globally runs before route/group middleware and would bypass guards;
// and under /app rather than / so unknown API paths still get JSON 404s and 405s.
func registerSPA(e *echo.Echo, h *Handlers, ui fs.FS) {
	dist := echo.MustSubFS(ui, "dist")

	e.StaticFS("/app/assets/", echo.MustSubFS(dist, "assets"), // trailing slash: "/app/assets*" would match "/app/assetsX"
		cacheControl("public, max-age=31536000, immutable"))

	index := func(c *echo.Context) error { return c.FileFS("index.html", dist) }
	e.GET("/app", index, cacheControl("no-cache"))
	e.GET("/", func(c *echo.Context) error { return c.Redirect(http.StatusFound, "/app") })

	e.RouteNotFound("/app/*", h.NotFound,
		cacheControl("no-cache"),
		middleware.StaticWithConfig(middleware.StaticConfig{
			Filesystem: dist,
			HTML5:      true,
			Skipper: func(c *echo.Context) bool {
				m := c.Request().Method
				return m != http.MethodGet && m != http.MethodHead
			},
		}),
	)
}

func cacheControl(v string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			c.Response().Header().Set(echo.HeaderCacheControl, v)
			return next(c)
		}
	}
}
