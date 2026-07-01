-- +goose Up
-- +goose StatementBegin
CREATE TABLE users (
    id               UUID         PRIMARY KEY,
    email_verified   BOOLEAN      NOT NULL DEFAULT FALSE,
    name             VARCHAR(255) NOT NULL,
    avatar_key       VARCHAR(512),
    organization_id  UUID,
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deleted_at       TIMESTAMPTZ
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE users;
-- +goose StatementEnd