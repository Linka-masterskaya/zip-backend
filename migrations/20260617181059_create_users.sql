-- +goose Up
CREATE TYPE user_role AS ENUM ('defectologist', 'parent', 'viewer', 'admin');

CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          UUID REFERENCES organizations(id),
    email           CITEXT UNIQUE NOT NULL,
    password_hash   TEXT,
    role            user_role NOT NULL DEFAULT 'viewer',
    yandex_id       TEXT UNIQUE,
    display_name    TEXT,
    avatar_key      TEXT,
    locale          TEXT DEFAULT 'ru',
    email_verified  BOOLEAN DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE users;
DROP TYPE user_role;
