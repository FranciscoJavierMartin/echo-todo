// Package handler is the HTTP adapter for the todo service: bind, validate, map,
// delegate, respond. It holds no business logic and never touches storage.
package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"echo-todo-2/internal/apperr"
	"echo-todo-2/internal/httpx"
	"echo-todo-2/internal/todo"
)

// RouteGet is the route name used to build Location headers via reverse routing.
const RouteGet = "todos.get"

type Handler struct {
	svc *todo.Service
	att *todo.AttachmentService
}

func New(svc *todo.Service) *Handler {
	return &Handler{svc: svc}
}

// WithAttachments enables the attachment endpoints.
func (h *Handler) WithAttachments(att *todo.AttachmentService) *Handler {
	h.att = att
	return h
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
	httpx.SetETag(c, t.Version)
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
	httpx.SetETag(c, t.Version)
	return c.JSON(http.StatusOK, toDTO(t))
}

// List: GET /v1/todos?status=open&priority=1,2&due_before=...&due_after=...&q=...
// &sort=-priority,due_at&limit=20&cursor=...&count=true
func (h *Handler) List(c *echo.Context) error {
	user, err := httpx.CurrentUser(c)
	if err != nil {
		return err
	}
	q, err := parseListQuery(c)
	if err != nil {
		return err
	}
	q.OwnerID = user.UserID

	page, err := h.svc.List(c.Request().Context(), q)
	if err != nil {
		return err
	}
	resp := ListResponse{Items: make([]TodoResponse, 0, len(page.Items)), Total: page.Total, Limit: q.Limit}
	if page.NextCursor != "" {
		resp.NextCursor = &page.NextCursor
	}
	for i := range page.Items {
		resp.Items = append(resp.Items, toDTO(&page.Items[i]))
	}
	return c.JSON(http.StatusOK, resp)
}

func parseListQuery(c *echo.Context) (todo.ListQuery, error) {
	var q todo.ListQuery
	var err error
	if q.Limit, err = httpx.QueryParamOr(c, "limit", todo.DefaultLimit); err != nil {
		return q, err
	}
	if q.Limit < 1 || q.Limit > todo.MaxLimit {
		return q, apperr.Invalid("limit", "must be between 1 and 100")
	}
	if q.WithCount, err = httpx.QueryParamOr(c, "count", false); err != nil {
		return q, err
	}
	statuses, err := httpx.QueryParams[string](c, "status")
	if err != nil {
		return q, err
	}
	for _, s := range statuses {
		st := todo.Status(s)
		if !st.Valid() {
			return q, apperr.Invalid("status", "must be one of: open, done, archived")
		}
		q.Statuses = append(q.Statuses, st)
	}
	if q.Priorities, err = httpx.QueryParams[int](c, "priority"); err != nil {
		return q, err
	}
	for _, p := range q.Priorities {
		if p < 1 || p > 5 {
			return q, apperr.Invalid("priority", "values must be between 1 and 5")
		}
	}
	if q.DueBefore, err = optionalTime(c, "due_before"); err != nil {
		return q, err
	}
	if q.DueAfter, err = optionalTime(c, "due_after"); err != nil {
		return q, err
	}
	q.Query = strings.TrimSpace(c.QueryParam("q"))
	if len([]rune(q.Query)) > 200 {
		return q, apperr.Invalid("q", "must be at most 200 characters")
	}
	q.SortSpec = c.QueryParam("sort")
	q.Cursor = c.QueryParam("cursor")
	return q, nil
}

func optionalTime(c *echo.Context, key string) (*time.Time, error) {
	if c.QueryParam(key) == "" {
		return nil, nil
	}
	t, err := httpx.QueryParamOr(c, key, time.Time{})
	if err != nil {
		return nil, apperr.Invalid(key, "must be an RFC 3339 timestamp").Wrap(err)
	}
	return &t, nil
}

// Update requires If-Match. A stale ETag yields 409 with the current
// representation (and its ETag) so the client can merge and retry.
func (h *Handler) Update(c *echo.Context) error {
	user, err := httpx.CurrentUser(c)
	if err != nil {
		return err
	}
	id, err := httpx.PathUUID(c, "id")
	if err != nil {
		return err
	}
	expected, err := httpx.RequireIfMatch(c)
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
	t, err := h.svc.Update(c.Request().Context(), user.UserID, id, in.toInput(), expected)
	if err != nil {
		return h.withCurrent(c, err, user.UserID, id)
	}
	httpx.SetETag(c, t.Version)
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
	expected, err := httpx.RequireIfMatch(c)
	if err != nil {
		return err
	}
	if err := h.svc.Delete(c.Request().Context(), user.UserID, id, expected); err != nil {
		return h.withCurrent(c, err, user.UserID, id)
	}
	return c.NoContent(http.StatusNoContent)
}

// withCurrent decorates a version conflict with the current representation.
func (h *Handler) withCurrent(c *echo.Context, err error, owner, id uuid.UUID) error {
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Kind != apperr.KindConflict {
		return err
	}
	cur, gerr := h.svc.Get(c.Request().Context(), owner, id)
	if gerr != nil {
		return err
	}
	httpx.SetETag(c, cur.Version)
	return ae.WithExtension("current", toDTO(cur))
}

type AuditEventResponse struct {
	ID        int64                  `json:"id"`
	ActorID   string                 `json:"actor_id"`
	Action    string                 `json:"action"`
	Changes   map[string]todo.Change `json:"changes"`
	CreatedAt time.Time              `json:"created_at"`
}

// Audit is admin-only (RequireRole on the route) and deliberately not owner-scoped.
func (h *Handler) Audit(c *echo.Context) error {
	id, err := httpx.PathUUID(c, "id")
	if err != nil {
		return err
	}
	events, err := h.svc.AdminAudit(c.Request().Context(), id)
	if err != nil {
		return err
	}
	out := make([]AuditEventResponse, len(events))
	for i, e := range events {
		out[i] = AuditEventResponse{ID: e.ID, ActorID: e.ActorID.String(), Action: e.Action, Changes: e.Changes, CreatedAt: e.CreatedAt}
	}
	return c.JSON(http.StatusOK, map[string]any{"items": out})
}
