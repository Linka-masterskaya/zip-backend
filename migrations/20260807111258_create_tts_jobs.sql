-- +goose Up
CREATE TABLE tts_jobs (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    text           TEXT NOT NULL,
    voice          TEXT NOT NULL,
    status         VARCHAR NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'in_progress', 'succeeded', 'failed')),
    minio_key      TEXT,
    sha256         TEXT,
    size_bytes     BIGINT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX tts_jobs_inflight_uniq ON tts_jobs (text, voice) WHERE status IN ('pending', 'in_progress');
CREATE INDEX tts_jobs_created_at_idx ON tts_jobs(created_at) WHERE status IN ('succeeded', 'failed');
-- +goose Down
DROP TABLE tts_jobs;