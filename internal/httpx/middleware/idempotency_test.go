package middleware_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/apperr"
	"echo-todo-2/internal/auth"
	"echo-todo-2/internal/httpx"
	mw "echo-todo-2/internal/httpx/middleware"
	"echo-todo-2/internal/platform/logger"
)

type memStore struct {
	mu       sync.Mutex
	recs     map[string]*mw.IdempotencyRecord
	claimErr error
}

func newMemStore() *memStore { return &memStore{recs: map[string]*mw.IdempotencyRecord{}} }

func (s *memStore) Claim(_ context.Context, u uuid.UUID, key string, hash []byte, _ time.Duration) (bool, *mw.IdempotencyRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claimErr != nil {
		return false, nil, s.claimErr
	}
	k := u.String() + key
	if r, ok := s.recs[k]; ok {
		return false, r, nil
	}
	s.recs[k] = &mw.IdempotencyRecord{RequestHash: hash}
	return true, nil, nil
}

func (s *memStore) Complete(_ context.Context, u uuid.UUID, key string, resp mw.StoredResponse) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.recs[u.String()+key]
	r.Completed, r.Response = true, &resp
	return nil
}

func (s *memStore) Release(_ context.Context, u uuid.UUID, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.recs, u.String()+key)
	return nil
}

func idemEcho(store mw.IdempotencyStore, h echo.HandlerFunc) *echo.Echo {
	var buf bytes.Buffer
	log := logger.NewWithWriter("test", &buf)
	e := echo.NewWithConfig(echo.Config{Logger: log, HTTPErrorHandler: httpx.ErrorHandler(log, false)})
	uid := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	asUser := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			httpx.SetPrincipal(c, &auth.Principal{UserID: uid, Role: auth.RoleUser})
			return next(c)
		}
	}
	e.POST("/things", h, middleware.BodyLimit(64), asUser, mw.Idempotency(store, time.Hour))
	return e
}

func post(e *echo.Echo, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/things", strings.NewReader(body))
	if key != "" {
		req.Header.Set(mw.IdempotencyKeyHeader, key)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestIdempotencyMiddleware(t *testing.T) {
	store := newMemStore()
	calls := 0
	e := idemEcho(store, func(c *echo.Context) error {
		calls++
		c.Response().Header().Set("Location", "/things/1")
		return c.JSON(http.StatusCreated, map[string]int{"n": calls})
	})

	first := post(e, "k", `{"a":1}`)
	require.Equal(t, http.StatusCreated, first.Code)
	again := post(e, "k", `{"a":1}`)
	require.Equal(t, first.Body.String(), again.Body.String())
	require.Equal(t, "/things/1", again.Header().Get("Location"))
	require.Equal(t, "true", again.Header().Get(mw.IdempotencyReplayedHeader))
	require.Equal(t, 1, calls)

	require.Equal(t, http.StatusUnprocessableEntity, post(e, "k", `{"a":2}`).Code)
	require.Equal(t, http.StatusCreated, post(e, "", `{"a":1}`).Code, "no key → no idempotency")
	require.Equal(t, 2, calls)
	require.Equal(t, http.StatusUnprocessableEntity, post(e, strings.Repeat("x", 256), `{}`).Code)
	require.Equal(t, http.StatusUnprocessableEntity, post(e, "tab\tkey", `{}`).Code)
	require.Equal(t, http.StatusRequestEntityTooLarge, post(e, "big", strings.Repeat("x", 100)).Code)
}

func TestIdempotencyInProgressAndFailures(t *testing.T) {
	store := newMemStore()
	entered, release := make(chan struct{}), make(chan struct{})
	e := idemEcho(store, func(c *echo.Context) error {
		switch c.QueryParam("x") {
		case "fail":
			return apperr.Invalid("x", "bad")
		case "block":
			close(entered)
			<-release
		}
		return c.String(http.StatusAccepted, "ok")
	})

	// A second request arrives while the first one still holds the key.
	done := make(chan int)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/things?x=block", strings.NewReader(`{}`))
		req.Header.Set(mw.IdempotencyKeyHeader, "busy")
		r := httptest.NewRecorder()
		e.ServeHTTP(r, req)
		done <- r.Code
	}()
	<-entered
	req0 := httptest.NewRequest(http.MethodPost, "/things?x=block", strings.NewReader(`{}`))
	req0.Header.Set(mw.IdempotencyKeyHeader, "busy")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req0)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, "1", rec.Header().Get("Retry-After"))
	close(release)
	require.Equal(t, http.StatusAccepted, <-done)

	// Handler errors release the key.
	req := httptest.NewRequest(http.MethodPost, "/things?x=fail", strings.NewReader(`{}`))
	req.Header.Set(mw.IdempotencyKeyHeader, "retry-me")
	r := httptest.NewRecorder()
	e.ServeHTTP(r, req)
	require.Equal(t, http.StatusUnprocessableEntity, r.Code)
	require.Equal(t, http.StatusAccepted, post(e, "retry-me", `{}`).Code)

	store.claimErr = errors.New("db down")
	require.Equal(t, http.StatusInternalServerError, post(e, "new", `{}`).Code)
}
