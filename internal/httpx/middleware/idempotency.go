package middleware

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"echo-todo-2/internal/apperr"
	"echo-todo-2/internal/httpx"
)

const (
	IdempotencyKeyHeader      = "Idempotency-Key"
	IdempotencyReplayedHeader = "Idempotency-Replayed"
)

// StoredResponse is the replayable outcome of a completed request.
type StoredResponse struct {
	Status  int
	Headers map[string]string
	Body    []byte
}

// IdempotencyRecord is the state of a key.
type IdempotencyRecord struct {
	RequestHash []byte
	Completed   bool
	Response    *StoredResponse
}

// IdempotencyStore persists keys per user. Claim inserts the key atomically and
// reports whether this caller owns it; otherwise it returns the existing record.
type IdempotencyStore interface {
	Claim(ctx context.Context, userID uuid.UUID, key string, requestHash []byte, ttl time.Duration) (claimed bool, existing *IdempotencyRecord, err error)
	Complete(ctx context.Context, userID uuid.UUID, key string, resp StoredResponse) error
	Release(ctx context.Context, userID uuid.UUID, key string) error
}

// replayHeaders are the response headers worth replaying.
var replayHeaders = []string{echo.HeaderContentType, echo.HeaderLocation, "ETag"}

// Idempotency makes POST handlers safe to retry. With an Idempotency-Key header:
//   - first request: the key is claimed, the handler runs, a 2xx response is stored for ttl
//   - replay with the same body: the stored response is returned verbatim (Idempotency-Replayed: true)
//   - same key, different body: 422 idempotency_key_mismatch
//   - same key while the first is still running: 409 idempotency_in_progress
//
// Failed (non-2xx) attempts release the key so the client can retry.
// Must run after AuthRequired: keys are scoped per user.
func Idempotency(store IdempotencyStore, ttl time.Duration) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			key := c.Request().Header.Get(IdempotencyKeyHeader)
			if key == "" {
				return next(c)
			}
			if len(key) > 255 || !printable(key) {
				return apperr.Invalid(IdempotencyKeyHeader, "must be 1-255 printable ASCII characters")
			}
			user, err := httpx.CurrentUser(c)
			if err != nil {
				return err
			}
			body, err := httpx.PeekBody(c)
			if err != nil {
				if echo.StatusCode(err) != 0 {
					return err // e.g. 413 from BodyLimit
				}
				return apperr.Invalid("body", "unreadable request body").Wrap(err)
			}
			hash := requestHash(c.Request().Method, c.Path(), body)
			ctx := c.Request().Context()

			claimed, rec, err := store.Claim(ctx, user.UserID, key, hash, ttl)
			if err != nil {
				return apperr.Internal("idempotency.Claim", err)
			}
			if !claimed {
				return replay(c, rec, hash)
			}

			capture := &captureWriter{ResponseWriter: c.Response()}
			c.SetResponse(capture)
			herr := next(c)
			c.SetResponse(capture.ResponseWriter)

			// Store/release with a context that survives client disconnects.
			bg := context.WithoutCancel(ctx)
			if herr != nil || capture.status < 200 || capture.status >= 300 {
				if rerr := store.Release(bg, user.UserID, key); rerr != nil {
					c.Logger().Error("idempotency release failed", "err", rerr)
				}
				return herr
			}
			resp := StoredResponse{Status: capture.status, Headers: map[string]string{}, Body: capture.buf.Bytes()}
			for _, h := range replayHeaders {
				if v := capture.Header().Get(h); v != "" {
					resp.Headers[h] = v
				}
			}
			if serr := store.Complete(bg, user.UserID, key, resp); serr != nil {
				c.Logger().Error("idempotency complete failed", "err", serr)
			}
			return nil
		}
	}
}

func replay(c *echo.Context, rec *IdempotencyRecord, hash []byte) error {
	if !bytes.Equal(rec.RequestHash, hash) {
		return apperr.Invalid(IdempotencyKeyHeader, "key was already used with a different request").WithCode("idempotency_key_mismatch")
	}
	if !rec.Completed || rec.Response == nil {
		c.Response().Header().Set(echo.HeaderRetryAfter, "1")
		return apperr.Conflict("a request with this idempotency key is still in progress").WithCode("idempotency_in_progress")
	}
	h := c.Response().Header()
	for k, v := range rec.Response.Headers {
		h.Set(k, v)
	}
	h.Set(IdempotencyReplayedHeader, "true")
	ct := rec.Response.Headers[echo.HeaderContentType]
	if ct == "" {
		ct = echo.MIMEOctetStream
	}
	return c.Blob(rec.Response.Status, ct, rec.Response.Body)
}

func requestHash(method, route string, body []byte) []byte {
	h := sha256.New()
	h.Write([]byte(method))
	h.Write([]byte{0})
	h.Write([]byte(route))
	h.Write([]byte{0})
	h.Write(body)
	return h.Sum(nil)
}

func printable(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// captureWriter tees the response so it can be stored for replay.
type captureWriter struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
}

func (w *captureWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *captureWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.buf.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w *captureWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *captureWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("hijack not supported")
}

// Unwrap lets echo.UnwrapResponse find the underlying *echo.Response.
func (w *captureWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
