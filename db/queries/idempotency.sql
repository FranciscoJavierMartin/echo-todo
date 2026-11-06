-- name: ClaimIdempotencyKey :execrows
INSERT INTO idempotency_keys (user_id, key, request_hash, expires_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id, key) DO NOTHING;

-- name: GetIdempotencyKey :one
SELECT user_id, key, request_hash, state, status, response_headers, response_body, created_at, expires_at
FROM idempotency_keys WHERE user_id = $1 AND key = $2;

-- name: CompleteIdempotencyKey :exec
UPDATE idempotency_keys
SET state = 'completed', status = $3, response_headers = $4, response_body = $5
WHERE user_id = $1 AND key = $2;

-- name: DeleteIdempotencyKey :exec
DELETE FROM idempotency_keys WHERE user_id = $1 AND key = $2;

-- name: DeleteExpiredIdempotencyKey :execrows
DELETE FROM idempotency_keys WHERE user_id = $1 AND key = $2 AND expires_at <= now();

-- name: PurgeExpiredIdempotencyKeys :execrows
DELETE FROM idempotency_keys WHERE expires_at <= now();
