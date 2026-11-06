-- name: CreateUser :one
INSERT INTO users (email, password_hash, display_name, role)
VALUES ($1, $2, $3, $4)
RETURNING id, email, password_hash, display_name, role, created_at, updated_at;

-- name: GetUserByEmail :one
SELECT id, email, password_hash, display_name, role, created_at, updated_at
FROM users WHERE email = $1;

-- name: GetUserByID :one
SELECT id, email, password_hash, display_name, role, created_at, updated_at
FROM users WHERE id = $1;

-- name: UpdateUserPasswordHash :exec
UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1;
