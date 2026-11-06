# Performance notes (Phase 19)

All numbers were measured on a 16-core workstation (local Postgres 16 and Redis 7 in Docker,
API built with `go build`, `APP_ENV=prod` JSON logs to a file). Reproduce with
`loadtest/seed.sql`, `loadtest/hey.sh` and `loadtest/k6/list.js`.

## SLO: p95 < 50 ms at 200 rps on the list endpoint

`GET /v1/todos?limit=20&sort=-priority,due_at&status=open`, authenticated, `hey -z 30s -c 50 -q 4`:

| Dataset (one user) | p50 | p95 | p99 | Saturation (`-c 50`, no rate cap) |
|---|---|---|---|---|
| 1,000 todos, before fixes | 6.6 ms | 11.2 ms | 13.8 ms | ~12,400 rps |
| 51,000 todos, before index fix | 32.8 ms | **47.1 ms** | 52.6 ms | 1,975 rps |
| 51,000 todos, after index fix | 5.5 ms | **9.5 ms** | 13.2 ms | 14,448 rps |

Other data points (51k rows, saturation):

* same list with `count=true`: 6,329 rps. The count roughly halves throughput, which is why
  `total` is opt-in (`?count=true`) and never computed by default.
* `GET /v1/todos/:id` (Redis cache hit): 31,700 rps, p95 2.8 ms.

## Micro-benchmarks (`go test -bench . -benchmem ./internal/server/`)

| Benchmark | ns/op | allocs/op |
|---|---|---|
| Router static `/healthz` | 12.8 | **0** |
| Router param `/v1/todos/:id` | 40.7 | **0** |
| Router two params | 30.5 | **0** |
| `e.Any("/v1/todos/:id")` | 19.2 | 0 |
| `HEAD` with `AutoHandleHEAD` | 71.7 | 0 |
| `HEAD` without it (405 path) | 124.5 | 1 |
| `NewConcurrentRouter`, single goroutine | 22.0 | 0 |
| `NewConcurrentRouter`, `RunParallel` (16 cores) | 162.5 | 0 |
| `DefaultRouter`, `RunParallel` (16 cores) | 3.9 | 0 |
| Full chain `GET /v1/todos/:id` (in-memory service) | ~58,000 | 131 (was 137) |

Takeaways:

* The router hot path allocates nothing, and the pooled `*echo.Context` never escapes. Allocations
  come from middleware and the handler, not from routing.
* `Any` is slightly faster than explicit methods, because one method slot matches everything. But it
  breaks 405/`Allow` semantics, so we keep explicit methods.
* `AutoHandleHEAD` costs about 30 ns more than a GET. The 405 path costs more because it allocates the `Allow` value.
* `NewConcurrentRouter` costs nothing single-threaded. Under parallel load its `RWMutex` read lock
  bounces a cache line between cores: 3.9 → 162 ns per lookup, about 40×. We never add routes at
  runtime, so we keep `DefaultRouter`.

## The three hottest paths and what changed

From a 15 s CPU profile taken at about 12k rps through the admin-guarded `/debug/pprof/profile`:

1. **`pgrepo.List` (≈28 % cumulative): Postgres query + row scan.** `EXPLAIN (ANALYZE, BUFFERS)`
   at 51k rows showed two problems. The mixed-direction sort `priority DESC, due_at ASC` could not
   use the all-ASC index, so the planner fetched the whole top-priority group (3,401 rows, 10,417
   buffers) and sorted it: **14–17 ms**. And `status = ANY($1::todo_status[])` is not an equality to
   the planner, so even a matching index could not provide the order.
   *Changes:* migration `00009_list_sort_index.sql` adds
   `(owner_id, status, priority DESC, coalesce(due_at,'infinity'), id) WHERE deleted_at IS NULL`.
   The repository now renders single-value filters as `status = $n` / `priority = $n`.
   Result: **0.31 ms**, 24 buffers, and 7.3× throughput on the endpoint.
2. **JSON encoding (≈15 %)** (`encoding/json` + time formatting) **and response writing (≈9 %)**.
   Not changed. The spec says to swap `JSONSerializer` only if JSON dominates, and after the DB fix
   it does not: the endpoint already runs at 14k rps with p95 under 10 ms. A pooled-buffer
   serializer that sets `Content-Length` (no chunked encoding) is the next step if this ever matters.
3. **Per-request context and logger cloning in middleware** (top allocation sites:
   `Request.WithContext`, `slog` handler clones) and **JWT HMAC verification (≈6.5 %)**.
   *Change:* `SetPrincipal` no longer clones the request logger with `user_id`. The ID already lives
   in the request `context.Context`, and the logger's context handler adds it to every record logged
   with that context. This saves 6 allocations / ~340 B per authenticated request
   (137 → 131 allocs/op in `BenchmarkFullChain/get`). JWT verification is inherent to the
   stateless design and stays.

## Reproducing

```bash
docker compose up -d postgres redis
make migrate
psql "$DATABASE_URL" -v n=50000 -f loadtest/seed.sql
APP_ENV=prod RATE_LIMIT_RPS=100000 JWT_SECRET=... go run ./cmd/api &
JWT_SECRET=... ./loadtest/hey.sh
# profile under load (admin token):
curl -H "Authorization: Bearer $(JWT_SECRET=... go run ./tools/mint -sub $(uuidgen) -role admin)" \
     -o cpu.out "http://localhost:8080/debug/pprof/profile?seconds=15"
go tool pprof -top -cum cpu.out
```
