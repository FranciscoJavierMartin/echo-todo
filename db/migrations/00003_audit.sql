-- +goose Up
CREATE TABLE todo_audit (
	id         bigserial PRIMARY KEY,
	todo_id    uuid NOT NULL REFERENCES todos(id) ON DELETE CASCADE,
	actor_id   uuid NOT NULL,
	action     text NOT NULL CHECK (action IN ('create', 'update', 'delete')),
	changes    jsonb NOT NULL DEFAULT '{}',
	created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX todo_audit_todo ON todo_audit (todo_id, id);

-- +goose Down
DROP TABLE todo_audit;
