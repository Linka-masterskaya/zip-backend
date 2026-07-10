-- +goose Up
-- +goose StatementBegin
CREATE TABLE verify_tokens (
    id          UUID        PRIMARY KEY,
    user_id     UUID        REFERENCES users(id) ON DELETE CASCADE,
    student_id  UUID        REFERENCES students(id) ON DELETE CASCADE,
    purpose     TEXT        NOT NULL CHECK (purpose IN ('email_verify','password_reset','email_change')),
    token_hash  BYTEA       NOT NULL,
    payload     BYTEA,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT verify_tokens_one_owner_check CHECK (
        (user_id IS NOT NULL AND student_id IS NULL) OR
        (user_id IS NULL AND student_id IS NOT NULL)
    )
);

CREATE UNIQUE INDEX verify_tokens_token_hash_uniq ON verify_tokens (token_hash);
CREATE INDEX        verify_tokens_user_active_idx ON verify_tokens (user_id) WHERE used_at IS NULL;
CREATE INDEX        verify_tokens_student_active_idx ON verify_tokens (student_id) WHERE used_at IS NULL;
CREATE INDEX        verify_tokens_expires_at_idx ON verify_tokens (expires_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE verify_tokens;
-- +goose StatementEnd