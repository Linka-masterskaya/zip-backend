-- +goose Up
-- +goose StatementBegin
CREATE TABLE users (
    id               UUID         PRIMARY KEY,
    email_verified   BOOLEAN      NOT NULL DEFAULT FALSE,
    display_name     VARCHAR(255),
    avatar_key       VARCHAR(512),
    org_id           UUID REFERENCES organizations(id),
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deleted_at       TIMESTAMPTZ
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE users;
-- +goose StatementEnd