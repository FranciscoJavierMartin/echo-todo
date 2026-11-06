-- +goose Up
-- Transactional outbox: rows are written in the same transaction as the change
-- they describe and drained by a worker (at-least-once delivery).
CREATE TABLE outbox (
	id           bigserial PRIMARY KEY,
	topic        text NOT NULL,
	payload      jsonb NOT NULL,
	attempts     integer NOT NULL DEFAULT 0,
	last_error   text,
	available_at timestamptz NOT NULL DEFAULT now(),
	processed_at timestamptz,
	created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX outbox_pending ON outbox (available_at, id) WHERE processed_at IS NULL;

-- +goose Down
DROP TABLE outbox;
