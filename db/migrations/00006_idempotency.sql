-- +goose Up
CREATE TABLE idempotency_keys (
	user_id          uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	key              text NOT NULL CHECK (length(key) BETWEEN 1 AND 255),
	request_hash     bytea NOT NULL,
	state            text NOT NULL DEFAULT 'in_progress' CHECK (state IN ('in_progress', 'completed')),
	status           integer,
	response_headers jsonb NOT NULL DEFAULT '{}',
	response_body    bytea,
	created_at       timestamptz NOT NULL DEFAULT now(),
	expires_at       timestamptz NOT NULL,
	PRIMARY KEY (user_id, key)
);
CREATE INDEX idempotency_keys_expires ON idempotency_keys (expires_at);

-- +goose Down
DROP TABLE idempotency_keys;
