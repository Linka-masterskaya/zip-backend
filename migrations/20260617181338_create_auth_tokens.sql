-- +goose Up
CREATE TYPE token_purpose AS ENUM ('email_verify', 'password_reset', 'email_change');

CREATE TABLE auth_tokens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose     token_purpose NOT NULL,
    token_hash  TEXT NOT NULL,   -- SHA256 хеш токена
    payload     JSONB,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_auth_tokens_user_id ON auth_tokens(user_id);
CREATE INDEX idx_auth_tokens_token_hash ON auth_tokens(token_hash);
CREATE INDEX idx_auth_tokens_expires_at ON auth_tokens(expires_at);

-- +goose Down
DROP TABLE auth_tokens;
DROP TYPE token_purpose;
