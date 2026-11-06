package app_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/app/apptest"
	authhandler "echo-todo-2/internal/auth/handler"
	"echo-todo-2/internal/httpx"
)

const password = "correct horse battery staple"

func register(t *testing.T, env *apptest.Env, email string) authhandler.UserResponse {
	t.Helper()
	rec := env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/auth/register",
		Body: map[string]string{"email": email, "password": password, "display_name": "Tester"}})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	return apptest.JSON[authhandler.UserResponse](t, rec)
}

func login(t *testing.T, env *apptest.Env, email string) authhandler.TokenResponse {
	t.Helper()
	rec := env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/auth/login",
		Body: map[string]string{"email": email, "password": password}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return apptest.JSON[authhandler.TokenResponse](t, rec)
}

func refresh(t *testing.T, env *apptest.Env, token string) (int, authhandler.TokenResponse, httpx.Problem) {
	t.Helper()
	rec := env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/auth/refresh",
		Body: map[string]string{"refresh_token": token}})
	var ok authhandler.TokenResponse
	var p httpx.Problem
	if rec.Code == http.StatusOK {
		ok = apptest.JSON[authhandler.TokenResponse](t, rec)
	} else {
		p = apptest.JSON[httpx.Problem](t, rec)
	}
	return rec.Code, ok, p
}

func TestAuthFlow(t *testing.T) {
	env := apptest.New(t)
	user := register(t, env, "Flow@Example.com")
	require.Equal(t, "flow@example.com", user.Email)

	// Duplicate email (case-insensitive) → 409.
	rec := env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/auth/register",
		Body: map[string]string{"email": "FLOW@example.com", "password": password, "display_name": "Again"}})
	require.Equal(t, http.StatusConflict, rec.Code)

	// Login issues both tokens.
	tokens := login(t, env, "flow@example.com")
	require.NotEmpty(t, tokens.AccessToken)
	require.NotEmpty(t, tokens.RefreshToken)
	require.Equal(t, "Bearer", tokens.TokenType)
	require.InDelta(t, 600, tokens.ExpiresIn, 2)

	me := env.Do(apptest.Request{Method: http.MethodGet, Path: "/v1/auth/me", Token: tokens.AccessToken})
	require.Equal(t, http.StatusOK, me.Code)
	require.Equal(t, user.ID, apptest.JSON[authhandler.UserResponse](t, me).ID)

	// Refresh rotates; the old token is rejected afterwards.
	code, rotated, _ := refresh(t, env, tokens.RefreshToken)
	require.Equal(t, http.StatusOK, code)
	require.NotEqual(t, tokens.RefreshToken, rotated.RefreshToken)

	// Reusing the rotated token revokes the whole family: even the fresh one dies.
	code, _, p := refresh(t, env, tokens.RefreshToken)
	require.Equal(t, http.StatusUnauthorized, code)
	require.Equal(t, "refresh_token_reused", p.Code)
	code, _, p = refresh(t, env, rotated.RefreshToken)
	require.Equal(t, http.StatusUnauthorized, code)
	require.Equal(t, "invalid_refresh_token", p.Code)

	// Logout revokes.
	fresh := login(t, env, "flow@example.com")
	rec = env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/auth/logout",
		Body: map[string]string{"refresh_token": fresh.RefreshToken}})
	require.Equal(t, http.StatusNoContent, rec.Code)
	code, _, _ = refresh(t, env, fresh.RefreshToken)
	require.Equal(t, http.StatusUnauthorized, code)
}

func TestLoginFailuresAreGeneric(t *testing.T) {
	env := apptest.New(t)
	register(t, env, "gen@example.com")
	wrong := env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/auth/login",
		Body: map[string]string{"email": "gen@example.com", "password": "nope-nope-nope"}})
	unknown := env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/auth/login",
		Body: map[string]string{"email": "ghost@example.com", "password": password}})
	require.Equal(t, http.StatusUnauthorized, wrong.Code)
	require.Equal(t, http.StatusUnauthorized, unknown.Code)
	pw, pu := apptest.JSON[httpx.Problem](t, wrong), apptest.JSON[httpx.Problem](t, unknown)
	require.Equal(t, pw.Detail, pu.Detail)
	require.Equal(t, "invalid_credentials", pw.Code)
}

func TestExpiredAccessTokenIsMachineReadable401(t *testing.T) {
	env := apptest.New(t)
	u := env.CreateUser("user")
	expired, _, err := env.App.Tokens.WithClock(func() time.Time { return time.Now().Add(-time.Hour) }).IssueAccess(u.ID, "user")
	require.NoError(t, err)
	rec := env.Do(apptest.Request{Method: http.MethodGet, Path: "/v1/todos", Token: expired})
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "token_expired", apptest.JSON[httpx.Problem](t, rec).Code)
	require.Equal(t, `Bearer realm="todo", error="token_expired"`, rec.Header().Get("WWW-Authenticate"))
}

func TestLogoutAll(t *testing.T) {
	env := apptest.New(t)
	register(t, env, "all@example.com")
	a := login(t, env, "all@example.com")
	b := login(t, env, "all@example.com")
	rec := env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/auth/logout-all", Token: a.AccessToken})
	require.Equal(t, http.StatusNoContent, rec.Code)
	for _, tok := range []string{a.RefreshToken, b.RefreshToken} {
		code, _, _ := refresh(t, env, tok)
		require.Equal(t, http.StatusUnauthorized, code)
	}
}

