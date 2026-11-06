# Production readiness checklist (Phase 22)

Each item links to the code or test that proves it.

- [x] **Graceful shutdown verified under load.** `TestGracefulShutdownUnderLoad`
  (internal/server/shutdown_load_test.go) runs 32 concurrent clients with no broken request.
  `TestGracefulShutdown_HungRequestIsReported` covers the cut-off case. `docker compose stop api`
  exits 0 after `outbox worker stopped` / `server stopped cleanly`.
- [x] **All secrets from env / secret manager, none in code.** internal/config is the only reader of
  the environment. `JWT_SECRET` (≥ 32 bytes, plus `JWT_PREVIOUS_SECRETS` for rotation), database and
  Redis URLs, and `METRICS_TOKEN` come from env. Boot fails without them (`TestLoad_RequiresDatabaseURLAndSecret`).
  Compose defaults are for local use only.
- [x] **Timeouts on DB, HTTP client, and handlers.**
  - Handlers: `ContextTimeout` (`REQUEST_TIMEOUT`, 10s) in `Pre`.
  - DB: a per-call `context.WithTimeout` in every repository method (`DB_QUERY_TIMEOUT`, 3s,
    `TestRepo_QueryTimeout`), plus a server-side `statement_timeout` backstop.
  - Redis: dial/read/write timeouts. The healthcheck HTTP client: 2s.
  - Server: `ReadHeaderTimeout`, `ReadTimeout`, `IdleTimeout`, `MaxHeaderBytes`.
- [x] **Rate limits on auth and writes.** Per IP globally, per IP on `/v1/auth/*` (5/s, burst 10),
  per user on todo writes (`TestAuthRateLimit`, `TestWriteRateLimitPerUser`,
  `TestRateLimitKeysOnRealClientIPBehindProxy`).
- [x] **Security headers, CORS, CSRF all asserted by tests.** internal/app/security_test.go covers
  headers on 200/401/404/405/422, HSTS only behind a trusted TLS proxy, CSRF rejection and the
  double-submit flow, CORS allow and deny, and 413s.
- [x] **Structured logs with request IDs, no PII.** slog JSON in prod. Every line carries
  `request_id`, plus `user_id` (a UUID) once authenticated, and `trace_id`. Client IPs are opt-in
  (`LOG_CLIENT_IP`). `TestLogsContainNoPII` asserts that emails, passwords, access/refresh tokens
  and client IPs never reach the logs.
- [x] **Metrics, traces, alerts on 5xx rate and p95.** Prometheus `/metrics` (route-template labels,
  optional `METRICS_TOKEN`). OTLP tracing with per-query spans. deploy/prometheus/alerts.yml
  (validated with `promtool check rules`) covers the 5xx ratio, list p95 > 50 ms, overall p95,
  login failure spikes, 429 spikes, and instance down.
- [x] **Backups + tested restore; migrations reversible.** scripts/backup.sh, scripts/restore.sh,
  and scripts/backup-restore-test.sh (dump → restore → compare row counts and goose version;
  verified against compose). `TestEachMigrationUpDownUp` applies, rolls back and re-applies every
  migration. CI runs `migrate up && migrate reset && migrate up`.
- [x] **pprof guarded, `/healthz` and `/readyz` separate.** `/debug/pprof/*` requires an admin
  (`TestPprofRequiresAdmin`). `/healthz` is liveness and never touches dependencies; `/readyz` pings
  Postgres and Redis (`TestOpsEndpoints`).
- [x] **Dependency and container scanning in CI.** `govulncheck`, `staticcheck`, and a trivy scan of
  the image that fails on HIGH/CRITICAL (.github/workflows/ci.yml).
- [x] **Runbook: rollback, rotate JWT secret, revoke all sessions.** docs/runbook.md, with
  `admin revoke-sessions` and kid-based JWT rotation (`TestSecretRotation`).
