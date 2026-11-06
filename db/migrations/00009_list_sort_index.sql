-- +goose Up
-- Found with EXPLAIN (ANALYZE, BUFFERS) under load (docs/performance.md): the
-- default dashboard sort "-priority,due_at" filtered by status could not use
-- todos_owner_priority_due (all ASC), so Postgres scanned and re-sorted every row
-- of the top priority group. This index matches the mixed directions exactly.
CREATE INDEX todos_owner_status_prio_desc_due ON todos
	(owner_id, status, priority DESC, (coalesce(due_at, 'infinity'::timestamptz)) ASC, id ASC)
	WHERE deleted_at IS NULL;

-- +goose Down
DROP INDEX todos_owner_status_prio_desc_due;
