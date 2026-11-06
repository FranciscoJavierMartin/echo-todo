# Mastering Echo v5 by Building a Production-Grade TODO API

Echo v5 is the current major version (released 2026‑01‑18, current line v5.4.x). It is a breaking rewrite compared to v4: `Context` is a **struct passed as `*echo.Context`**, logging is **`log/slog`**, error handling uses **`NewHTTPError(code, message string)`** and a swapped handler signature `(c *echo.Context, err error)`, and server startup goes through **`StartConfig`**. Most v4 tutorials you find online will not compile. This guide is written against the real v5 API.

Since you already know Go, the goal is not "learn Go" — it is to internalize Echo's routing, middleware, binding, and error-handling model by building something with the feature set of a real service: auth, authorization, validation, pagination, concurrency control, idempotency, uploads, caching, observability, tests, and deployment.

Read the docs alongside this: [guide](https://echo.labstack.com/guide/quickstart/), [middleware](https://echo.labstack.com/middleware/), [API reference](https://pkg.go.dev/github.com/labstack/echo/v5), and the [v4→v5 API changes](https://github.com/labstack/echo/blob/v5/API_CHANGES_V5.md).

---

## How to use this guide

- 23 phases, each with a **Definition of Done (DoD)**. Do not start a phase until the previous DoD is green.
- Every phase is commit-sized. Commit at each DoD with a message like `feat(todos): optimistic concurrency via If-Match`.
- Build the API first, no UI. A JSON API exercises every framework primitive that matters.
- Two tracks: **Track A (framework mastery)** = phases 2–10; **Track B (product engineering)** = 11–22. Do them in order; B depends on A.
- Rule: never copy a v4 snippet without checking the signature. If it says `echo.Context`, add the `*`.

**Setup assumptions:** Go 1.25+, PostgreSQL 16, Docker, `make`, `curl`, `hey`/`k6`. Modules: `github.com/labstack/echo/v5`, `github.com/labstack/echo-jwt/v5`, `github.com/jackc/pgx/v5`, `github.com/sqlc-dev/sqlc`, `github.com/go-playground/validator/v10`, `golang.org/x/crypto`, `github.com/redis/go-redis/v9`, `github.com/testcontainers/testcontainers-go`, `github.com/stretchr/testify`.

---

## Phase 0 — Baseline sanity

Confirm the toolchain, then pin the framework:

```bash
go version                    # must be >= 1.25
mkdir -p ~/code/todo && cd ~/code/todo
go mod init github.com/you/todo
go get github.com/labstack/echo/v5@v5.4.4
go get github.com/labstack/echo/v5/middleware
```

Write the smallest possible server and run it:

```go
package main

import (
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

func main() {
	e := echo.New()
	e.Use(middleware.RequestLogger())
	e.Use(middleware.Recover())
	e.GET("/healthz", func(c *echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})
	if err := e.Start(":8080"); err != nil {
		slog.Error("start", "err", err)
	}
}
```

**DoD:** `curl -s localhost:8080/healthz` returns 200; logs appear in structured form; a deliberate `panic` in a handler yields a 500 instead of a crash.

---

## Phase 1 — Understand the framework before you use it

Spend an hour in the source. You are learning a library, and the fastest route is reading it: `echo.go`, `router.go`, `context.go`, `group.go`, `bind.go`, `httperror.go`, `server.go`.

Things to internalize:

1. **`HandlerFunc` is `func(c *Context) error`.** Returning an error is the whole control-flow contract; `nil` means "response already written."
2. **`Context` is pooled.** `ServeHTTP` calls `AcquireContext`/`Reset`/`ReleaseContext`. Never hold a `*echo.Context` past the handler — copy what you need into your own structs before spawning goroutines.
3. **`Router` is an interface.** `DefaultRouter` is a radix tree; `NewConcurrentRouter` makes add/remove safe at runtime; `RouterConfig` exposes `NotFoundHandler`, `MethodNotAllowedHandler`, `OptionsMethodHandler`, `AllowOverwritingRoute`, `AutoHandleHEAD`.
4. **Two middleware tiers.** `e.Pre()` runs before routing (so it also runs on 404s); `e.Use()` runs after a route matched. This distinction is a security tool: put body limits and timeouts in `Pre`, put auth in `Use`.
5. **`Context.Response()` returns `http.ResponseWriter`.** To reach the status-recording `*echo.Response`, use `echo.UnwrapResponse(rw)`.
6. **Errors are typed.** `echo.HTTPError{Code, Message}`, `echo.HTTPStatusCoder` (`StatusCode() int`), `echo.StatusCode(err)`, `echo.ProblemError`/`ProblemErrorer` for RFC 9457.

**DoD:** A markdown note in `docs/architecture.md` explaining the request lifecycle from `ServeHTTP` to your handler, and where each of your future middlewares sits in it.

---

## Phase 2 — Layout, config, and logger

Create the skeleton. The rule: `cmd/` is wiring only, `internal/` holds everything else, and nothing in `internal/` imports `cmd`.

```
cmd/api/main.go
internal/config/config.go
internal/platform/logger/logger.go
internal/platform/postgres/postgres.go
internal/httpx/errors.go
internal/httpx/respond.go
internal/httpx/middleware/
internal/auth/
internal/todo/
db/migrations/
db/queries/
```

Config: parse env with `os.LookupEnv`, validate eagerly, and fail loudly. Never `Getenv` inside handlers.

```go
type Config struct {
	Env             string        `env:"APP_ENV"`      // dev|test|prod
	Addr            string        `env:"HTTP_ADDR"`
	DatabaseURL     string        `env:"DATABASE_URL"`
	RedisURL        string        `env:"REDIS_URL"`
	JWTSecret       []byte        `env:"JWT_SECRET"`
	AccessTTL       time.Duration `env:"ACCESS_TTL"`
	RefreshTTL      time.Duration `env:"REFRESH_TTL"`
	AllowedOrigins  []string      `env:"CORS_ORIGINS"`
	TrustedProxies  []string      `env:"TRUSTED_PROXIES"`
	ExposeErrors    bool          `env:"EXPOSE_ERRORS"`
	MaxUploadBytes  int64         `env:"MAX_UPLOAD_BYTES"`
	RequestsPerSec  float64       `env:"RATE_LIMIT_RPS"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT"`
}
```

Logger: one `*slog.Logger`, JSON in prod, text in dev, and a request-scoped child logger carrying `request_id` and `user_id`.

```go
func New(cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	var h slog.Handler
	if cfg.Env == "prod" {
		h = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		opts.Level = slog.LevelDebug
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.New(h).With("service", "todo", "env", cfg.Env)
}
```

**DoD:** `go run ./cmd/api` starts with a config-driven logger and refuses to boot when `DATABASE_URL` or `JWT_SECRET` is missing.

---

## Phase 3 — Server bootstrap and graceful shutdown

`e.Start(":8080")` is deliberately minimal. Use `StartConfig`, which gives you `GracefulTimeout`, `BeforeServeFunc`, `OnShutdownError`, `ListenerAddrFunc`, and `StartTLS`.

```go
func newServer(cfg config.Config, log *slog.Logger, h *Handlers) *echo.Echo {
	e := echo.NewWithConfig(echo.Config{
		Logger:           log,
		HTTPErrorHandler: httpx.ErrorHandler(log, cfg.ExposeErrors),
		Router: echo.NewRouter(echo.RouterConfig{
			AllowOverwritingRoute: false,
			AutoHandleHEAD:        true,
			NotFoundHandler:       h.NotFound,
			MethodNotAllowedHandler: h.MethodNotAllowed,
		}),
	})
	registerRoutes(e, h)
	return e
}

func run() error {
	cfg, err := config.Load()
	if err != nil { return err }
	log := logger.New(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sc := echo.StartConfig{
		Address:         cfg.Addr,
		HideBanner:      cfg.Env == "prod",
		GracefulTimeout: cfg.ShutdownTimeout,
		OnShutdownError: func(err error) { log.Error("shutdown", "err", err) },
		BeforeServeFunc: func(s *http.Server) error {
			s.ReadHeaderTimeout = 5 * time.Second
			s.IdleTimeout = 120 * time.Second
			s.MaxHeaderBytes = 32 << 10
			return nil
		},
	}
	if err := sc.Start(ctx, e); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
```

**DoD:** `Ctrl‑C` drains in-flight requests within `SHUTDOWN_TIMEOUT`, logs the drain, and exits 0. A slow handler finishes; a hung one is cut off and reported via `OnShutdownError`.

---

## Phase 4 — Data layer

Schema with real constraints — the database is your first validator:

```sql
CREATE TYPE todo_status AS ENUM ('open', 'done', 'archived');

CREATE TABLE users (
	id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
	email         citext NOT NULL UNIQUE,
	password_hash text NOT NULL,
	display_name  text NOT NULL CHECK (length(display_name) BETWEEN 1 AND 80),
	role          text NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin')),
	created_at    timestamptz NOT NULL DEFAULT now(),
	updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE todos (
	id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
	owner_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	title       text NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
	notes       text NOT NULL DEFAULT '',
	status      todo_status NOT NULL DEFAULT 'open',
	due_at      timestamptz,
	priority    smallint NOT NULL DEFAULT 3 CHECK (priority BETWEEN 1 AND 5),
	version     integer NOT NULL DEFAULT 1,
	deleted_at  timestamptz,
	created_at  timestamptz NOT NULL DEFAULT now(),
	updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX todos_owner_status_due ON todos (owner_id, status, due_at) WHERE deleted_at IS NULL;
```

Use `sqlc` so SQL stays SQL and Go stays typed. Note `version` — that is your optimistic-lock column.

Repository contract — keep it narrow and context-first:

```go
type Repo interface {
	Create(ctx context.Context, t *Todo) error
	Get(ctx context.Context, ownerID, id uuid.UUID) (*Todo, error)
	List(ctx context.Context, f Filter) ([]Todo, int, error)
	Update(ctx context.Context, t *Todo, expectedVersion int) (*Todo, error)
	SoftDelete(ctx context.Context, ownerID, id uuid.UUID) error
}
```

The `Update` returns a sentinel when zero rows match the version:

```go
var ErrVersionConflict = errors.New("version conflict")
```

Transactions: `pgx.Tx` passed through a `WithTx(ctx, fn)` helper, never stored on a struct.

**DoD:** migrations run via `goose`/`atlas` in `make migrate`; repository tests pass against a containerized Postgres; `Update` returns `ErrVersionConflict` on a stale version.

---

## Phase 5 — Service layer and error taxonomy

Handlers must not contain business logic. Services own transactions and translate storage errors into a domain taxonomy:

```go
type Kind int

const (
	KindInternal Kind = iota
	KindInvalid
	KindNotFound
	KindConflict
	KindUnauthenticated
	KindForbidden
	KindRateLimited
)

type Error struct {
	Kind    Kind
	Op      string
	Message string   // safe to show a user
	Fields  map[string]string // per-field validation detail
	Err     error    // wrapped cause, never shown
}

func (e *Error) Error() string { return e.Op + ": " + e.Message }
func (e *Error) Unwrap() error { return e.Err }

func (e *Error) StatusCode() int {
	switch e.Kind {
	case KindInvalid: return http.StatusUnprocessableEntity
	case KindNotFound: return http.StatusNotFound
	case KindConflict: return http.StatusConflict
	case KindUnauthenticated: return http.StatusUnauthorized
	case KindForbidden: return http.StatusForbidden
	case KindRateLimited: return http.StatusTooManyRequests
	default: return http.StatusInternalServerError
	}
}

func (e *Error) ProblemError() *echo.ProblemError {
	return &echo.ProblemError{
		Title:  http.StatusText(e.StatusCode()),
		Status: e.StatusCode(),
		Detail: e.Message,
	}
}
```

That single `ProblemErrorer` implementation gives you RFC 9457 output for free and keeps handlers thin.

**DoD:** `service.Create` returns `*Error` with `KindInvalid` for an empty title; no `pgx` or `sql` import exists anywhere under `internal/todo/handler`.

---

## Phase 6 — Centralized error handling

One handler, one shape, no leaks.

```go
func ErrorHandler(log *slog.Logger, expose bool) echo.HTTPErrorHandler {
	problem := echo.ProblemDetailsHTTPErrorHandler(expose)
	return func(c *echo.Context, err error) {
		if c.Response().Header().Get(echo.HeaderContentType) != "" && isCommitted(c) {
			return // response already sent; only log
		}
		var appErr *apperr.Error
		if errors.As(err, &appErr) {
			lvl := slog.LevelWarn
			if appErr.Kind == apperr.KindInternal { lvl = slog.LevelError }
			log.LogAttrs(c.Request().Context(), lvl, "request failed",
				slog.String("op", appErr.Op),
				slog.Int("status", appErr.StatusCode()),
				slog.Any("err", appErr.Err),
			)
		}
		problem(c, err)
	}
}
```

```go
func isCommitted(c *echo.Context) bool {
	if r, err := echo.UnwrapResponse(c.Response()); err == nil { return r.Committed }
	return false
}
```

Rules: `echo.ErrNotFound` and friends are `HTTPStatusCoder`s — let them flow. Use `err.Wrap(cause)` to keep a cause on an HTTP error. `echo.DefaultHTTPErrorHandler(false)` is the JSON alternative if you dislike problem+json.

**DoD:** `curl /todos/00000000-...` yields `404` with `application/problem+json`; a forced DB outage yields a 500 whose body has no SQL text while logs do.

---

## Phase 7 — Binding and validation

Bind explicitly, never trust `c.Bind` blindly. Prefer per-source functions:

- `echo.BindBody(c, &in)` — body only
- `echo.BindQueryParams(c, &in)`
- `echo.BindPathValues(c, &in)`
- `echo.BindHeaders(c, &in)`

Register a validator and call `c.ValidateCtx` (context-aware, added v5.4.1):

```go
type Validator struct{ v *validator.Validate }

func (val *Validator) Validate(i any) error {
	if err := val.v.Struct(i); err != nil {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, "validation failed").Wrap(err)
	}
	return nil
}
func (val *Validator) ValidateCtx(ctx context.Context, i any) error { return val.Validate(i) }
```

```go
e.Validator = &Validator{v: newValidator()}
```

Handler shape — bind, validate, map, delegate, respond:

```go
func (h *TodoHandler) Create(c *echo.Context) error {
	var in CreateTodoInput
	if err := echo.BindBody(c, &in); err != nil {
		return apperr.Invalid("body", "malformed JSON").Wrap(err)
	}
	if err := c.ValidateCtx(&in); err != nil { return err }

	user, err := h.currentUser(c)
	if err != nil { return err }

	t, err := h.svc.Create(c.Request().Context(), user.ID, in)
	if err != nil { return err }
	c.Response().Header().Set("Location", "/v1/todos/"+t.ID.String())
	return c.JSON(http.StatusCreated, toDTO(t))
}
```

For query strings prefer typed generics over manual `strconv`:

```go
limit, err := echo.QueryParamOr[int](c, "limit", 20)
```

Supported types include ints, floats, bool, string, `time.Time`, `time.Duration`, `TextUnmarshaler`, `json.Unmarshaler`. Note the documented semantics: missing key → `ErrNonExistentKey`; present-but-empty → zero value, no error; invalid → `BindingError`.

**DoD:** 422 with per-field messages for a blank title and priority 9; `?limit=abc` returns 422, not a 500; a 10 MB body is rejected with 413 once `BodyLimit` is in place.

---

## Phase 8 — Routing mastery

Practice every routing primitive deliberately:

```go
v1 := e.Group("/v1")
todos := v1.Group("/todos", h.AuthRequired)
todos.POST("", h.Create)
todos.GET("", h.List)
todos.GET("/:id", h.Get)
todos.PATCH("/:id", h.Update)
todos.DELETE("/:id", h.Delete)
todos.POST("/:id/attachments", h.UploadAttachment, middleware.BodyLimit(cfg.MaxUploadBytes))
todos.GET("/export/*", h.Export)          // wildcard
todos.GET("/:id/audit", h.Audit, h.RequireRole("admin"))
```

Named routes and reverse URL building:

```go
info := e.GET("/v1/todos/:id", h.Get)
info.Name = "todos.get"
// later
url, _ := e.Router().Routes().Reverse("todos.get", id)
```

Inspect routes for docs and tests:

```go
byMethod, _ := e.Router().Routes().FilterByMethod(http.MethodPost)
```

Custom 404 that still renders JSON, plus group-aware fallbacks:

```go
e.RouteNotFound("/*", h.NotFound)
```

Gotchas: group prefixes concatenate without inserting a slash (`/v1` + `todos` = `/v1todos`); trailing slashes are distinct routes — add `e.Pre(middleware.RemoveTrailingSlash())`; groups auto-register catch-all 404 routes when they have middleware (disable with `echo.Config{NoGroupAutoRegister404Routes: true}`); `Any` beats nothing but loses to a method-specific route; `AutoHandleHEAD` executes GET handlers including side effects.

**DoD:** A test asserts every route is reachable, every unknown path returns JSON 404, wrong method returns 405 with an `Allow` header, and no route has a stray double slash.

---

## Phase 9 — Middleware: write your own

`MiddlewareFunc` is `func(next HandlerFunc) HandlerFunc`. Write these from scratch to learn the model — request ID propagation, structured access log, auth, RBAC, idempotency, panic recovery with request context. Then use the built-ins.

Template with correct ordering and error propagation:

```go
func RequestContext(log *slog.Logger) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			rid := c.Request().Header.Get(echo.HeaderXRequestID)
			if rid == "" { rid = uuid.NewString() }
			c.Response().Header().Set(echo.HeaderXRequestID, rid)

			ctx := context.WithValue(c.Request().Context(), ctxKeyRequestID, rid)
			c.SetRequest(c.Request().WithContext(ctx))
			c.SetLogger(log.With("request_id", rid, "route", c.Path()))

			return next(c)
		}
	}
}
```

Built-in middleware you will actually use, with v5 signatures:

| Need | Call |
|---|---|
| Access log | `middleware.RequestLogger()` / `...WithConfig` |
| Panic recovery | `middleware.Recover()` |
| Request ID | `middleware.RequestID()` |
| Body cap | `middleware.BodyLimit(1 * middleware.MB)` |
| Timeouts | `middleware.ContextTimeout(5 * time.Second)` |
| Security headers | `middleware.SecureWithConfig(...)` |
| CORS | `middleware.CORS("https://app.example.com")` or `CORSWithConfig` |
| CSRF | `middleware.CSRFWithConfig(...)` |
| Rate limit | `middleware.RateLimiterWithConfig(...)` |
| Gzip / decompress | `middleware.Gzip()`, `middleware.Decompress()` |
| Static files | `middleware.StaticWithConfig(...)` |
| Method override | `middleware.MethodOverride()` (register in `Pre`) |
| Trailing slash | `middleware.RemoveTrailingSlash()` (in `Pre`) |

Ordering (outermost first): recover → request ID → logger → secure → CORS → body limit → rate limit → timeout → auth → RBAC → handler.

Two traps: `middleware.Static` registered with `Use` runs **before** route/group middleware, so route guards do not protect the files it serves; and `Skipper` (`func(c *echo.Context) bool`) is how you exempt `/healthz` from rate limiting and logging.

**DoD:** Custom `AuthRequired` puts a typed user in the context via `c.Set`, rejects with 401, and the access log includes `request_id`, `user_id`, status, latency, and route template.

---

## Phase 10 — Observability

- **Logs:** `slog` only, JSON in prod, request-scoped child logger via `c.SetLogger`. Never log bodies by default; use `middleware.BodyDumpWithConfig` for debugging (it caps at 5 MB).
- **Metrics:** `labstack/echo-prometheus` — counter on `route`, `method`, `status`; histogram on duration. Keep labels low-cardinality (route template, never raw paths).
- **Tracing:** `labstack/echo-otel`; propagate `traceparent`; span per request, child spans around DB calls.
- **pprof:** mount under a guarded group: `debug := e.Group("/debug", h.RequireRole("admin")); debug.GET("/pprof/*", echo.WrapHandler(http.DefaultServeMux))`.
- **Real IP behind a proxy:** set `e.IPExtractor = echo.ExtractIPFromXFFHeader(echo.TrustIPRange(cidr))` — v5 returns `RemoteAddr` unless configured. Set `e.SchemeExtractor` similarly for correct `https` detection.

**DoD:** `/metrics` exposes request counters; a trace shows handler + query spans; rate limiting keys off the real client IP behind a local proxy.

---

## Phase 11 — Authentication

- Passwords: **argon2id** (`golang.org/x/crypto/argon2`), 64 MB / t=3 / p=2, per-user salt, PHC-encoded string, `subtle.ConstantTimeCompare` for legacy paths.
- Registration: normalize email, unique-violation → `KindConflict`.
- Login: generic "invalid credentials" for both unknown user and wrong password; equalize timing by hashing a dummy password on miss.
- **Access token:** JWT, HS256, 10 min, claims `sub`, `role`, `jti`, `exp`.
- **Refresh token:** opaque 32-byte random, stored **hashed** in `refresh_tokens` with `family_id`, `expires_at`, `revoked_at`, `replaced_by`; rotate on every use; on reuse of a rotated token, revoke the whole family.
- Transport: `Authorization: Bearer` for API clients; `HttpOnly; Secure; SameSite=Lax` cookies for browsers. If cookies, CSRF middleware is mandatory.

Middleware with `echojwt/v5`:

```go
e.Use(echojwt.WithConfig(echojwt.Config{
	SigningKey:    cfg.JWTSecret,
	SigningMethod: echojwt.AlgorithmHS256,
	TokenLookup:   "header:Authorization:Bearer ,cookie:access_token",
	ContextKey:    "token",
	ErrorHandler: func(c *echo.Context, err error) error {
		return apperr.Unauthenticated("invalid or expired token").Wrap(err)
	},
}))
```

Read the claims with generics, not assertions:

```go
token, err := echo.ContextGet[*jwt.Token](c, "token")
claims, ok := token.Claims.(*Claims)   // set NewClaimsFunc to your Claims type
```

Also expose API keys for machine clients via `middleware.KeyAuthWithConfig` with a hashed-key lookup and constant-time compare. Store the refresh cookie with `Path=/v1/auth`, `MaxAge`, and `SameSite`.

**DoD:** Login issues both tokens; refresh rotates and old token is rejected; reuse revokes the family; logout revokes; expired access token → 401 with a machine-readable code.

---

## Phase 12 — Authorization

Two layers, both server-side:

1. **Role gate:** `RequireRole("admin")` middleware for coarse checks.
2. **Ownership:** every `Get/Update/Delete` filters by `owner_id` in SQL — never fetch then compare in Go. Return 404 (not 403) for another user's todo so IDs stay unenumerable.

```go
func (h *Handlers) RequireRole(role string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			u, err := h.currentUser(c)
			if err != nil { return err }
			if u.Role != role { return apperr.Forbidden("requires role " + role) }
			return next(c)
		}
	}
}
```

**DoD:** A test matrix proves user B gets 404 on user A's todo for GET/PATCH/DELETE, and 403 on admin-only routes.

---

## Phase 13 — Hardening

- `middleware.SecureWithConfig` with CSP, `nosniff`, `SAMEORIGIN`, `Referrer-Policy`, HSTS once TLS is real.
- `middleware.CORSWithConfig` — explicit origins, `AllowCredentials: true`, never `*` with credentials.
- `middleware.CSRFWithConfig` — `TokenLookup: "header:X-CSRF-Token"`, `CookieSameSite: Lax`, `TrustedOrigins`.
- `middleware.BodyLimit(cfg.MaxUploadBytes)` and `middleware.ContextTimeout(10 * time.Second)` in `Pre`.
- Rate limits: tight on `/v1/auth/*` (5/s burst 10), generous on reads.
- `e.IPExtractor` + `e.SchemeExtractor` for proxy deployments.
- DB timeouts per query: `ctx, cancel := context.WithTimeout(ctx, 3*time.Second)`.
- Enforce max lengths in DB and validator; cap pagination at 100.

**DoD:** A security test file asserts headers on every response, CSRF rejection without the token, 429 after the burst, and 413 over the body cap.

---

## Phase 14 — List features

`GET /v1/todos?status=open&priority=1,2&due_before=...&q=...&sort=-priority,due_at&limit=20&cursor=...`

- Bind with `echo.QueryParamOr`, `echo.QueryParams[string]`, `echo.ParseValues[int]`.
- Validate sort against an allowlist mapped to SQL fragments; never interpolate raw column names.
- Keyset pagination on `(priority, due_at, id)`; return `next_cursor`; also return `total` only when `?count=true`.
- Free-text search with `websearch_to_tsquery` on a generated `tsvector` column and a GIN index.

**DoD:** Pagination is stable across inserts; invalid sort → 422; 500-item dataset pages correctly with no duplicates or gaps.

---

## Phase 15 — Concurrency and idempotency

**Optimistic concurrency:** expose `version` in the DTO and an `ETag`. Require `If-Match` on PATCH/DELETE.

```go
etag := fmt.Sprintf(`"%d"`, t.Version)
if match := c.Request().Header.Get("If-Match"); match != "" && match != etag {
	return apperr.Conflict("todo was modified").Wrap(repo.ErrVersionConflict)
}
c.Response().Header().Set("ETag", etag)
```

Map `ErrVersionConflict` → 409 with the current representation so clients can merge.

**Idempotency:** `POST` accepts `Idempotency-Key`. Middleware stores `(key, user_id, request_hash, status, body, expires_at)`; replay returns the stored response; a mismatched body for the same key returns 422. 24h TTL.

**DoD:** Two concurrent PATCHes with the same ETag — one 200, one 409; a replayed POST returns the identical body and no duplicate row.

---

## Phase 16 — Files, streaming, static assets

- Uploads: `c.FormFile("file")` → size/extension/`http.DetectContentType` checks → stream to storage → persist metadata. Never trust `FileHeader.Header`.
- Downloads: `c.Attachment(file, name)` for downloads, `c.Inline` for previews, `c.Stream(code, ctype, reader)` for generated content.
- Serve SPAs with `e.StaticFS("/", echo.MustSubFS(uiFS, "dist"))` plus `HTML5` fallback for deep links.
- Embed the built UI: `//go:embed all:dist`.

**DoD:** Upload of a 20 MB file is rejected; a `.png`-renamed `.exe` is caught by content sniffing; SPA deep links resolve; static assets are served with cache headers.

---

## Phase 17 — Cache, jobs, outbox

- Cache reads in Redis with keys `todo:{id}:v{version}` and explicit invalidation on write; short TTLs, never cached auth decisions.
- `GET` responses get `ETag` + `If-None-Match` → 304.
- Emails/reminders go through an `outbox` table written in the same transaction as the todo, drained by a worker — no lost notifications.
- Worker started in `run()` on a `context` cancelled by the same signal context as the server.

**DoD:** Cached GET returns 304 on match; deleting a todo purges its key; a forced worker crash loses no outbox row.

---

## Phase 18 — Testing

Pyramid, with the framework in mind:

- **Unit:** services with hand-written fake repos; validators.
- **Handler tests:** `httptest` + `e.ServeHTTP(rec, req)`. Build the server with `newServer` so middleware is real.
- **Fixtures:** `echotest.LoadBytes(t, "testdata/create_todo.json")` and `echotest.ContextConfig` for direct context construction; `TrimNewlineEnd` for golden comparisons.
- **Integration:** `testcontainers-go` Postgres; migrate, run, tear down. One test per migration is cheap insurance.
- **Fuzz:** `FuzzBindCreateTodo` on the JSON body — no panics, always 4xx or 201.
- **Auth contract test:** one test asserting `c.Get("token").(*jwt.Token)` still casts — echo-jwt warns this breaks on library majors.

```go
func TestCreateTodo_Validation(t *testing.T) {
	e := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/todos", strings.NewReader(`{"title":""}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+testToken(t, userID))
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Contains(t, rec.Body.String(), "title")
}
```

**DoD:** ≥80% coverage on `internal/todo`, ≥90% on `internal/auth`; fuzz runs 30s clean; CI is green.

---

## Phase 19 — Performance

Benchmark the real chain: `go test -bench . -benchmem -cpuprofile cpu.out ./internal/todo`. Targets: zero allocations in the router hot path, no `*echo.Context` escaping to the heap, no `fmt.Sprintf` in handlers.

- Compare `e.Any` vs explicit methods, and `AutoHandleHEAD` on/off, on a benchmark.
- Try `echo.NewConcurrentRouter` and measure the lock cost.
- Load test with `hey -z 30s -c 50` and `k6` for a scenario with auth + pagination.
- Profile under load with pprof; find the slow query with `EXPLAIN (ANALYZE, BUFFERS)`.
- Swap `JSONSerializer` for a faster encoder only if profiling says JSON matters.

**DoD:** p95 < 50 ms at 200 rps on the list endpoint; a written note of the three hottest functions and what you changed.

---

## Phase 20 — API documentation

Write `api/openapi.yaml` first, then generate server stubs with `oapi-codegen` (it emits Echo v5 handlers) or keep it spec-only and validate responses in tests with `kin-openapi`. Alternative: `swaggo/echo-swagger` for annotation-driven Swagger 2.0. Serve `/docs` behind `RequireRole("admin")` in prod.

**DoD:** Spec covers every route with schemas and error responses; a test fails when a handler's status codes drift from the spec.

---

## Phase 21 — Container and CI

Multi-stage Dockerfile: `golang:1.27` builder → `distroless/static` runtime, non-root, `-trimpath -ldflags="-s -w"`, `CGO_ENABLED=0`. Compose with Postgres + Redis + a one-shot `migrate` service that the API depends on.

CI stages: `gofmt -l`, `go vet`, `staticcheck`, `govulncheck`, `go test -race ./...`, integration job with services, build image, scan with `trivy`. Postgres and Redis behind health checks.

**DoD:** `docker compose up` yields a working API; CI blocks a PR that fails `-race` or `govulncheck`.

---

## Phase 22 — Production readiness checklist

- [ ] Graceful shutdown verified under load
- [ ] All secrets from env/secret manager, none in code
- [ ] Timeouts on DB, HTTP client, and handlers
- [ ] Rate limits on auth and writes
- [ ] Security headers, CORS, CSRF all asserted by tests
- [ ] Structured logs with request IDs, no PII
- [ ] Metrics, traces, alerts on 5xx rate and p95
- [ ] Backups + tested restore; migrations reversible
- [ ] pprof guarded, `/healthz` and `/readyz` separate
- [ ] Dependency and container scanning in CI
- [ ] Runbook: rollback, rotate JWT secret, revoke all sessions

---

## Phase 23 — Mastery exercises

Do these only after the checklist is green:

1. Replace `DefaultRouter` with a custom `Router` implementation that logs match decisions.
2. Add a second `*echo.Echo` per tenant and wire `echo.NewVirtualHostHandler`.
3. Serve a whole separate internal admin API on a distinct `*echo.Echo` with its own middleware stack.
4. Implement a streaming `text/event-stream` endpoint with `Response.Flush` and a 30 s heartbeat.
5. Add WebSocket live updates for todo changes across a user's devices.
6. Add a `ProblemErrorer`-based error type and assert the exact RFC 9457 body.
7. Implement `HEAD` handling manually and compare it to `AutoHandleHEAD`.
8. Build a `Renderer` with `html/template` and render a server-side todo page with CSRF tokens.
9. Add `middleware.Proxy` in front of a legacy service and verify `X-Forwarded-Proto` handling.
10. Convert the JSON API to `jsoniter` and measure.

**Reading:** the Echo [guide](https://echo.labstack.com/guide/quickstart/), [cookbook](https://echo.labstack.com/cookbook/hello-world/), the [middleware docs](https://echo.labstack.com/middleware/), `API_CHANGES_V5.md`, the [ROADMAP](https://github.com/labstack/echo/blob/master/ROADMAP.md), and the source for `router.go` and `context.go`. `mikestefanello/pagoda` is a real-world Echo app worth reading.

---

## v5-specific pitfalls

1. `echo.Context` in a signature is a compile error — always `*echo.Context`.
2. `NewHTTPError` takes a single `string`. No variadic details; use `.Wrap(err)`.
3. `HTTPErrorHandler` is `(c *echo.Context, err error)` — reversed from v4.
4. `c.Response()` is an `http.ResponseWriter`; `UnwrapResponse` for the status fields.
5. No `StartTLS`/`Shutdown` on `*Echo`; use `StartConfig`.
6. No `e.Reverse`/`URI`/`URL`; use `Router().Routes().Reverse(name, ...)`.
7. `middleware.Logger()` is gone; it is `RequestLogger()`. `Timeout` is `ContextTimeout`.
8. `RealIP()` returns `RemoteAddr` until you set `IPExtractor`.
9. `BodyLimit` takes bytes, not a string like `"2M"`.
10. `FormParam*` generics are now `FormValue*`.
11. Group prefixes do not auto-insert `/`.
12. `middleware.Static` with `Use` bypasses route guards.
13. Do not mutate `Echo` fields or add routes after `Start` — use `NewConcurrentRouter` if you must.
14. `echo.Map` is removed; use `map[string]any`.

Work the phases in order, hit every DoD, and by phase 23 you will know Echo's router, middleware chain, binder, validator hook, error pipeline, and server lifecycle well enough that v5's breaking changes stop being a hurdle and become design decisions you understand.