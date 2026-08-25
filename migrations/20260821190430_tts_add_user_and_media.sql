-- +goose Up
ALTER TABLE tts_jobs ADD COLUMN org_id UUID REFERENCES organizations(id);
ALTER TABLE tts_jobs ADD COLUMN media_id UUID REFERENCES media_files(id) ON DELETE SET NULL;

ALTER TABLE tts_jobs
    DROP COLUMN minio_key,
    DROP COLUMN sha256,
    DROP COLUMN size_bytes;

DROP INDEX tts_jobs_inflight_uniq;
CREATE UNIQUE INDEX tts_jobs_inflight_uniq ON tts_jobs(org_id, text, voice)
  WHERE status IN ('pending', 'in_progress');

-- +goose Down
DROP INDEX tts_jobs_inflight_uniq;
CREATE UNIQUE INDEX tts_jobs_inflight_uniq ON tts_jobs(text, voice)
  WHERE status IN ('pending', 'in_progress');
ALTER TABLE tts_jobs
    ADD COLUMN minio_key TEXT,
    ADD COLUMN sha256 TEXT,
    ADD COLUMN size_bytes BIGINT;
ALTER TABLE tts_jobs DROP COLUMN media_id;
ALTER TABLE tts_jobs DROP COLUMN org_id;