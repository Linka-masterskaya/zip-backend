-- +goose Up
-- +goose StatementBegin
CREATE TABLE auth_identities (
    id            UUID         PRIMARY KEY,
    user_id       UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider      VARCHAR(32)  NOT NULL CHECK (provider IN ('yandex')),
    provider_uid  VARCHAR(255) NOT NULL,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (provider, provider_uid)
);
CREATE INDEX auth_identities_user_id_idx ON auth_identities (user_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE auth_identities;
-- +goose StatementEnd