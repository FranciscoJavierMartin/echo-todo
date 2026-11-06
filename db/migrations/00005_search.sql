-- +goose Up
ALTER TABLE todos ADD COLUMN search tsvector GENERATED ALWAYS AS (
	setweight(to_tsvector('english', coalesce(title, '')), 'A') ||
	setweight(to_tsvector('english', coalesce(notes, '')), 'B')
) STORED;
CREATE INDEX todos_search ON todos USING gin (search);

-- Keyset pagination indexes for the common sorts (owner-scoped, live rows only).
CREATE INDEX todos_owner_created ON todos (owner_id, created_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX todos_owner_priority_due ON todos (owner_id, priority, (coalesce(due_at, 'infinity'::timestamptz)), id) WHERE deleted_at IS NULL;

-- +goose Down
DROP INDEX todos_owner_priority_due;
DROP INDEX todos_owner_created;
DROP INDEX todos_search;
ALTER TABLE todos DROP COLUMN search;
