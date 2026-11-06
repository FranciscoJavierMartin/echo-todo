package middleware_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/auth"
	"echo-todo-2/internal/httpx"
	mw "echo-todo-2/internal/httpx/middleware"
	"echo-todo-2/internal/platform/logger"
)

func newEcho() (*echo.Echo, *bytes.Buffer) {
	var buf bytes.Buffer
	log := logger.NewWithWriter("prod", &buf)
	e := echo.NewWithConfig(echo.Config{Logger: log, HTTPErrorHandler: httpx.ErrorHandler(log, false)})
	e.Pre(mw.RequestContext(log), mw.AccessLog(mw.SkipPaths("/skip")), mw.Recover())
	return e, &buf
}

func get(e *echo.Echo, path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &m), line)
		out = append(out, m)
	}
	return out
}

func TestRequestContext_ReusesValidAndReplacesInvalidIDs(t *testing.T) {
	e, _ := newEcho()
	var seen string
	e.GET("/x", func(c *echo.Context) error {
		seen = logger.RequestID(c.Request().Context())
		return c.NoContent(http.StatusOK)
	})

	rec := get(e, "/x", map[string]string{echo.HeaderXRequestID: "abc-123"})
	require.Equal(t, "abc-123", rec.Header().Get(echo.HeaderXRequestID))
	require.Equal(t, "abc-123", seen)

	rec = get(e, "/x", map[string]string{echo.HeaderXRequestID: "bad id\nwith newline"})
	_, err := uuid.Parse(rec.Header().Get(echo.HeaderXRequestID))
	require.NoError(t, err)

	rec = get(e, "/x", map[string]string{echo.HeaderXRequestID: strings.Repeat("a", 65)})
	require.Len(t, rec.Header().Get(echo.HeaderXRequestID), 36)

	rec = get(e, "/unknown", nil)
	require.NotEmpty(t, rec.Header().Get(echo.HeaderXRequestID), "Pre middleware also covers 404s")
}

func TestAccessLog_FieldsAndSkipper(t *testing.T) {
	e, buf := newEcho()
	uid := uuid.New()
	e.GET("/items/:id", func(c *echo.Context) error {
		mw.SetPrincipal(c, &auth.Principal{UserID: uid, Role: auth.RoleUser})
		return c.String(http.StatusAccepted, "ok")
	})
	e.GET("/skip", func(c *echo.Context) error { return c.NoContent(http.StatusOK) })

	get(e, "/items/42", map[string]string{echo.HeaderXRequestID: "rid-1"})
	get(e, "/skip", nil)

	lines := logLines(t, buf)
	require.Len(t, lines, 1)
	l := lines[0]
	require.Equal(t, "access", l["msg"])
	require.Equal(t, "rid-1", l["request_id"])
	require.Equal(t, uid.String(), l["user_id"])
	require.Equal(t, float64(http.StatusAccepted), l["status"])
	require.Equal(t, "/items/:id", l["route"], "route template, never the raw path")
	require.Contains(t, l, "latency")
}

func TestAccessLog_ErrorStatusAndUnmatched(t *testing.T) {
	e, buf := newEcho()
	get(e, "/nothing-here", nil)
	l := logLines(t, buf)[0]
	require.Equal(t, float64(404), l["status"])
	require.Equal(t, "<unmatched>", l["route"])
}

func TestRecover_PanicBecomes500WithRequestContext(t *testing.T) {
	e, buf := newEcho()
	e.GET("/boom", func(c *echo.Context) error { panic("kaboom") })
	rec := get(e, "/boom", map[string]string{echo.HeaderXRequestID: "rid-panic"})

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.NotContains(t, rec.Body.String(), "kaboom")
	require.Contains(t, rec.Body.String(), `"code":"panic"`)

	var panicLine, accessLine map[string]any
	for _, l := range logLines(t, buf) {
		switch l["msg"] {
		case "panic recovered":
			panicLine = l
		case "access":
			accessLine = l
		}
	}
	require.NotNil(t, panicLine)
	require.Equal(t, "rid-panic", panicLine["request_id"])
	require.Contains(t, panicLine["stack"], "goroutine")
	require.NotNil(t, accessLine, "panicking requests are still access-logged")
	require.Equal(t, float64(500), accessLine["status"])
}

func TestRecover_ErrorPanicAndAbortHandler(t *testing.T) {
	e, _ := newEcho()
	e.GET("/err", func(c *echo.Context) error { panic(http.ErrNoCookie) })
	require.Equal(t, http.StatusInternalServerError, get(e, "/err", nil).Code)

	e.GET("/abort", func(c *echo.Context) error { panic(http.ErrAbortHandler) })
	require.PanicsWithValue(t, http.ErrAbortHandler, func() { get(e, "/abort", nil) })
}

func TestRequireRole(t *testing.T) {
	e, _ := newEcho()
	setRole := func(role string) echo.MiddlewareFunc {
		return func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c *echo.Context) error {
				if role != "" {
					mw.SetPrincipal(c, &auth.Principal{UserID: uuid.New(), Role: role})
				}
				return next(c)
			}
		}
	}
	ok := func(c *echo.Context) error { return c.NoContent(http.StatusOK) }
	e.GET("/user", ok, setRole(auth.RoleUser), mw.RequireRole(auth.RoleAdmin))
	e.GET("/admin", ok, setRole(auth.RoleAdmin), mw.RequireRole(auth.RoleAdmin))
	e.GET("/anon", ok, setRole(""), mw.RequireRole(auth.RoleAdmin))

	require.Equal(t, http.StatusForbidden, get(e, "/user", nil).Code)
	require.Equal(t, http.StatusOK, get(e, "/admin", nil).Code)
	require.Equal(t, http.StatusUnauthorized, get(e, "/anon", nil).Code)
}
