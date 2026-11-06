# Implementation Plan — Echo v5 TODO API (SPEC.md phases 1–22)

Phase 0 is already done (`cmd/api/main.go`, Echo v5.4.4 pinned). Phase 23 is out of scope.

## Decisions

| Topic | Decision |
|---|---|
| Scope | Phases 1–22 (no Phase 23 mastery exercises) |
| Commits | One commit per phase, conventional messages, in a repo initialised inside `echo-todo-2/` |
| Module | `echo-todo-2`, Go 1.27.3 |
| Migrations | `pressly/goose` embedded as a library, `cmd/migrate` + `make migrate`, reversible up/down |
| SQL | `sqlc` (pgx/v5 driver) for static queries; dynamic list query built with an allowlisted builder |
| OpenAPI | Spec-first `api/openapi.yaml` + `oapi-codegen` `echo5-server` + models into `internal/api`; handlers implement `ServerInterface`; kin-openapi drift test |
| Redis | Optional: empty `REDIS_URL` → no-op cache. Idempotency stored in Postgres |
| Uploads | Local filesystem behind a `Storage` interface |
| UI | Placeholder `web/dist` embedded with `//go:embed all:dist` |
| CI | GitHub Actions |
| IDs | `github.com/google/uuid` |
| Refresh token | Returned in body for API clients; also set as HttpOnly cookie (`Path=/v1/auth`) when `X-Client: browser` |

## Layout

```
cmd/api/main.go            wiring only
cmd/migrate/main.go        goose up/down/status
internal/config            env parsing + eager validation
internal/apperr            Kind taxonomy, *Error (StatusCode + ProblemError)
internal/platform/logger   slog setup
internal/platform/postgres pool, WithTx, tracer
internal/platform/redis    client + cache abstraction
internal/platform/storage  local FS storage
internal/httpx             ErrorHandler, respond, validator, server (newServer/routes)
internal/httpx/middleware  requestctx, accesslog, auth, rbac, idempotency, recover
internal/auth              argon2id, JWT, refresh rotation/families, API keys, handlers
internal/todo              model, repo, service, handler, cache, attachments
internal/outbox            outbox repo + worker
internal/api               oapi-codegen output
db/migrations, db/queries, sqlc.yaml
api/openapi.yaml
docs/architecture.md, docs/performance.md, docs/runbook.md, docs/production.md
web/dist                   placeholder SPA
loadtest/                  k6 + hey scripts
Dockerfile, compose.yaml, Makefile, .github/workflows/ci.yml
```

## Phases

| # | Deliverable | DoD verification |
|---|---|---|
| 1 | `docs/architecture.md`: request lifecycle ServeHTTP → Pre → router → Use → handler → error handler; placement of every middleware | Doc exists |
| 2 | Skeleton, `config.Load()` (LookupEnv, typed parsing, loud failures), slog logger | Unit test: refuses boot without `DATABASE_URL`/`JWT_SECRET` |
| 3 | `newServer` with `RouterConfig`, `StartConfig` + signal ctx, `BeforeServeFunc` timeouts, `OnShutdownError` | Test: slow request drains on cancel; hung request reported |
| 4 | Migrations (citext, pgcrypto, schema), sqlc queries, Repo, `WithTx`, `ErrVersionConflict` | Repo tests vs testcontainers Postgres; `make migrate` |
| 5 | `apperr` taxonomy, todo service owning tx and mapping storage errors | Unit test with fake repo; test asserting no pgx import in handler pkg |
| 6 | `ErrorHandler` (RFC 9457 problem+json, committed guard, kind-based logging) | 404 problem+json; DB-down 500 without SQL in body |
| 7 | Validator (`Validate`/`ValidateCtx`) with per-field errors, BindBody / QueryParamOr, BodyLimit | 422 field errors, `limit=abc` → 422, 10MB → 413 |
| 8 | Groups, named routes + Reverse, RemoveTrailingSlash, custom 404/405 | Route-table test: reachable, JSON 404, 405 + Allow, no `//` |
| 9 | Custom middlewares (request ctx, access log, AuthRequired, RequireRole, recover), built-in ordering, Skippers | Access log has request_id, user_id, status, latency, route |
| 10 | Prometheus `/metrics`, OTel tracing + DB spans, guarded pprof, IP/Scheme extractors | Metrics test, in-memory span exporter test, XFF rate-limit key test |
| 11 | argon2id, register/login (timing-equalised), JWT via echo-jwt/v5, refresh rotation + family revoke, logout, cookies, API keys | Auth flow tests incl. expired → 401 with machine code |
| 12 | Ownership in SQL, 404 for foreign todos, RequireRole | User-B matrix: 404 GET/PATCH/DELETE, 403 admin |
| 13 | Secure headers, CORS, CSRF (cookie mode), rate limits, DB per-query timeouts, pagination cap | `security_test.go`: headers, CSRF, 429, 413 |
| 14 | Filters, allowlisted sort, keyset cursor, optional count, tsvector + GIN search | 500-row paging stable, no dupes/gaps; bad sort → 422 |
| 15 | ETag / If-Match (required), 409 with current representation, Idempotency-Key middleware (PG, 24h) | Concurrent PATCH one 200 / one 409; replay identical, no dup |
| 16 | Attachments (size, ext, sniffing, local storage), Attachment/Inline, Stream export, embedded SPA + HTML5 + cache headers | 20MB rejected, renamed .exe rejected, deep link → index.html |
| 17 | Redis cache `todo:{id}:v{n}`, If-None-Match → 304, transactional outbox + worker | 304, purge on delete, no outbox loss on worker crash |
| 18 | Remaining tests, echotest fixtures, fuzz, JWT contract test, coverage | ≥80% todo, ≥90% auth, 30s fuzz clean |
| 19 | Benchmarks, k6/hey scripts, `docs/performance.md` | Local measurements recorded |
| 20 | OpenAPI spec, oapi-codegen echo5 stubs, handlers implement interface, drift test | Compile-time assertion + kin-openapi test |
| 21 | Dockerfile (distroless, non-root), compose (pg, redis, migrate, api), GH Actions | `docker compose up` works |
| 22 | `/readyz`, runbook, backup/restore script, alert rules, checklist | `docs/production.md` checklist |

## Caveats

- Perf targets measured on the local machine.
- CI workflow can be linted locally but only proven on GitHub.
- Manual-ish DoDs (Ctrl-C drain, proxy IP) covered by automated tests.
