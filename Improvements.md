I've read the whole codebase (all 178 tracked files, every non-test source file, the CI, Docker, compose, OpenAPI spec, migrations, and docs). Here's the exhaustive gap list.
Review: what's missing in echo-todo-2
Context: Phases 0–22 of SPEC.md are implemented and green. The gaps below are everything outside that checklist: tooling, product features, ops, and a handful of real defects I found while reading.
1. Real defects found while reading (highest priority)
#	Issue	Evidence
1.1	make lint is a silent no-op. .PHONY lists lint but the target has no recipe — make lint prints "Nothing to be done for 'lint'" and exits 0. CI never calls it, so the hole is invisible.	Makefile:5 vs Makefile:60-64
1.2	A 34 MB compiled test binary is committed to git. server.test is tracked and was added in the phase-19 commit. .gitignore covers *.out but not *.test.	git ls-files → server.test; Makefile has no clean target either
1.3	.env is not in .dockerignore. COPY . . bakes the local .env (which exists on disk and holds a JWT secret) into the builder layer and into the exported GHA build cache. Not in the runtime image, but still a leak into the cache.	.dockerignore lacks .env; .env exists locally
1.4	docs/production.md claims a timeout the code doesn't set. The checklist says "Server: ReadHeaderTimeout, ReadTimeout, IdleTimeout, MaxHeaderBytes" — StartConfig.BeforeServeFunc sets only ReadHeaderTimeout, IdleTimeout, MaxHeaderBytes. No ReadTimeout.	internal/server/server.go:77-83 vs docs/production.md:18
1.5	docs/production.md claims promtool validation that no CI job performs. "validated with promtool check rules" — there is no promtool step, and no promtool binary anywhere in the repo.	docs/production.md:30-32, .github/workflows/ci.yml
1.6	Compose does not pass through most of the documented env vars. Only APP_ENV, DATABASE_URL, REDIS_URL, JWT_SECRET, CORS_ORIGINS, TRUSTED_PROXIES, OTEL_EXPORTER_OTLP_ENDPOINT are forwarded. JWT_PREVIOUS_SECRETS, METRICS_TOKEN, RATE_LIMIT_RPS, AUTH_RATE_LIMIT_RPS, WRITE_RATE_LIMIT_RPS, ARGON2_*, LOG_CLIENT_IP, EXPOSE_ERRORS, REQUEST_TIMEOUT, SHUTDOWN_TIMEOUT, MAX_BODY_BYTES, MAX_UPLOAD_BYTES are documented in the README but unreachable via compose.	compose.yaml:45-52 vs README.md:28-44
1.7	server.New silently disables all metrics when Deps.Telemetry is nil — it falls back to telemetry.NewForTest(nil), swapping in a fresh registry, so /metrics would return an empty scrape instead of failing loudly. cmd/api always sets it, so this is a footgun, not a live bug.	internal/server/server.go:41-43
1.8	No ReadTimeout means a slow-body POST is only bounded by BodyLimit (which caps bytes but not the time spent trickling them). REQUEST_TIMEOUT does cover it via ContextTimeout — so this is lower severity than it looks, but the doc claim is still wrong.	as 1.4
1.9	The API version is compiled in but never surfaced. -X main.version= is set in the Dockerfile, but there is no api --version / api version command and no version in /healthz or /readyz, so you can't confirm what's deployed from the running instance.	cmd/api/main.go:26, Dockerfile:14
2. Developer tooling
2.1 Missing outright
- No .golangci.yml and no golangci-lint anywhere. CI runs staticcheck + go vet as separate go run ...@latest steps — unpinned, and missing the whole linter set (errcheck, revive, gocritic, ineffassign, misspell, bodyclose, unparam, prealloc, nakedret, etc.). errcheck in particular is absent, so unchecked errors are invisible outside the #nosec spots you annotated by hand.
- No .editorconfig, no .gitattributes.
- No dependabot.yml — dependency and GitHub-Action updates are entirely manual.
- No CODEOWNERS, no PR template, no issue templates.
- No CONTRIBUTING.md, no LICENSE, no SECURITY.md, no CHANGELOG.md.
- No pre-commit hooks, no devcontainer, no goreleaser / release workflow.
- No .env.example. Only .env (gitignored) exists, so a new contributor has no template of the 20+ variables the README documents.
- No shellcheck/actionlint/hadolint in CI for scripts/*.sh, the workflow, or the Dockerfile.
- No gitleaks/secret scanning in CI — and there is a populated .env on disk.
2.2 Makefile gaps
- lint target: broken (1.1). No golangci-lint wiring.
- No bench target — internal/server/bench_test.go exists and docs/performance.md quotes its numbers, but there's no way to re-run them.
- No loadtest target, no seed target, no restore target (only restore-test), no psql/db-reset helper, no clean target, no verify aggregate target, no seed for a local demo user.
- sqlc isn't pinned for local use. CI pins sqlc@v1.31.1; make sqlc calls whatever sqlc is on $PATH. Meanwhile api/oapi-codegen is pinned via go.mod's tool directive — asymmetric.
- No //go:generate for sqlc. api/embed.go has one; db/queries/*.sql has none, so make generate is half-declarative.
- fmt/vet lack ##  docstrings, so make help doesn't list them.
- cover runs without -race and merges profiles from a single pass; no gotestsum/JUnit output for CI reporting, and no test-results artifact.
2.3 CI gaps
- No image publish, SBOM, signing, or provenance. The image job builds with load: true and scans with Trivy, then throws the image away. No GHCR push, no syft SBOM, no cosign keyless signing, no SLSA provenance, no release/tag workflow.
- No Go version matrix (one version only), no build matrix for other OS/arch, no binaries published for download.
- No go mod tidy/go mod verify drift check.
- No OpenAPI breaking-change detection (oasdiff between base and PR).
- No OpenAPI lint (spectral/redocly) — the spec is hand-written YAML with no linting.
- No promtool check rules despite the docs claiming it.
- No backup/restore drill in CI — scripts/backup-restore-test.sh exists but is never executed by a workflow.
- No API smoke test in CI. The image job curls /readyz and stops. Nothing exercises login → create → list → update → delete against the built image.
- No load test in CI (k6/hey scripts exist but never run).
- No golangci-lint job.
- GitHub Actions are pinned by tag (@v4, @v5), not by SHA — no supply-chain hardening.
- image job grants security-events: write but never uploads SARIF.
- permissions isn't minimized per-job (lint/test inherit top-level only, which is fine, but there's no job-level least-privilege story).
2.4 Testing gaps
- No tests for cmd/ or tools/ at all. cmd/admin (4 subcommands that mutate production auth state), cmd/migrate, cmd/api, and tools/covercheck are entirely untested.
- Coverage gate only covers internal/todo and internal/auth. Running the existing profile at 70% for the other trees: internal/idempotency 69.7% and internal/platform 63.8% both fail. internal/outbox is 72.1% — a hair above.
- No committed fuzz corpus (testdata/fuzz), only in-code seeds for the single FuzzBindCreateTodo target. Only one fuzz target exists.
- No go test -shuffle=on — order-dependent flakes would be invisible.
- No t.Parallel() anywhere; tests are strictly serial and slower than needed.
- No property-based or table-driven generation beyond hand-written cases.
- No benchmark regression baseline (no benchstat, no stored .txt baselines to diff against).
- internal/todo/arch_test.go only guards internal/todo and internal/todo/handler. No equivalent boundary test for internal/auth, internal/httpx, or internal/server (which must stay Echo-coupled, but nothing asserts it).
- The coverage.out on disk is dated before the last commit — treat my coverage numbers as indicative, not exact.
3. Product / API features missing
3.1 Account lifecycle (the biggest product hole)
- No password change endpoint (POST /v1/auth/password). Users cannot rotate their own password without an operator.
- No password reset / forgot-password flow.
- No email verification.
- No MFA / TOTP / step-up auth — and admin role gates (RequireRole("admin")) have no re-authentication, so a stolen admin JWT is full admin.
- No account deletion or user-data export (GDPR-shaped gap).
- No session/device management: no list of refresh-token families, no "log out this device", no session table view for the user.
- No "revoke all API keys" — only per-key revoke.
3.2 API keys
- No expiry — api_keys has no expires_at column, so a leaked key is valid forever.
- No scopes — a key inherits the owner's role via a JOIN, so every key is full-power. The APIKey.Role field is populated but never set independently.
- No key rotation, no key naming conventions, no last-used rotation policy.
- last_used_at is written on every request (TouchAPIKey) — an extra write per authenticated API request.
3.3 Todos
- Soft-deleted todos are unreachable. deleted_at is set, but there is no ?deleted=true filter, no trash listing, and no restore/purge endpoint. Deleted rows accumulate forever with no retention job.
- No bulk operations (bulk create/update/delete/patch) — the single biggest usability gap for a todo API.
- No tags/labels, no sub-todos, no recurring todos, no due-date reminders.
- No full-text relevance ranking. Search is a websearch_to_tsquery filter only; there's no ts_rank ordering, no prefix matching, no per-user language config, no snippet/highlight.
- No timezones. due_at is a bare timestamptz; "due tomorrow at 9am" is impossible.
- No Link header pagination, only next_cursor in the body.
- No If-None-Match on list responses — only single-item GET supports 304.
- Idempotency-Key only on POST /v1/todos. Attachment upload, auth register/login, and API-key creation are not idempotent.
- No If-Match on attachment delete, no optimistic concurrency on attachments at all.
3.4 Attachments
- Local filesystem only — no S3/object-store backend, so the API cannot scale horizontally with attachments.
- No Range requests — no resumable downloads, no partial GETs.
- No per-user storage quota; MAX_UPLOAD_BYTES is per-file only.
- No antivirus/content scanning, no image processing (thumbnails, EXIF stripping — EXIF is a real PII leak in a photo upload).
- No Content-Disposition RFC 5987 encoding for non-ASCII filenames (filename*=UTF-8''…).
- No ETag/conditional GET on downloads despite the SHA-256 being stored.
3.5 Outbox / async
- The outbox has no real consumer. LogNotifier logs the payload; there is no email/push/webhook provider. The transactional-outbox pattern is built but never connected to anything.
- No webhook subscriptions — no subscribe/unsubscribe API, no delivery attempts, no HMAC signing of outbound webhooks.
- No dead-letter queue. Failed messages retry forever with a 10-minute cap; attempts grows unbounded and there is no terminal state, no admin command to inspect or requeue a poison message.
- No admin command to list/purge failed outbox rows (only outbox-pending prints a count).
- No cron/scheduler beyond the outbox drain loop.
3.6 Platform features (SPEC Phase 23, all 10 items unimplemented)
Custom Router with match logging; multi-tenant echo.Echo + NewVirtualHostHandler; a separate admin API on its own *echo.Echo with its own middleware stack; SSE streaming endpoint with 30 s heartbeat; WebSocket live updates; ProblemErrorer-based error type with exact-body assertion; manual HEAD vs AutoHandleHEAD; html/template renderer with CSRF tokens; middleware.Proxy fronting a legacy service; jsoniter swap + measurement.
4. HTTP / API-contract gaps
- No response compression. No gzip/Brotli middleware — only the Scalar bundle is pre-gzipped. JSON responses are uncompressed on the wire.
- No RateLimit-Limit / RateLimit-Remaining / RateLimit-Reset headers, despite a full rate-limiting system.
- No Retry-After on 503 (only on 429 and on idempotency-in-progress).
- X-Request-Id is not in the OpenAPI spec even though it's echoed on every response — and it isn't in the spec's response headers either.
- No Retry-After/RateLimit-* in the spec; several documented status codes aren't uniform across operations (e.g. 403 missing on some 401-only ops).
- No API versioning/deprecation policy — the path is /v1 but there's no Sunset/Deprecation header story or doc.
- No Problem type URIs — the handler hardcodes about:blank; RFC 9457 wants a URI that identifies the problem type.
- No Cache-Control on most endpoints, only on a handful (todo GET, export, tokens). List responses are uncached-by-default but also not explicitly no-store.
- No WWW-Authenticate on 403/429 (only on 401).
- No Permissions-Policy, Cross-Origin-Opener-Policy, Cross-Origin-Resource-Policy headers; CSP is decent but incomplete.
- No HSTS includeSubDomains / preload.
- No cookie __Host- prefix and no SameSite configurability.
- No Clear-Site-Data on logout.
- No OPTIONS/preflight handling tests, no HEAD contract tests per route.
5. Data layer gaps
- No pool sizing configuration. MaxConns/MinConns are not exposed; MaxConnLifetime/MaxConnIdleTime are hardcoded (1 h / 5 min). No DB_MAX_CONNS env var.
- statement_timeout is derived as 2*DBQuery_TIMEOUT and hardcoded — no separate knob.
- No TLS options for Postgres or Redis beyond what the URL implies.
- No created_at/updated_at DB triggers. updated_at is bumped only in the specific queries that remember to (todos.sql, users.sql role/password) — a new update path can silently miss it.
- No soft-delete retention/purge job and no deleted_at index for cleanup queries.
- No todo_audit retention/archival job — the audit table grows forever.
- No partitioning on todos / todo_audit / outbox for large-scale deployments.
- No pg_stat_statements / slow-query guidance in docs/performance.md.
- No index-usage monitoring to detect the next missing index.
- No EXPLAIN regression test to lock in the phase-19 index win.
- No migration lock advisory / concurrent-migration safety notes.
- No citext/pgcrypto extension audit in the docs.
6. Observability gaps
- No custom application metrics. Only HTTP request/duration counters plus Go/process collectors. Missing: DB pool stats (pgxpool.Stat), cache hit ratio, outbox depth/lag, idempotency conflicts, rate-limit rejections by bucket, auth failure counts, attachment bytes, active users.
- No tracing for the outbox worker, Redis, or storage — only HTTP + pgx spans.
- No OpenTelemetry logs export; slog goes to stdout with no OTLP log pipeline.
- No log level env var (LOG_LEVEL) — hardcoded Info (prod) / Debug (dev).
- No log sampling for high-volume access logs.
- No Grafana dashboards, no Prometheus scrape config, no recording rules, no Alertmanager routing (only deploy/prometheus/alerts.yml).
- Alert rules cover HTTP only. Missing: outbox backlog, outbox delivery failure rate, DB pool saturation, disk space, backup age/failure, migration pending, idempotency-store growth, cache hit ratio, Redis down, DB down (only "instance down").
- No SLO/error-budget alerting — the p95 thresholds are static, not burn-rate based.
- No readyz check for the outbox worker — a wedged worker still reports ready.
7. Ops / infrastructure gaps
- No Kubernetes manifests, Helm chart, or Terraform. Deploy is compose-only; there is no manifest for the phase-22 rollout story the runbook describes.
- No Prometheus scrape config or Grafana provisioning to make the shipped alerts usable.
- No OTel collector / Tempo / Jaeger service in compose.
- No alerting delivery config (Alertmanager, Slack/PagerDuty receivers).
- No backup encryption, retention policy, or offsite/PITR. pg_dump --format=custom --compress=6 is neither encrypted nor restorable to a point in time; no wal-g/barman.
- No automated backup schedule — scripts/backup.sh is manual only.
- No admin commands for: listing users, inspecting a user's sessions, requeueing a failed outbox row, creating a user, resetting a password, forcing a role change with immediate effect (currently admin promote takes up to ACCESS_TTL to take effect).
- admin has no migrate proxy — the runbook's deploy step requires a separate binary.
- No secret-manager integration guidance (Vault/AWS SM) — env-only, which the runbook calls acceptable but doesn't cover rotation for DATABASE_URL/REDIS_URL.
- No multi-replica story. The rate limiter uses an in-memory store (NewRateLimiterMemoryStoreWithConfig), so RATE_LIMIT_RPS is per-process — N replicas means N× the intended limit. Same for the byUser write limit. This is the single most consequential production gap.
- No restart: unless-stopped on the postgres/redis compose services (only api has it).
- No compose health-gating on api in the local dev flow beyond the image HEALTHCHECK.
- No TLS anywhere in compose; no mTLS between services.
- No CI artifact for the built image (can't promote the tested image — a new build is produced at deploy time).
8. Documentation gaps
- No API usage guide with real curl examples for the auth flow (register → login → cookie/CSRF → CRUD → ETag → idempotency). The README has a quick start but no request/response walkthrough.
- No error-code table. The spec scatters machine codes across prose (missing_token, token_expired, invalid_credentials, refresh_token_reused, idempotency_key_mismatch, csrf_invalid, precondition_required, timeout, …). There is no single index a client can code against.
- No pagination guide (cursor semantics, sort binding, count=true cost).
- No schema/data-model document — the migrations are the only source of truth.
- No architecture diagrams (request lifecycle is prose-only).
- No ADRs — design decisions live only in plan/PLAN.md's table, which is a plan, not a decision record.
- No runbook entry for: Redis being down (cache disabled — mentioned), DB connection exhaustion, disk full, cert expiry, migration rollback to a specific version (only "one per migration"), rate-limit false positives, outbox poison-message recovery.
- No docs/loadtest.md — how to run k6/hey, seed data, and interpret results is scattered across docs/performance.md and script comments.
- No on-call / escalation guidance beyond alert annotations.
- No contributing guide (how to add an endpoint end-to-end: spec → sqlc → codegen → handler → contract test).
- The README's "Development" section references make lint implicitly via CI but the target is broken; the README also omits make bench, make restore-test prerequisites (compose must be up), and the TEST_DATABASE_URL / PGTEST_REQUIRED knobs.
- web/dist is a hand-written placeholder with hashed filenames (app.3f9c1e.js) but there is no frontend source, no package.json, and no build step — the hash is a lie and the SPA cannot be rebuilt or developed on. Either commit real source + a bundler, or stop pretending it's built output.
9. Suggested priority order
Now (defects, ~1 day): 1.1 broken make lint · 1.2 untrack server.test + ignore *.test · 1.3 .env in .dockerignore · 1.4/1.5 fix or delete the two false claims in docs/production.md · 1.6 compose env passthrough · 1.9 --version.
Next (dev tooling, ~2–3 days): .golangci.yml + pinned versions · dependabot.yml · .env.example · Makefile bench/lint/clean/seed/loadtest targets · sqlc pinning + go:generate for db/queries · CI: image publish + SBOM + signing, OpenAPI lint + oasdiff, promtool, backup/restore drill, a real API smoke test, golangci-lint, SHA-pinned actions.
Then (correctness at scale, ~1 week): Redis-backed rate limiter (multi-replica) · pool sizing config · updated_at triggers · soft-delete visibility + retention jobs · API-key expiry/scopes · outbox dead-letter + admin inspection commands.
Then (product): password change/reset · account deletion · session management · bulk endpoints · webhook delivery wiring the outbox to something real · tags/sub-todos · attachment S3 backend + quotas.
Later: SPEC Phase 23 mastery exercises · a real frontend for web/ · Kubernetes/Helm · Grafana dashboards + SLO burn-rate alerts.
What I skipped / risk: I did not run the full test suite (the coverage.out on disk predates the last commit, so my two failing coverage numbers are indicative). I did not execute the CI, compose, or Docker builds — the CI/Docker findings are from reading the files, not from running them. I did not review the vendored Scalar bundle or font binaries. I gave no code changes; this is a read-only review as requested.
To turn this into work, switch to the default agent and tell me which slice to take — I'd start with §1 plus the .golangci.yml/Makefile/CI tooling block.