-- +goose Up
-- +goose StatementBegin
CREATE TABLE verify_tokens (
    id          UUID        PRIMARY KEY,
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  BYTEA       NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX verify_tokens_token_hash_uniq ON verify_tokens (token_hash);
CREATE INDEX        verify_tokens_user_active_idx ON verify_tokens (user_id) WHERE used_at IS NULL;
CREATE INDEX        verify_tokens_expires_at_idx  ON verify_tokens (expires_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE verify_tokens;
-- +goose StatementEnd