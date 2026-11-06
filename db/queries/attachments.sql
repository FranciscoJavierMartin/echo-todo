-- name: InsertAttachment :one
INSERT INTO attachments (todo_id, owner_id, filename, content_type, size_bytes, sha256, storage_key)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, todo_id, owner_id, filename, content_type, size_bytes, sha256, storage_key, created_at;

-- name: GetAttachment :one
SELECT a.id, a.todo_id, a.owner_id, a.filename, a.content_type, a.size_bytes, a.sha256, a.storage_key, a.created_at
FROM attachments a JOIN todos t ON t.id = a.todo_id
WHERE a.id = $1 AND a.todo_id = $2 AND a.owner_id = $3 AND t.deleted_at IS NULL;

-- name: ListAttachments :many
SELECT a.id, a.todo_id, a.owner_id, a.filename, a.content_type, a.size_bytes, a.sha256, a.storage_key, a.created_at
FROM attachments a JOIN todos t ON t.id = a.todo_id
WHERE a.todo_id = $1 AND a.owner_id = $2 AND t.deleted_at IS NULL
ORDER BY a.created_at, a.id;

-- name: DeleteAttachment :one
DELETE FROM attachments WHERE id = $1 AND todo_id = $2 AND owner_id = $3
RETURNING storage_key;