func TestBrowserCookieTransport(t *testing.T) {
	env := apptest.New(t)
	register(t, env, "cookie@example.com")
	rec := env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/auth/login",
		Headers: map[string]string{authhandler.ClientHeader: "browser"},
		Body:    map[string]string{"email": "cookie@example.com", "password": password}})
	require.Equal(t, http.StatusOK, rec.Code)
	body := apptest.JSON[authhandler.TokenResponse](t, rec)
	require.Empty(t, body.AccessToken, "browsers never see tokens in the body")
	require.Empty(t, body.RefreshToken)

	cookies := map[string]*http.Cookie{}
	for _, c := range rec.Result().Cookies() {
		cookies[c.Name] = c
	}
	access, refreshC := cookies[authhandler.AccessCookie], cookies[authhandler.RefreshCookie]
	require.NotNil(t, access)
	require.NotNil(t, refreshC)
	require.True(t, access.HttpOnly)
	require.Equal(t, http.SameSiteLaxMode, refreshC.SameSite)
	require.Equal(t, "/v1/auth", refreshC.Path)
	require.Positive(t, refreshC.MaxAge)

	// The access cookie authenticates API calls.
	todos := env.Do(apptest.Request{Method: http.MethodGet, Path: "/v1/todos",
		Headers: map[string]string{"Cookie": "access_token=" + access.Value}})
	require.Equal(t, http.StatusOK, todos.Code)

	// Cookie-authenticated writes need the CSRF double-submit token.
	rec = env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/auth/refresh",
		Headers: map[string]string{"Cookie": "refresh_token=" + refreshC.Value, authhandler.ClientHeader: "browser"}})
	require.Equal(t, http.StatusForbidden, rec.Code)

	csrfRec := env.Do(apptest.Request{Method: http.MethodGet, Path: "/v1/auth/csrf",
		Headers: map[string]string{"Cookie": "refresh_token=" + refreshC.Value}})
	require.Equal(t, http.StatusOK, csrfRec.Code)
	csrfTok := apptest.JSON[map[string]string](t, csrfRec)["csrf_token"]
	require.NotEmpty(t, csrfTok)

	// The refresh cookie works without a body.
	rec = env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/auth/refresh",
		Headers: map[string]string{
			"Cookie":                 "refresh_token=" + refreshC.Value + "; _csrf=" + csrfTok,
			"X-CSRF-Token":           csrfTok,
			authhandler.ClientHeader: "browser",
		}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, strings.Join(rec.Header().Values("Set-Cookie"), "\n"), "access_token=")
}

func TestAPIKeyAuthentication(t *testing.T) {
	env := apptest.New(t)
	register(t, env, "machine@example.com")
	tokens := login(t, env, "machine@example.com")

	rec := env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/auth/api-keys", Token: tokens.AccessToken,
		Body: map[string]string{"name": "ci"}})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	key := apptest.JSON[authhandler.APIKeyResponse](t, rec)
	require.True(t, strings.HasPrefix(key.Key, "todo_"))

	viaKey := func(k string) int {
		return env.Do(apptest.Request{Method: http.MethodGet, Path: "/v1/todos", Headers: map[string]string{"X-API-Key": k}}).Code
	}
	require.Equal(t, http.StatusOK, viaKey(key.Key))
	require.Equal(t, http.StatusUnauthorized, viaKey(key.Key+"x"))
	require.Equal(t, http.StatusUnauthorized, viaKey("todo_nope_nope"))

	// Keys cannot mint more keys.
	rec = env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/auth/api-keys",
		Headers: map[string]string{"X-API-Key": key.Key}, Body: map[string]string{"name": "escalate"}})
	require.Equal(t, http.StatusForbidden, rec.Code)

	list := env.Do(apptest.Request{Method: http.MethodGet, Path: "/v1/auth/api-keys", Token: tokens.AccessToken})
	require.Equal(t, http.StatusOK, list.Code)
	require.NotContains(t, list.Body.String(), key.Key, "raw keys are shown only once")

	rec = env.Do(apptest.Request{Method: http.MethodDelete, Path: "/v1/auth/api-keys/" + key.ID, Token: tokens.AccessToken})
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, http.StatusUnauthorized, viaKey(key.Key))
	rec = env.Do(apptest.Request{Method: http.MethodDelete, Path: "/v1/auth/api-keys/" + uuid.NewString(), Token: tokens.AccessToken})
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRegisterValidation(t *testing.T) {
	env := apptest.New(t)
	rec := env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/auth/register",
		Body: map[string]string{"email": "not-an-email", "password": "short", "display_name": " "}})
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	p := apptest.JSON[httpx.Problem](t, rec)
	require.Contains(t, p.Errors, "email")
	require.Contains(t, p.Errors, "password")
	require.Contains(t, p.Errors, "display_name")
}

// No PII or secrets in logs: emails, passwords and tokens never appear.
func TestLogsContainNoPII(t *testing.T) {
	env := apptest.New(t)
	register(t, env, "private.person@example.com")
	tokens := login(t, env, "private.person@example.com")
	env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/auth/login",
		Body: map[string]string{"email": "private.person@example.com", "password": "wrong-password-123"}})
	env.Do(apptest.Request{Method: http.MethodGet, Path: "/v1/todos", Token: tokens.AccessToken, Remote: "203.0.113.9:1234"})
	env.Do(apptest.Request{Method: http.MethodPost, Path: "/v1/auth/refresh", Body: map[string]string{"refresh_token": tokens.RefreshToken}})

	logs := env.Logs.String()
	require.Contains(t, logs, `"msg":"access"`)
	for _, secret := range []string{"private.person", password, "wrong-password-123", tokens.AccessToken, tokens.RefreshToken, "203.0.113.9"} {
		require.NotContains(t, logs, secret)
	}
}
