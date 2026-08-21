-- +goose Up
ALTER TABLE users
    ADD COLUMN avatar_size_bytes BIGINT
    CHECK (avatar_size_bytes IS NULL OR avatar_size_bytes >= 0);

CREATE TABLE avatar_cleanup_jobs (
    id                BIGSERIAL PRIMARY KEY,
    object_key        VARCHAR(512) NOT NULL UNIQUE,
    org_id            UUID REFERENCES organizations(id) ON DELETE SET NULL,
    object_size_bytes BIGINT CHECK (object_size_bytes IS NULL OR object_size_bytes >= 0),
    quota_adjusted    BOOLEAN NOT NULL DEFAULT FALSE,
    attempts          INTEGER NOT NULL DEFAULT 0,
    next_attempt_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error        TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at      TIMESTAMPTZ
);

CREATE INDEX idx_avatar_cleanup_jobs_pending
    ON avatar_cleanup_jobs (next_attempt_at, id)
    WHERE completed_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS avatar_cleanup_jobs;
ALTER TABLE users DROP COLUMN IF EXISTS avatar_size_bytes;
