// Package handler is the HTTP adapter for the todo service: bind, validate, map,
// delegate, respond. It holds no business logic and never touches storage.
package handler

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"echo-todo-2/internal/apperr"
	"echo-todo-2/internal/httpx"
	"echo-todo-2/internal/todo"
)

// RouteGet is the route name used to build Location headers via reverse routing.
const RouteGet = "todos.get"

type Handler struct {
	svc *todo.Service
}

func New(svc *todo.Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) Create(c *echo.Context) error {
	var in CreateTodoRequest
	if err := httpx.BindJSON(c, &in); err != nil {
		return err
	}
	if err := c.ValidateCtx(&in); err != nil {
		return err
	}
	user, err := httpx.CurrentUser(c)
	if err != nil {
		return err
	}

	t, err := h.svc.Create(c.Request().Context(), user.UserID, in.toInput())
	if err != nil {
		return err
	}
	loc, err := c.Echo().Router().Routes().Reverse(RouteGet, t.ID)
	if err != nil {
		loc = "/v1/todos/" + t.ID.String()
	}
	return httpx.Created(c, loc, toDTO(t))
}

func (h *Handler) Get(c *echo.Context) error {
	user, err := httpx.CurrentUser(c)
	if err != nil {
		return err
	}
	id, err := httpx.PathUUID(c, "id")
	if err != nil {
		return err
	}
	t, err := h.svc.Get(c.Request().Context(), user.UserID, id)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toDTO(t))
}

func (h *Handler) List(c *echo.Context) error {
	user, err := httpx.CurrentUser(c)
	if err != nil {
		return err
	}
	limit, err := httpx.QueryParamOr(c, "limit", todo.DefaultLimit)
	if err != nil {
		return err
	}
	offset, err := httpx.QueryParamOr(c, "offset", 0)
	if err != nil {
		return err
	}
	if limit < 1 || limit > todo.MaxLimit {
		return apperr.Invalid("limit", "must be between 1 and 100")
	}
	if offset < 0 {
		return apperr.Invalid("offset", "must be at least 0")
	}
	statuses, err := httpx.QueryParams[string](c, "status")
	if err != nil {
		return err
	}
	f := todo.Filter{OwnerID: user.UserID, Limit: limit, Offset: offset}
	for _, s := range statuses {
		st := todo.Status(s)
		if !st.Valid() {
			return apperr.Invalid("status", "must be one of: open, done, archived")
		}
		f.Statuses = append(f.Statuses, st)
	}

	page, err := h.svc.List(c.Request().Context(), f)
	if err != nil {
		return err
	}
	resp := ListResponse{Items: make([]TodoResponse, 0, len(page.Items)), Total: page.Total, Limit: limit, Offset: offset}
	for i := range page.Items {
		resp.Items = append(resp.Items, toDTO(&page.Items[i]))
	}
	return c.JSON(http.StatusOK, resp)
}

func (h *Handler) Update(c *echo.Context) error {
	user, err := httpx.CurrentUser(c)
	if err != nil {
		return err
	}
	id, err := httpx.PathUUID(c, "id")
	if err != nil {
		return err
	}
	var in UpdateTodoRequest
	if err := httpx.BindJSON(c, &in); err != nil {
		return err
	}
	if err := c.ValidateCtx(&in); err != nil {
		return err
	}
	t, err := h.svc.Update(c.Request().Context(), user.UserID, id, in.toInput(), 0)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toDTO(t))
}

func (h *Handler) Delete(c *echo.Context) error {
	user, err := httpx.CurrentUser(c)
	if err != nil {
		return err
	}
	id, err := httpx.PathUUID(c, "id")
	if err != nil {
		return err
	}
	if err := h.svc.Delete(c.Request().Context(), user.UserID, id); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}
