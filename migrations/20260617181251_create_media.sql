-- +goose Up
CREATE TABLE media_files (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          UUID NOT NULL REFERENCES organizations(id),
    uploader_id     UUID NOT NULL REFERENCES users(id),
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'ready', 'failed')),
    source_text     TEXT,
    sha256          TEXT,
    mime_type       TEXT,
    size_bytes      BIGINT,
    minio_key       TEXT UNIQUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(org_id, sha256)
);

-- +goose Down
DROP TABLE media_files;
