package handler

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"echo-todo-2/internal/apperr"
	"echo-todo-2/internal/httpx"
	"echo-todo-2/internal/todo"
)

type AttachmentResponse struct {
	ID          string    `json:"id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`
	CreatedAt   time.Time `json:"created_at"`
}

func toAttachmentDTO(a *todo.Attachment) AttachmentResponse {
	const hexdigits = "0123456789abcdef"
	sum := make([]byte, len(a.SHA256)*2)
	for i, b := range a.SHA256 {
		sum[i*2], sum[i*2+1] = hexdigits[b>>4], hexdigits[b&0x0f]
	}
	return AttachmentResponse{ID: a.ID.String(), Filename: a.Filename, ContentType: a.ContentType, Size: a.Size, SHA256: string(sum), CreatedAt: a.CreatedAt}
}

// Upload streams the multipart "file" part straight into storage: no temp
// files, no full buffering, and never more than the limit is accepted.
func (h *Handler) Upload(c *echo.Context) error {
	user, err := httpx.CurrentUser(c)
	if err != nil {
		return err
	}
	todoID, err := httpx.PathUUID(c, "id")
	if err != nil {
		return err
	}
	mr, err := c.Request().MultipartReader()
	if err != nil {
		return apperr.New(apperr.KindUnsupportedMedia, "expected multipart/form-data").WithCode("multipart_required").Wrap(err)
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return apperr.Invalid("file", "a file part is required")
		}
		if err != nil {
			if echo.StatusCode(err) == http.StatusRequestEntityTooLarge {
				return err
			}
			return apperr.Invalid("body", "malformed multipart body").Wrap(err)
		}
		if part.FormName() != "file" {
			_ = part.Close()
			continue
		}
		a, err := h.att.Upload(c.Request().Context(), user.UserID, todoID, part.FileName(), part)
		_ = part.Close()
		if err != nil {
			return err
		}
		c.Response().Header().Set(echo.HeaderLocation, c.Request().URL.Path+"/"+a.ID.String())
		return c.JSON(http.StatusCreated, toAttachmentDTO(a))
	}
}

func (h *Handler) ListAttachments(c *echo.Context) error {
	user, err := httpx.CurrentUser(c)
	if err != nil {
		return err
	}
	todoID, err := httpx.PathUUID(c, "id")
	if err != nil {
		return err
	}
	items, err := h.att.List(c.Request().Context(), user.UserID, todoID)
	if err != nil {
		return err
	}
	out := make([]AttachmentResponse, len(items))
	for i := range items {
		out[i] = toAttachmentDTO(&items[i])
	}
	return c.JSON(http.StatusOK, map[string]any{"items": out})
}

// Download serves the blob with c.Attachment (or c.Inline for ?disposition=inline
// on types browsers can render safely). echo's Filesystem is the blob store.
func (h *Handler) Download(c *echo.Context) error {
	user, err := httpx.CurrentUser(c)
	if err != nil {
		return err
	}
	todoID, err := httpx.PathUUID(c, "id")
	if err != nil {
		return err
	}
	id, err := httpx.PathUUID(c, "aid")
	if err != nil {
		return err
	}
	a, err := h.att.Get(c.Request().Context(), user.UserID, todoID, id)
	if err != nil {
		return err
	}
	hdr := c.Response().Header()
	hdr.Set(echo.HeaderContentType, a.ContentType)
	hdr.Set(echo.HeaderContentSecurityPolicy, "default-src 'none'; sandbox")
	hdr.Set(echo.HeaderCacheControl, "private, max-age=0, must-revalidate")
	if c.QueryParam("disposition") == "inline" && todo.InlineSafe(a.ContentType) {
		return c.Inline(a.StorageKey, a.Filename)
	}
	return c.Attachment(a.StorageKey, a.Filename)
}

func (h *Handler) DeleteAttachment(c *echo.Context) error {
	user, err := httpx.CurrentUser(c)
	if err != nil {
		return err
	}
	todoID, err := httpx.PathUUID(c, "id")
	if err != nil {
		return err
	}
	id, err := httpx.PathUUID(c, "aid")
	if err != nil {
		return err
	}
	if err := h.att.Delete(c.Request().Context(), user.UserID, todoID, id); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// Export streams all of the caller's todos as CSV or NDJSON (GET /v1/todos/export/*,
// e.g. /v1/todos/export/todos.csv). Rows are produced by a goroutine into a pipe;
// the goroutine gets copies of the owner ID and request context, never the pooled
// *echo.Context.
func (h *Handler) Export(c *echo.Context) error {
	user, err := httpx.CurrentUser(c)
	if err != nil {
		return err
	}
	name := c.Param("*")
	var enc exportEncoder
	switch {
	case strings.HasSuffix(name, ".csv"):
		enc = newCSVEncoder()
	case strings.HasSuffix(name, ".ndjson"):
		enc = newNDJSONEncoder()
	default:
		return apperr.NotFound("export format not supported; use .csv or .ndjson")
	}

	ctx, owner := c.Request().Context(), user.UserID
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(exportAll(ctx, h.svc, owner, enc, pw))
	}()
	defer pr.Close()

	hdr := c.Response().Header()
	hdr.Set(echo.HeaderContentDisposition, `attachment; filename="`+todo.SanitizeFilename(name)+`"`)
	hdr.Set(echo.HeaderCacheControl, "no-store")
	return c.Stream(http.StatusOK, enc.contentType(), pr)
}
