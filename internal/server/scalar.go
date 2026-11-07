package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v5"
)

// The Scalar API Reference. The bundle is vendored (see scripts/vendor-scalar.sh)
// and read its configuration from the script element with id "api-reference":
// it inserts its own mount div before that element, fetches data-url and runs.

//go:embed scalar.js.gz
var scalarBundle []byte

//go:embed scalar/fonts
var scalarFonts embed.FS

// scalarCSPValue relaxes the global CSP for the reference page only: Scalar
// injects its stylesheet as an inline <style>. Scripts stay same-origin —
// no 'unsafe-inline', no CDN. Fonts and everything else are served by us;
// the page talks to no origin outside our own, offline included.
const scalarCSPValue = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; font-src 'self'; connect-src 'self'; " +
	"object-src 'none'; base-uri 'self'; frame-ancestors 'self'; form-action 'self'"

// scalarPage is the reference shell. Scalar mounts into a div it inserts
// before the #api-reference script, so no markup of our own is needed.
const scalarPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Todo API Reference</title></head>
<body>
<script id="api-reference" data-url="/docs/openapi.yaml" data-configuration='%s' src="/docs/scalar.js"></script>
</body></html>`

// scalarCSP swaps the strict global CSP for the reference page only.
func scalarCSP(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		c.Response().Header().Set(echo.HeaderContentSecurityPolicy, scalarCSPValue)
		return next(c)
	}
}

// scalarUI renders the reference page. The "Try it" server URL is the origin
// the client used, overriding the spec's localhost default; json.Marshal plus
// HTML escaping keeps a hostile Host header inside the JSON and the attribute.
func scalarUI(c *echo.Context) error {
	cfg, _ := json.Marshal(map[string]any{"servers": []map[string]string{
		{"url": c.Scheme() + "://" + c.Request().Host},
	}})
	return c.HTML(http.StatusOK, fmt.Sprintf(scalarPage, html.EscapeString(string(cfg))))
}

// scalarJS serves the vendored bundle, always pre-compressed (see
// scripts/vendor-scalar.sh): 1.4 MB instead of 4.9 MB on the wire.
func scalarJS(c *echo.Context) error {
	h := c.Response().Header()
	h.Set(echo.HeaderContentEncoding, "gzip")
	h.Set(echo.HeaderContentLength, strconv.Itoa(len(scalarBundle)))
	h.Set(echo.HeaderCacheControl, "public, max-age=86400")
	return c.Blob(http.StatusOK, "text/javascript; charset=utf-8", scalarBundle)
}

// scalarFont serves the vendored webfonts the bundle's stylesheet requests,
// plus OFL.txt: the fonts must ship with their license notice (OFL §2), so it
// is served next to them. Only the woff2 files and that one text are reachable
// from the embedded directory.
func scalarFont(c *echo.Context) error {
	name := c.Param("file")
	if strings.ContainsAny(name, "/\\") || name == ".." || name == "." {
		return echo.ErrNotFound
	}
	var ctype string
	switch {
	case strings.HasSuffix(name, ".woff2"):
		ctype = "font/woff2"
	case strings.HasSuffix(name, ".txt"):
		ctype = "text/plain; charset=utf-8"
	default:
		return echo.ErrNotFound
	}
	b, err := scalarFonts.ReadFile("scalar/fonts/" + name)
	if err != nil {
		return echo.ErrNotFound
	}
	c.Response().Header().Set(echo.HeaderContentLength, strconv.Itoa(len(b)))
	c.Response().Header().Set(echo.HeaderCacheControl, "public, max-age=86400")
	return c.Blob(http.StatusOK, ctype, b)
}
