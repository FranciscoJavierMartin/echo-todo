package httpx

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"echo-todo-2/internal/apperr"
	"echo-todo-2/internal/auth"
)

const principalKey = "principal"

// SetPrincipal stores the authenticated caller in the echo context.
func SetPrincipal(c *echo.Context, p *auth.Principal) {
	c.Set(principalKey, p)
}

// CurrentUser returns the authenticated caller or a 401.
func CurrentUser(c *echo.Context) (*auth.Principal, error) {
	p, err := echo.ContextGet[*auth.Principal](c, principalKey)
	if err != nil || p == nil {
		return nil, apperr.Unauthenticated("authentication required").WithCode("unauthenticated")
	}
	return p, nil
}

// PathUUID parses a UUID path parameter. Malformed IDs are reported as 404, since
// no resource can live at that URL (and it keeps the ID space opaque).
func PathUUID(c *echo.Context, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		return uuid.Nil, apperr.NotFound("resource not found").Wrap(err)
	}
	return id, nil
}

// Created writes 201 with a Location header.
func Created(c *echo.Context, location string, body any) error {
	c.Response().Header().Set(echo.HeaderLocation, location)
	return c.JSON(http.StatusCreated, body)
}
