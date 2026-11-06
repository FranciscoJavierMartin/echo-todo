-- +goose Up
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TYPE todo_status AS ENUM ('open', 'done', 'archived');

CREATE TABLE users (
	id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
	email         citext NOT NULL UNIQUE,
	password_hash text NOT NULL,
	display_name  text NOT NULL CHECK (length(display_name) BETWEEN 1 AND 80),
	role          text NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin')),
	created_at    timestamptz NOT NULL DEFAULT now(),
	updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE todos (
	id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
	owner_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	title       text NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
	notes       text NOT NULL DEFAULT '' CHECK (length(notes) <= 10000),
	status      todo_status NOT NULL DEFAULT 'open',
	due_at      timestamptz,
	priority    smallint NOT NULL DEFAULT 3 CHECK (priority BETWEEN 1 AND 5),
	version     integer NOT NULL DEFAULT 1,
	deleted_at  timestamptz,
	created_at  timestamptz NOT NULL DEFAULT now(),
	updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX todos_owner_status_due ON todos (owner_id, status, due_at) WHERE deleted_at IS NULL;

-- +goose Down
DROP TABLE todos;
DROP TABLE users;
DROP TYPE todo_status;
