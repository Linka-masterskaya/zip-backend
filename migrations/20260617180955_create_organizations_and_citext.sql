-- +goose Up
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE organizations (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                TEXT NOT NULL,
    storage_used_bytes  BIGINT NOT NULL DEFAULT 0,
    storage_quota_bytes BIGINT NOT NULL DEFAULT 10737418240,  -- 10 GiB
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE organizations;
DROP EXTENSION IF EXISTS citext;
