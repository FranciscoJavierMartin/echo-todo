-- +goose Up
CREATE TABLE refresh_tokens (
	id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
	user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	family_id   uuid NOT NULL,
	token_hash  bytea NOT NULL UNIQUE,
	expires_at  timestamptz NOT NULL,
	revoked_at  timestamptz,
	replaced_by uuid REFERENCES refresh_tokens(id) ON DELETE SET NULL,
	user_agent  text NOT NULL DEFAULT '',
	created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX refresh_tokens_family ON refresh_tokens (family_id);
CREATE INDEX refresh_tokens_user_active ON refresh_tokens (user_id) WHERE revoked_at IS NULL;

CREATE TABLE api_keys (
	id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
	user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	name         text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
	prefix       text NOT NULL UNIQUE,
	key_hash     bytea NOT NULL,
	last_used_at timestamptz,
	revoked_at   timestamptz,
	created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX api_keys_user ON api_keys (user_id);

-- +goose Down
DROP TABLE api_keys;
DROP TABLE refresh_tokens;
