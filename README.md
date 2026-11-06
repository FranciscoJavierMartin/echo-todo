# echo-todo-2

A TODO API built with [Echo v5](https://github.com/labstack/echo) that follows SPEC.md phases 1–22. It
includes auth (argon2id, JWT + rotating refresh families, API keys), ownership-scoped SQL, RFC 9457
errors, keyset pagination, full-text search, ETag/If-Match, idempotency keys, streamed uploads,
a Redis cache, a transactional outbox, Prometheus/OTel, an OpenAPI contract, and a distroless image with CI.

## Quick start

```bash
docker compose up --build          # postgres + redis + one-shot migrate + api on :8080
curl localhost:8080/readyz
open http://localhost:8080/app      # placeholder SPA
```

Without Docker:

```bash
make db-up && make migrate
DATABASE_URL=postgres://todo:todo@localhost:5432/todo?sslmode=disable \
JWT_SECRET=$(openssl rand -base64 48) go run ./cmd/api
```

## Configuration (env)

| Variable | Default | Notes |
|---|---|---|
| `APP_ENV` | `dev` | `dev`, `test`, `prod` (JSON logs, stricter checks) |
| `HTTP_ADDR` | `:8080` | |
| `DATABASE_URL` | required | |
| `JWT_SECRET` | required | ≥ 32 bytes. Rotation via `JWT_PREVIOUS_SECRETS` (see the runbook) |
| `REDIS_URL` | empty | Optional. Enables the todo cache |
| `ACCESS_TTL` / `REFRESH_TTL` | `10m` / `720h` | |
| `CORS_ORIGINS` | empty | Explicit origins, comma-separated |
| `TRUSTED_PROXIES` | empty | IPs/CIDRs allowed to set X-Forwarded-For/Proto |
| `RATE_LIMIT_RPS` / `AUTH_RATE_LIMIT_RPS` / `WRITE_RATE_LIMIT_RPS` | `20` / `5` / `10` | Per IP / per IP on auth / per user on writes |
| `MAX_BODY_BYTES` / `MAX_UPLOAD_BYTES` | 1 MiB / 10 MiB | |
| `REQUEST_TIMEOUT` / `DB_QUERY_TIMEOUT` / `SHUTDOWN_TIMEOUT` | `10s` / `3s` / `15s` | |
| `UPLOAD_DIR` | `./data/uploads` | |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | empty | Enables tracing |
| `METRICS_TOKEN` | empty | Bearer token for `/metrics` |
| `EXPOSE_ERRORS` | `false` | Forbidden in prod |
| `LOG_CLIENT_IP` | `false` | IPs are personal data |
| `ARGON2_MEMORY_KIB` / `ARGON2_ITERATIONS` / `ARGON2_PARALLELISM` | `65536` / `3` / `2` | |

## Development

```bash
make test         # all tests with -race (Docker for testcontainers, or TEST_DATABASE_URL)
make cover        # unit + e2e coverage, gated: internal/todo ≥ 80%, internal/auth ≥ 90%
make fuzz         # 30s fuzzing of the create-todo binding
make generate     # sqlc + oapi-codegen (api/openapi.yaml → internal/api)
```

Every end-to-end test response is validated against `api/openapi.yaml`. An undocumented status code
or schema drift fails the build.

## Layout

```
cmd/api, cmd/migrate, cmd/admin   binaries (wiring only)
internal/app                      builds the app; end-to-end tests
internal/server                   Echo config, middleware chain, routes, OpenAPI adapter
internal/httpx                    error handler, validator, binding, middleware
internal/todo, internal/auth      domain, services, handlers, Postgres repos
internal/outbox, internal/idempotency, internal/platform/*
api/openapi.yaml, db/migrations, db/queries, web/dist
```

## Docs

* [docs/architecture.md](docs/architecture.md): request lifecycle, middleware placement, v5 gotchas
* [docs/performance.md](docs/performance.md): benchmarks, load tests, profiling findings
* [docs/runbook.md](docs/runbook.md): deploy, rollback, secret rotation, session revocation
* [docs/production.md](docs/production.md): readiness checklist with evidence
