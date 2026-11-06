package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	apispec "echo-todo-2/api"
	"echo-todo-2/internal/api"
	"echo-todo-2/internal/apperr"
)

// API implements the oapi-codegen ServerInterface generated from api/openapi.yaml.
// Adding an operation to the spec without implementing it here fails the build.
//
// Requests reach these methods through api.ServerInterfaceWrapper, which binds and
// format-checks path/query/header parameters from the spec. The feature handlers
// still read the request themselves, so their richer validation (per-field 422s,
// 428 for a missing If-Match) applies unchanged.
type API struct {
	h *Handlers
}

var _ api.ServerInterface = (*API)(nil)

func (a *API) GetHealth(c *echo.Context) error      { return a.h.Health(c) }
func (a *API) GetReadiness(c *echo.Context) error   { return a.h.Ready(c) }
func (a *API) GetMetrics(c *echo.Context) error     { return a.h.metrics(c) }
func (a *API) GetOpenAPISpec(c *echo.Context) error { return openAPISpec(c) }
func (a *API) GetPprof(c *echo.Context, _ string) error {
	return pprofHandler()(c)
}

func (a *API) RegisterUser(c *echo.Context) error             { return a.h.deps.Auth.Register(c) }
func (a *API) Login(c *echo.Context, _ api.LoginParams) error { return a.h.deps.Auth.Login(c) }
func (a *API) RefreshTokens(c *echo.Context, _ api.RefreshTokensParams) error {
	return a.h.deps.Auth.Refresh(c)
}
func (a *API) Logout(c *echo.Context) error                 { return a.h.deps.Auth.Logout(c) }
func (a *API) LogoutAll(c *echo.Context) error              { return a.h.deps.Auth.LogoutAll(c) }
func (a *API) GetMe(c *echo.Context) error                  { return a.h.deps.Auth.Me(c) }
func (a *API) GetCsrfToken(c *echo.Context) error           { return csrfToken(c) }
func (a *API) CreateApiKey(c *echo.Context) error           { return a.h.deps.Auth.CreateAPIKey(c) }
func (a *API) ListApiKeys(c *echo.Context) error            { return a.h.deps.Auth.ListAPIKeys(c) }
func (a *API) RevokeApiKey(c *echo.Context, _ api.ID) error { return a.h.deps.Auth.RevokeAPIKey(c) }

func (a *API) ListTodos(c *echo.Context, _ api.ListTodosParams) error { return a.h.deps.Todos.List(c) }
func (a *API) CreateTodo(c *echo.Context, _ api.CreateTodoParams) error {
	return a.h.deps.Todos.Create(c)
}
func (a *API) GetTodo(c *echo.Context, _ api.ID, _ api.GetTodoParams) error {
	return a.h.deps.Todos.Get(c)
}
func (a *API) UpdateTodo(c *echo.Context, _ api.ID, _ api.UpdateTodoParams) error {
	return a.h.deps.Todos.Update(c)
}
func (a *API) DeleteTodo(c *echo.Context, _ api.ID, _ api.DeleteTodoParams) error {
	return a.h.deps.Todos.Delete(c)
}
func (a *API) GetTodoAudit(c *echo.Context, _ api.ID) error { return a.h.deps.Todos.Audit(c) }
func (a *API) ExportTodos(c *echo.Context, _ string) error  { return a.h.deps.Todos.Export(c) }
func (a *API) ListAttachments(c *echo.Context, _ api.ID) error {
	return a.h.deps.Todos.ListAttachments(c)
}
func (a *API) UploadAttachment(c *echo.Context, _ api.ID) error { return a.h.deps.Todos.Upload(c) }
func (a *API) DownloadAttachment(c *echo.Context, _ api.ID, _ openapi_types.UUID, _ api.DownloadAttachmentParams) error {
	return a.h.deps.Todos.Download(c)
}
func (a *API) DeleteAttachment(c *echo.Context, _ api.ID, _ openapi_types.UUID) error {
	return a.h.deps.Todos.DeleteAttachment(c)
}

// pathParams are reported as 404 when malformed: no resource can live there.
var pathParams = map[string]bool{"id": true, "aid": true}

// spec maps the generated wrapper's 400 binding errors onto our taxonomy:
// malformed path IDs → 404, bad query/header values → 422 for that field.
func spec(h echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		err := h(c)
		var he *echo.HTTPError
		if err == nil || !errors.As(err, &he) || he.Code != http.StatusBadRequest {
			return err
		}
		for _, prefix := range []string{"Invalid format for parameter ", "Query argument ", "Header parameter ", "Expected one value for "} {
			rest, ok := strings.CutPrefix(he.Message, prefix)
			if !ok {
				continue
			}
			name, _, _ := strings.Cut(rest, ":")
			name, _, _ = strings.Cut(name, " ")
			if pathParams[name] {
				return apperr.NotFound("resource not found").Wrap(err)
			}
			return apperr.Invalid(name, "has an invalid value").Wrap(err)
		}
		return err
	}
}

func openAPISpec(c *echo.Context) error {
	return c.Blob(http.StatusOK, "application/yaml", apispec.Spec)
}
