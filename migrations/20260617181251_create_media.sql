-- +goose Up
CREATE TABLE media_files (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          UUID NOT NULL REFERENCES organizations(id),
    uploader_id     UUID NOT NULL REFERENCES users(id),
    sha256          TEXT NOT NULL,
    mime_type       TEXT NOT NULL,
    size_bytes      BIGINT NOT NULL,
    minio_key       TEXT NOT NULL UNIQUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(org_id, sha256)
);

-- +goose Down
DROP TABLE media_files;
