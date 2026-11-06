-- name: CreateTodo :one
INSERT INTO todos (owner_id, title, notes, status, due_at, priority)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, owner_id, title, notes, status, due_at, priority, version, deleted_at, created_at, updated_at;

-- name: GetTodo :one
SELECT id, owner_id, title, notes, status, due_at, priority, version, deleted_at, created_at, updated_at
FROM todos
WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL;

-- name: GetTodoVersion :one
SELECT version FROM todos
WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL;

-- name: UpdateTodo :one
UPDATE todos
SET title = $4, notes = $5, status = $6, due_at = $7, priority = $8,
    version = version + 1, updated_at = now()
WHERE id = $1 AND owner_id = $2 AND version = $3 AND deleted_at IS NULL
RETURNING id, owner_id, title, notes, status, due_at, priority, version, deleted_at, created_at, updated_at;

-- name: SoftDeleteTodo :execrows
-- expected_version = 0 means "any version".
UPDATE todos
SET deleted_at = now(), version = version + 1, updated_at = now()
WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL
  AND (sqlc.arg(expected_version)::int = 0 OR version = sqlc.arg(expected_version)::int);

-- name: InsertAuditEvent :exec
INSERT INTO todo_audit (todo_id, actor_id, action, changes)
VALUES ($1, $2, $3, $4);

-- name: ListAuditEvents :many
SELECT id, todo_id, actor_id, action, changes, created_at
FROM todo_audit WHERE todo_id = $1 ORDER BY id;

-- name: TodoExistsAnyOwner :one
SELECT EXISTS (SELECT 1 FROM todos WHERE id = $1);
