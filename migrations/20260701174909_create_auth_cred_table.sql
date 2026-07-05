-- +goose Up
-- +goose StatementBegin
CREATE TABLE auth_cred (
    user_id          UUID  PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    email_hash       BYTEA NOT NULL,
    email_encrypted  BYTEA NOT NULL,
    password_hash    TEXT,
    role             TEXT  NOT NULL CHECK (role IN ('defectologist','head_defectologist')),
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX auth_cred_email_hash_uniq ON auth_cred (email_hash);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE auth_cred;
-- +goose StatementEnd