-- +goose Up
CREATE TABLE attachments (
	id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
	todo_id      uuid NOT NULL REFERENCES todos(id) ON DELETE CASCADE,
	owner_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	filename     text NOT NULL CHECK (length(filename) BETWEEN 1 AND 255),
	content_type text NOT NULL,
	size_bytes   bigint NOT NULL CHECK (size_bytes >= 0),
	sha256       bytea NOT NULL,
	storage_key  text NOT NULL UNIQUE,
	created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX attachments_todo ON attachments (todo_id, created_at);

-- +goose Down
DROP TABLE attachments;
