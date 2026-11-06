-- name: InsertRefreshToken :one
INSERT INTO refresh_tokens (user_id, family_id, token_hash, expires_at, user_agent)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, user_id, family_id, token_hash, expires_at, revoked_at, replaced_by, user_agent, created_at;

-- name: GetRefreshTokenByHashForUpdate :one
SELECT id, user_id, family_id, token_hash, expires_at, revoked_at, replaced_by, user_agent, created_at
FROM refresh_tokens WHERE token_hash = $1
FOR UPDATE;

-- name: MarkRefreshTokenRotated :exec
UPDATE refresh_tokens SET revoked_at = now(), replaced_by = $2
WHERE id = $1;

-- name: RevokeRefreshFamily :execrows
UPDATE refresh_tokens SET revoked_at = now()
WHERE family_id = $1 AND revoked_at IS NULL;

-- name: RevokeAllRefreshTokensForUser :execrows
UPDATE refresh_tokens SET revoked_at = now()
WHERE user_id = $1 AND revoked_at IS NULL;

-- name: RevokeAllRefreshTokens :execrows
UPDATE refresh_tokens SET revoked_at = now() WHERE revoked_at IS NULL;

-- name: InsertAPIKey :one
INSERT INTO api_keys (user_id, name, prefix, key_hash)
VALUES ($1, $2, $3, $4)
RETURNING id, user_id, name, prefix, key_hash, last_used_at, revoked_at, created_at;

-- name: GetAPIKeyByPrefix :one
SELECT k.id, k.user_id, k.name, k.prefix, k.key_hash, k.last_used_at, k.revoked_at, k.created_at, u.role
FROM api_keys k JOIN users u ON u.id = k.user_id
WHERE k.prefix = $1;

-- name: TouchAPIKey :exec
UPDATE api_keys SET last_used_at = now() WHERE id = $1;

-- name: ListAPIKeys :many
SELECT id, user_id, name, prefix, key_hash, last_used_at, revoked_at, created_at
FROM api_keys WHERE user_id = $1 ORDER BY created_at DESC;

-- name: RevokeAPIKey :execrows
UPDATE api_keys SET revoked_at = now()
WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL;
