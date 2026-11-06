# Runbook

Binaries in the image: `api` (server, plus `api healthcheck`), `migrate` (`up|down|status|reset`), and
`admin` (`revoke-sessions|promote|purge|outbox-pending`). All of them read configuration from the environment.

## Deploy

1. CI is green: lint, vet, staticcheck, govulncheck, `-race` tests, the coverage gate, fuzz, and the
   trivy image scan.
2. Run `migrate up` as a one-shot job **before** rolling out the new API version. The compose
   `migrate` service does the same thing. Migrations must be backwards compatible with the
   running version (expand, then contract).
3. Roll out the API. Instances are taken out of rotation by `/readyz`, not `/healthz`.
4. Watch the `TodoHigh5xxRate` and `TodoListLatencyP95High` alerts for 15 minutes.

## Rollback

* **API only:** redeploy the previous image tag. The schema is backwards compatible by rule (step 2
  above), so no DB action is needed.
* **Migration:** every migration has a tested `Down` (`TestEachMigrationUpDownUp`). Run
  `migrate down` once per migration to undo, **after** rolling the API back. Take a backup first
  (`scripts/backup.sh`). `Down` of a column or table addition drops data written since.
* **Bad data:** restore into a side database with `scripts/restore.sh <dump> todo_restore` and copy
  rows back selectively. Never restore over `todo` while the API is running.

## Graceful shutdown

SIGTERM stops accepting connections and lets in-flight requests finish within `SHUTDOWN_TIMEOUT`
(15s by default). The outbox worker finishes or rolls back its current batch. Set the orchestrator's
grace period above `SHUTDOWN_TIMEOUT` (compose: `stop_grace_period: 20s`). The log line
`graceful shutdown did not complete` means requests were cut off. Investigate slow handlers.

## Rotate the JWT secret

Access tokens carry a `kid` derived from the signing secret, so a rotation logs nobody out:

1. Generate a new secret: `openssl rand -base64 48`.
2. Deploy with `JWT_SECRET=<new>` and `JWT_PREVIOUS_SECRETS=<old>`. New tokens are signed with the
   new key, and tokens signed with the old key still verify.
3. After one `ACCESS_TTL` (10 minutes by default) plus a margin, remove `JWT_PREVIOUS_SECRETS` and
   deploy again.

Refresh tokens are opaque and stored hashed in the DB, so a JWT secret change does not affect them.

**Suspected secret leak:** skip the grace window. Deploy with only the new `JWT_SECRET`. Every access
token becomes invalid immediately, and clients refresh with their refresh tokens. If refresh tokens
may also be compromised, continue below.

## Revoke all sessions

```bash
admin revoke-sessions                 # every user: all refresh tokens revoked
admin revoke-sessions -user <uuid>    # one user (also: POST /v1/auth/logout-all as that user)
```

Revoked refresh tokens stop working at once. Access tokens stay valid until they expire, up to
`ACCESS_TTL`. To cut those off immediately as well, rotate `JWT_SECRET` with no grace window (see above).

## Elevated 5xx

1. Check the logs at `level=ERROR` and `msg="request failed"`. Each line carries `op`, `request_id`,
   `trace_id` and the wrapped cause. Clients see a generic problem body that includes the `request_id`
   to quote.
2. Check `/readyz`. `postgres: down` or `redis: down` points at a dependency. A Redis outage only
   disables the cache: requests keep working, just slower.
3. Check `code="timeout"` (503). This means the DB or a request is over budget; see Latency.

## Latency

1. Look at the trace of a slow request (OTLP backend). It contains a child span per SQL query.
2. Run `EXPLAIN (ANALYZE, BUFFERS)` on the slow statement. See docs/performance.md for a worked example.
3. Profile under load: `curl -H "Authorization: Bearer <admin token>" -o cpu.out
   "https://host/debug/pprof/profile?seconds=15"`, then `go tool pprof -top -cum cpu.out`.

## Credential stuffing

Failed logins are rate limited per IP (`AUTH_RATE_LIMIT_RPS`, 5/s with a burst of 10). Behind a
proxy, make sure `TRUSTED_PROXIES` is set, or every client shares the proxy's bucket. Lower the limit
for an active attack, and consider `admin revoke-sessions -user` for accounts that show successful
logins from the attack.

## Outbox backlog

`admin outbox-pending` prints the number of undelivered events. Rows with a growing `attempts` and
a `last_error` show which provider is failing. Delivery is at least once and retried with exponential
backoff (capped at 10 minutes), so a backlog drains by itself once the provider recovers.

## Promote an admin

`admin promote -email ops@example.com`. The role is read from the access token, so it takes effect
at the user's next refresh (at most `ACCESS_TTL`).

## Housekeeping

The worker purges expired idempotency keys and processed outbox rows hourly. `admin purge` does the
same on demand.
