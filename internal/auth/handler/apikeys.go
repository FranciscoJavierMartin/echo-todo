package handler

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v5"

	"echo-todo-2/internal/httpx"
)

type CreateAPIKeyRequest struct {
	Name string `json:"name" validate:"notblank,max=80"`
}

type APIKeyResponse struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Key        string     `json:"key,omitempty"` // only on creation
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

func (h *Handler) CreateAPIKey(c *echo.Context) error {
	p, err := httpx.CurrentUser(c)
	if err != nil {
		return err
	}
	var in CreateAPIKeyRequest
	if err := httpx.BindJSON(c, &in); err != nil {
		return err
	}
	if err := c.ValidateCtx(&in); err != nil {
		return err
	}
	raw, k, err := h.svc.CreateAPIKey(c.Request().Context(), p.UserID, in.Name)
	if err != nil {
		return err
	}
	c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
	return c.JSON(http.StatusCreated, APIKeyResponse{ID: k.ID.String(), Name: k.Name, Prefix: k.Prefix, Key: raw, CreatedAt: k.CreatedAt})
}

func (h *Handler) ListAPIKeys(c *echo.Context) error {
	p, err := httpx.CurrentUser(c)
	if err != nil {
		return err
	}
	keys, err := h.svc.ListAPIKeys(c.Request().Context(), p.UserID)
	if err != nil {
		return err
	}
	out := make([]APIKeyResponse, len(keys))
	for i, k := range keys {
		out[i] = APIKeyResponse{ID: k.ID.String(), Name: k.Name, Prefix: k.Prefix, CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt, RevokedAt: k.RevokedAt}
	}
	return c.JSON(http.StatusOK, map[string]any{"items": out})
}

func (h *Handler) RevokeAPIKey(c *echo.Context) error {
	p, err := httpx.CurrentUser(c)
	if err != nil {
		return err
	}
	id, err := httpx.PathUUID(c, "id")
	if err != nil {
		return err
	}
	if err := h.svc.RevokeAPIKey(c.Request().Context(), p.UserID, id); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}
