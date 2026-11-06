-- name: EnqueueOutbox :exec
INSERT INTO outbox (topic, payload, available_at) VALUES ($1, $2, $3);

-- name: ClaimOutboxBatch :many
-- SKIP LOCKED lets several workers drain concurrently without double-processing;
-- the row locks are held until the surrounding transaction ends.
SELECT id, topic, payload, attempts
FROM outbox
WHERE processed_at IS NULL AND available_at <= now()
ORDER BY id
LIMIT $1
FOR UPDATE SKIP LOCKED;

-- name: MarkOutboxProcessed :exec
UPDATE outbox SET processed_at = now(), attempts = attempts + 1, last_error = NULL WHERE id = $1;

-- name: MarkOutboxFailed :exec
UPDATE outbox SET attempts = attempts + 1, last_error = $2, available_at = $3 WHERE id = $1;

-- name: CountPendingOutbox :one
SELECT count(*) FROM outbox WHERE processed_at IS NULL;

-- name: PurgeProcessedOutbox :execrows
DELETE FROM outbox WHERE processed_at IS NOT NULL AND processed_at < $1;
