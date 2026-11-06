# Architecture: the Echo v5 request lifecycle

This note follows one HTTP request from `net/http` to a TODO handler and back. It also shows
where each middleware in this service sits. It is written against `github.com/labstack/echo/v5@v5.4.4`.

## 1. From `net/http` to Echo

```
net.Listener ─▶ http.Server (configured in StartConfig.BeforeServeFunc)
                 │  ReadHeaderTimeout / IdleTimeout / MaxHeaderBytes
                 ▼
            (*echo.Echo).ServeHTTP(w, r)
```

`StartConfig.Start(ctx, e)` builds the `http.Server`, calls `BeforeServeFunc` so we can harden it, and
runs `Serve`. When `ctx` is cancelled (SIGINT/SIGTERM through `signal.NotifyContext`), a goroutine
calls `server.Shutdown` with a deadline of `GracefulTimeout`. If in-flight requests miss that
deadline, `OnShutdownError` is called.

## 2. Inside `ServeHTTP`

```go
c := e.contextPool.Get().(*Context)   // pooled *echo.Context
defer e.contextPool.Put(c)
c.Reset(r, w)                         // wraps w in *echo.Response
if e.premiddleware == nil {
    c.handler = e.router.Route(c)     // routing
    err = e.chain(c)                  // Use() middlewares + handler
} else {
    err = e.preChain(c)               // Pre() middlewares → routing → chain
}
if err != nil { e.HTTPErrorHandler(c, err) }
```

Consequences:

* **The context is pooled.** It is reused for the next request as soon as `ServeHTTP` returns. Never keep
  `*echo.Context` after the handler returns. Before starting goroutines, copy the values you need
  (IDs, the request `context.Context`).
* **Chains are prebuilt.** `Pre`/`Use` rebuild `e.preChain`/`e.chain` once. Nothing is allocated
  per request to wrap middleware. So do not mutate middleware after `Start`.
* **One error exit.** Each layer returns `error`. The *first* non-nil error that bubbles up to
  `ServeHTTP` reaches the single `HTTPErrorHandler`. A handler that writes a response returns `nil`.

## 3. Routing

`Router` is an interface. `DefaultRouter` is a radix tree with priority static > param (`:id`) >
wildcard (`*`). `Route(c)` fills `c.PathValues()` and `c.RouteInfo()` (route template such as
`/v1/todos/:id`). It returns the matched handler, which already has its **group and route
middleware** baked in. When nothing matches, it returns `RouterConfig.NotFoundHandler`. When the path
matches but the method does not, it returns `MethodNotAllowedHandler` (the `Allow` header is
prepared in the context).

Groups with middleware register catch-all `RouteNotFound` routes (`/prefix` and `/prefix/*`). That way
their middleware (CORS, auth) also runs for unknown paths under the prefix.

## 4. Middleware tiers

| Tier | Registered with | Runs | Used for |
|---|---|---|---|
| Pre | `e.Pre(...)` | before routing, also for 404/405 | recovery, request ID, access log, trailing slash, body limit, request timeout, security headers, CORS, metrics, tracing |
| Global | `e.Use(...)` | after a route matched (including 404 handler) | rate limiting (needs route template for skipping) |
| Group | `g := e.Group(p, mw...)` / `g.Use` | only for routes of the group | auth, CSRF, auth-tier rate limit |
| Route | `g.GET(path, h, mw...)` | only for that route | `RequireRole`, upload body limit, idempotency |

## 5. This service's chain (outermost first)

```
http.Server
└─ ServeHTTP
   ├─ Pre: Recover                    (custom, logs panic with request_id, returns 500 problem)
   ├─ Pre: RequestContext             (X-Request-Id, ctx values, request-scoped slog logger)
   ├─ Pre: AccessLog                  (RequestLoggerWithConfig: status, latency, route, request_id, user_id)
   ├─ Pre: Tracing (echo-otel)        (span per request; traceparent propagation)
   ├─ Pre: Metrics (echo-prometheus)  (route template label, never the raw path)
   ├─ Pre: RemoveTrailingSlash
   ├─ Pre: Secure headers / CORS
   ├─ Pre: BodyLimit (global cap)     (413 before reading the body)
   ├─ Pre: ContextTimeout             (request context deadline)
   ├─ Router.Route(c)  → route template known from here on
   ├─ Use: RateLimiter (global, keyed on c.RealIP(), /healthz skipped)
   ├─ Group /v1/auth: RateLimiter (tight), CSRF (cookie clients)
   ├─ Group /v1/todos: AuthRequired (echo-jwt → typed principal in context, logger += user_id)
   │   ├─ Route: RequireRole("admin") (audit)
   │   ├─ Route: BodyLimit(MaxUploadBytes) (attachments)
   │   └─ Route: Idempotency (POST)
   └─ Handler: bind → validate → service → respond
        └─ error? → HTTPErrorHandler → RFC 9457 problem+json, logged once
```

The access logger sits *outside* auth, but it still logs `user_id`. Auth stores the principal in
the request context, and the logger reads it after `next` returns.

## 6. Error pipeline

```
handler / middleware returns err
  └─ httpx.ErrorHandler(c, err)
       ├─ response already committed? → log only
       ├─ *apperr.Error (ProblemErrorer + HTTPStatusCoder) → problem+json with code/errors extensions
       ├─ echo.HTTPStatusCoder (ErrNotFound, ErrMethodNotAllowed, 413, 429…) → problem+json
       └─ anything else → 500 problem+json, cause logged, never exposed (unless EXPOSE_ERRORS)
```

Services return `*apperr.Error`, which carries a `Kind` (`Invalid`, `NotFound`, `Conflict`, …). The
error type maps itself to a status code. Handlers stay a thin layer: bind, validate, call the
service, respond.

## 7. Layers

```
cmd/api          wiring only (config → logger → db → services → server → worker)
internal/server  newServer: Echo config, middleware chain, route table
internal/httpx   error handler, validator, response helpers, middleware
internal/todo    domain model, Repo interface, service (no pgx), handler, pgrepo
internal/auth    password hashing, tokens, refresh families, service, handler, pgrepo
internal/platform  logger, postgres (pool, tx in ctx), redis, storage
```
