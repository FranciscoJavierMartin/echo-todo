package handler_test

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/auth"
	"echo-todo-2/internal/auth/authtest"
	"echo-todo-2/internal/auth/handler"
	"echo-todo-2/internal/httpx"
)

type env struct {
	e    *echo.Echo
	repo *authtest.Repo
	svc  *auth.Service
	user *auth.Principal
}

func newEnv(t *testing.T) *env {
	t.Helper()
	repo := authtest.NewRepo()
	svc, err := auth.NewService(repo, authtest.NoTx{}, auth.NewTokens([]byte("0123456789abcdef0123456789abcdef"), time.Minute), time.Hour,
		auth.PasswordParams{MemoryKiB: 1024, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32})
	require.NoError(t, err)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	e := echo.NewWithConfig(echo.Config{
		Logger: log, HTTPErrorHandler: httpx.ErrorHandler(log, false),
		Validator: httpx.NewValidator(), JSONSerializer: httpx.StrictJSONSerializer{},
	})
	en := &env{e: e, repo: repo, svc: svc}
	h := handler.New(svc, true, time.Minute, time.Hour)
	asUser := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if en.user != nil {
				httpx.SetPrincipal(c, en.user)
			}
			return next(c)
		}
	}
	g := e.Group("/v1/auth", asUser)
	g.POST("/register", h.Register)
	g.POST("/login", h.Login)
	g.POST("/refresh", h.Refresh)
	g.POST("/logout", h.Logout)
	g.POST("/logout-all", h.LogoutAll)
	g.GET("/me", h.Me)
	g.POST("/api-keys", h.CreateAPIKey)
	g.GET("/api-keys", h.ListAPIKeys)
	g.DELETE("/api-keys/:id", h.RevokeAPIKey)
	return en
}

func (en *env) do(method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	en.e.ServeHTTP(rec, req)
	return rec
}

func TestAuthenticatedEndpointsRequirePrincipal(t *testing.T) {
	en := newEnv(t)
	for _, c := range []struct{ m, p, b string }{
		{http.MethodGet, "/v1/auth/me", ""},
		{http.MethodPost, "/v1/auth/logout-all", ""},
		{http.MethodPost, "/v1/auth/api-keys", `{"name":"x"}`},
		{http.MethodGet, "/v1/auth/api-keys", ""},
		{http.MethodDelete, "/v1/auth/api-keys/" + uuid.NewString(), ""},
	} {
		require.Equal(t, http.StatusUnauthorized, en.do(c.m, c.p, c.b).Code, c.p)
	}
}

func TestMalformedAndInvalidBodies(t *testing.T) {
	en := newEnv(t)
	en.user = &auth.Principal{UserID: uuid.New(), Role: auth.RoleUser, Via: "jwt"}
	for _, p := range []string{"/v1/auth/register", "/v1/auth/login", "/v1/auth/refresh", "/v1/auth/logout", "/v1/auth/api-keys"} {
		require.Equal(t, http.StatusUnprocessableEntity, en.do(http.MethodPost, p, `{"oops"`).Code, p)
	}
	require.Equal(t, http.StatusUnprocessableEntity, en.do(http.MethodPost, "/v1/auth/login", `{}`).Code)
	require.Equal(t, http.StatusUnprocessableEntity, en.do(http.MethodPost, "/v1/auth/api-keys", `{"name":" "}`).Code)
	require.Equal(t, http.StatusUnprocessableEntity, en.do(http.MethodPost, "/v1/auth/refresh", `{"refresh_token":"`+strings.Repeat("x", 600)+`"}`).Code)
	require.Equal(t, http.StatusUnprocessableEntity, en.do(http.MethodPost, "/v1/auth/logout", `{"refresh_token":"`+strings.Repeat("x", 600)+`"}`).Code)
	require.Equal(t, http.StatusNotFound, en.do(http.MethodDelete, "/v1/auth/api-keys/not-a-uuid", "").Code)
}

func TestHappyPathsAndCookieClearing(t *testing.T) {
	en := newEnv(t)
	rec := en.do(http.MethodPost, "/v1/auth/register", `{"email":"h@example.com","password":"long enough pw","display_name":"H"}`)
	require.Equal(t, http.StatusCreated, rec.Code)

	rec = en.do(http.MethodPost, "/v1/auth/login", `{"email":"h@example.com","password":"long enough pw"}`, handler.ClientHeader, "browser")
	require.Equal(t, http.StatusOK, rec.Code)
	var refresh *http.Cookie
	for _, c := range rec.Result().Cookies() {
		require.True(t, c.Secure, "secure cookies when enabled")
		if c.Name == handler.RefreshCookie {
			refresh = c
		}
	}
	require.NotNil(t, refresh)

	// Failed refresh clears the cookies.
	rec = en.do(http.MethodPost, "/v1/auth/refresh", "", "Cookie", "refresh_token=bogus")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Contains(t, strings.Join(rec.Header().Values("Set-Cookie"), ";"), "Max-Age=0")

	rec = en.do(http.MethodPost, "/v1/auth/logout", "", "Cookie", "refresh_token="+refresh.Value)
	require.Equal(t, http.StatusNoContent, rec.Code)

	u, err := en.repo.UserByEmail(t.Context(), "h@example.com")
	require.NoError(t, err)
	en.user = &auth.Principal{UserID: u.ID, Role: u.Role, Via: "jwt"}
	require.Equal(t, http.StatusOK, en.do(http.MethodGet, "/v1/auth/me", "").Code)
	require.Equal(t, http.StatusNoContent, en.do(http.MethodPost, "/v1/auth/logout-all", "", "Cookie", "access_token=x").Code)

	en.user = &auth.Principal{UserID: uuid.New(), Role: auth.RoleUser}
	require.Equal(t, http.StatusNotFound, en.do(http.MethodGet, "/v1/auth/me", "").Code)
}

func TestServiceFailuresSurfaceAs500(t *testing.T) {
	en := newEnv(t)
	en.user = &auth.Principal{UserID: uuid.New(), Role: auth.RoleUser, Via: "jwt"}
	en.repo.Err = errors.New("db down")
	for _, c := range []struct{ m, p, b string }{
		{http.MethodPost, "/v1/auth/register", `{"email":"x@example.com","password":"long enough pw","display_name":"X"}`},
		{http.MethodPost, "/v1/auth/login", `{"email":"x@example.com","password":"pw"}`},
		{http.MethodPost, "/v1/auth/logout", `{"refresh_token":"t"}`},
		{http.MethodPost, "/v1/auth/logout-all", ""},
		{http.MethodGet, "/v1/auth/me", ""},
		{http.MethodPost, "/v1/auth/api-keys", `{"name":"k"}`},
		{http.MethodGet, "/v1/auth/api-keys", ""},
		{http.MethodDelete, "/v1/auth/api-keys/" + uuid.NewString(), ""},
	} {
		rec := en.do(c.m, c.p, c.b)
		require.Equal(t, http.StatusInternalServerError, rec.Code, c.p)
		require.NotContains(t, rec.Body.String(), "db down")
	}
}
