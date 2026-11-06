-- +goose Up
-- Max lengths are enforced in the validator *and* here, so no client can bypass them.
ALTER TABLE users ADD CONSTRAINT users_email_length CHECK (length(email) <= 254);
ALTER TABLE refresh_tokens ADD CONSTRAINT refresh_tokens_user_agent_length CHECK (length(user_agent) <= 200);

-- +goose Down
ALTER TABLE refresh_tokens DROP CONSTRAINT refresh_tokens_user_agent_length;
ALTER TABLE users DROP CONSTRAINT users_email_length;
