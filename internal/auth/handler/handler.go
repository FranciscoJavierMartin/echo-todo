// Package handler is the HTTP adapter for authentication.
package handler

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v5"

	"echo-todo-2/internal/auth"
	"echo-todo-2/internal/httpx"
)

const (
	AccessCookie  = "access_token"
	RefreshCookie = "refresh_token"
	// RefreshCookiePath scopes the refresh cookie to the auth endpoints only.
	RefreshCookiePath = "/v1/auth"
	// ClientHeader opts a browser client into cookie transport.
	ClientHeader = "X-Client"
)

type Handler struct {
	svc        *auth.Service
	secure     bool
	accessTTL  time.Duration
	refreshTTL time.Duration
}

func New(svc *auth.Service, secureCookies bool, accessTTL, refreshTTL time.Duration) *Handler {
	return &Handler{svc: svc, secure: secureCookies, accessTTL: accessTTL, refreshTTL: refreshTTL}
}

type RegisterRequest struct {
	Email       string `json:"email" validate:"required,email,max=254"`
	Password    string `json:"password" validate:"required,min=10,max=128"`
	DisplayName string `json:"display_name" validate:"notblank,max=80"`
}

type LoginRequest struct {
	Email    string `json:"email" validate:"required,max=254"`
	Password string `json:"password" validate:"required,max=128"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" validate:"max=512"`
}

type UserResponse struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
}

type TokenResponse struct {
	AccessToken      string       `json:"access_token,omitempty"`
	TokenType        string       `json:"token_type"`
	ExpiresIn        int          `json:"expires_in"`
	RefreshToken     string       `json:"refresh_token,omitempty"`
	RefreshExpiresAt time.Time    `json:"refresh_expires_at"`
	User             UserResponse `json:"user"`
}

func toUser(u *auth.User) UserResponse {
	return UserResponse{ID: u.ID.String(), Email: u.Email, DisplayName: u.DisplayName, Role: u.Role, CreatedAt: u.CreatedAt}
}

func (h *Handler) Register(c *echo.Context) error {
	var in RegisterRequest
	if err := httpx.BindJSON(c, &in); err != nil {
		return err
	}
	if err := c.ValidateCtx(&in); err != nil {
		return err
	}
	u, err := h.svc.Register(c.Request().Context(), in.Email, in.Password, in.DisplayName)
	if err != nil {
		return err
	}
	return httpx.Created(c, "/v1/auth/me", toUser(u))
}

func (h *Handler) Login(c *echo.Context) error {
	var in LoginRequest
	if err := httpx.BindJSON(c, &in); err != nil {
		return err
	}
	if err := c.ValidateCtx(&in); err != nil {
		return err
	}
	pair, err := h.svc.Login(c.Request().Context(), in.Email, in.Password, c.Request().UserAgent())
	if err != nil {
		return err
	}
	return h.respondTokens(c, pair)
}

func (h *Handler) Refresh(c *echo.Context) error {
	raw, err := h.refreshTokenFrom(c)
	if err != nil {
		return err
	}
	pair, err := h.svc.Refresh(c.Request().Context(), raw, c.Request().UserAgent())
	if err != nil {
		h.clearCookies(c)
		return err
	}
	return h.respondTokens(c, pair)
}

func (h *Handler) Logout(c *echo.Context) error {
	raw, err := h.refreshTokenFrom(c)
	if err != nil {
		return err
	}
	if err := h.svc.Logout(c.Request().Context(), raw); err != nil {
		return err
	}
	h.clearCookies(c)
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) LogoutAll(c *echo.Context) error {
	p, err := httpx.CurrentUser(c)
	if err != nil {
		return err
	}
	if err := h.svc.LogoutAll(c.Request().Context(), p.UserID); err != nil {
		return err
	}
	h.clearCookies(c)
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) Me(c *echo.Context) error {
	p, err := httpx.CurrentUser(c)
	if err != nil {
		return err
	}
	u, err := h.svc.Me(c.Request().Context(), p.UserID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toUser(u))
}

// refreshTokenFrom reads the token from the JSON body, falling back to the cookie.
func (h *Handler) refreshTokenFrom(c *echo.Context) (string, error) {
	var in RefreshRequest
	if err := httpx.BindJSON(c, &in); err != nil {
		return "", err
	}
	if err := c.ValidateCtx(&in); err != nil {
		return "", err
	}
	if in.RefreshToken != "" {
		return in.RefreshToken, nil
	}
	if ck, err := c.Cookie(RefreshCookie); err == nil {
		return ck.Value, nil
	}
	return "", nil
}

func isBrowser(c *echo.Context) bool {
	return c.Request().Header.Get(ClientHeader) == "browser"
}

func (h *Handler) respondTokens(c *echo.Context, pair *auth.TokenPair) error {
	resp := TokenResponse{
		AccessToken:      pair.AccessToken,
		TokenType:        "Bearer",
		ExpiresIn:        int(time.Until(pair.AccessExpiresAt).Seconds()),
		RefreshToken:     pair.RefreshToken,
		RefreshExpiresAt: pair.RefreshExpiresAt,
		User:             toUser(pair.User),
	}
	if isBrowser(c) {
		// Browsers get HttpOnly cookies; tokens never touch JavaScript.
		c.SetCookie(h.cookie(AccessCookie, pair.AccessToken, "/", h.accessTTL))
		c.SetCookie(h.cookie(RefreshCookie, pair.RefreshToken, RefreshCookiePath, h.refreshTTL))
		resp.AccessToken, resp.RefreshToken = "", ""
	}
	c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
	return c.JSON(http.StatusOK, resp)
}

func (h *Handler) cookie(name, value, path string, ttl time.Duration) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteLaxMode,
	}
}

func (h *Handler) clearCookies(c *echo.Context) {
	if _, err := c.Cookie(RefreshCookie); err != nil {
		if _, err := c.Cookie(AccessCookie); err != nil {
			return
		}
	}
	c.SetCookie(h.cookie(AccessCookie, "", "/", -time.Second))
	c.SetCookie(h.cookie(RefreshCookie, "", RefreshCookiePath, -time.Second))
}
