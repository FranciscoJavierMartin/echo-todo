// Package apptest builds the real application (real middleware chain, real
// Postgres via pgtest) for end-to-end HTTP tests.
package apptest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	"echo-todo-2/internal/app"
	"echo-todo-2/internal/auth"
	"echo-todo-2/internal/config"
	"echo-todo-2/internal/db"
	"echo-todo-2/internal/platform/logger"
	"echo-todo-2/internal/platform/postgres"
	"echo-todo-2/internal/platform/postgres/pgtest"
	"echo-todo-2/internal/platform/telemetry"
)

const Secret = "apptest-secret-apptest-secret-0123456789"

// Config returns a valid test configuration.
func Config() config.Config {
	return config.Config{
		Env:             "test",
		Addr:            ":0",
		DatabaseURL:     "set-by-pgtest",
		JWTSecret:       []byte(Secret),
		AccessTTL:       10 * time.Minute,
		RefreshTTL:      24 * time.Hour,
		MaxUploadBytes:  10 << 20,
		MaxBodyBytes:    1 << 20,
		RequestsPerSec:  1000,
		ShutdownTimeout: time.Second,
		// Cheap hashing keeps tests fast; production defaults are 64 MiB / t=3 / p=2.
		Argon2MemoryKiB: 1024,
		Argon2Time:      1,
		Argon2Threads:   1,
		AuthRPS:         1000,
		WriteRPS:        1000,
		RequestTimeout:  10 * time.Second,
		DBQueryTimeout:  3 * time.Second,
	}
}

type options struct {
	cfg func(*config.Config)
	tp  trace.TracerProvider
}

type Option func(*options)

func WithConfig(fn func(*config.Config)) Option { return func(o *options) { o.cfg = fn } }

func WithTracerProvider(tp trace.TracerProvider) Option { return func(o *options) { o.tp = tp } }

type Env struct {
	t      testing.TB
	App    *app.App
	Echo   *echo.Echo
	Pool   *pgxpool.Pool
	Config config.Config
	Tel    *telemetry.Telemetry
	Logs   *SyncBuffer
}

func New(t testing.TB, opts ...Option) *Env {
	t.Helper()
	var o options
	for _, fn := range opts {
		fn(&o)
	}
	cfg := Config()
	if o.cfg != nil {
		o.cfg(&cfg)
	}
	tel := telemetry.NewForTest(o.tp)
	var pgOpts []postgres.Option
	if o.tp != nil {
		pgOpts = append(pgOpts, tel.PGTracer())
	}
	pool, url := pgtest.NewWithURL(t, pgOpts...)
	cfg.DatabaseURL = url

	logs := &SyncBuffer{}
	log := logger.NewWithWriter("prod", logs)
	a, err := app.New(cfg, log, pool, tel)
	require.NoError(t, err)
	return &Env{t: t, App: a, Echo: a.Echo, Pool: pool, Config: cfg, Tel: tel, Logs: logs}
}

// User is a test user with a valid access token.
type User struct {
	ID    uuid.UUID
	Email string
	Role  string
	Token string
}

// CreateUser inserts a user directly and mints an access token for it.
func (e *Env) CreateUser(role string) User {
	e.t.Helper()
	email := strings.ToLower(uuid.NewString()[:8]) + "@example.com"
	u, err := db.New(e.Pool).CreateUser(context.Background(), db.CreateUserParams{
		Email: email, PasswordHash: "unused", DisplayName: "Test " + role, Role: role,
	})
	require.NoError(e.t, err)
	tok, _, err := e.App.Tokens.IssueAccess(u.ID, role)
	require.NoError(e.t, err)
	return User{ID: u.ID, Email: email, Role: role, Token: tok}
}

func (e *Env) CreateAdmin() User { return e.CreateUser(auth.RoleAdmin) }

// Request describes an HTTP call.
type Request struct {
	Method  string
	Path    string
	Body    any // string, []byte or a value marshalled as JSON
	Token   string
	Headers map[string]string
	Remote  string
}

func (e *Env) Do(r Request) *httptest.ResponseRecorder {
	e.t.Helper()
	var body io.Reader
	switch b := r.Body.(type) {
	case nil:
	case string:
		body = strings.NewReader(b)
	case []byte:
		body = bytes.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		require.NoError(e.t, err)
		body = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(r.Method, r.Path, body)
	if body != nil {
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	if r.Token != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+r.Token)
	}
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}
	if r.Remote != "" {
		req.RemoteAddr = r.Remote
	}
	rec := httptest.NewRecorder()
	e.Echo.ServeHTTP(rec, req)
	return rec
}

// JSON decodes a recorder body into T.
func JSON[T any](t testing.TB, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v), rec.Body.String())
	return v
}

// SyncBuffer is a goroutine-safe log sink.
type SyncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *SyncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *SyncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Lines returns decoded JSON log records.
func (b *SyncBuffer) Lines() []map[string]any {
	var out []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(b.String()), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}
