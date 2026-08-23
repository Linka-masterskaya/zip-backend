-- +goose Up
ALTER TABLE tts_jobs ADD COLUMN org_id UUID REFERENCES organizations(id);
ALTER TABLE tts_jobs ADD COLUMN media_id UUID REFERENCES media_files(id);

DELETE FROM tts_jobs WHERE org_id IS NULL;

ALTER TABLE tts_jobs ALTER COLUMN org_id SET NOT NULL;

ALTER TABLE tts_jobs
    DROP COLUMN minio_key,
    DROP COLUMN sha256,
    DROP COLUMN size_bytes;

DROP INDEX tts_jobs_inflight_uniq;
CREATE UNIQUE INDEX tts_jobs_inflight_uniq ON tts_jobs(org_id, text, voice)
  WHERE status IN ('pending', 'in_progress');
ALTER TABLE tts_jobs ADD CONSTRAINT tts_jobs_succeeded_has_media
  CHECK (status != 'succeeded' OR media_id IS NOT NULL);

-- +goose Down
DROP INDEX tts_jobs_inflight_uniq;
CREATE UNIQUE INDEX tts_jobs_inflight_uniq ON tts_jobs(text, voice)
  WHERE status IN ('pending', 'in_progress');
ALTER TABLE tts_jobs DROP CONSTRAINT tts_jobs_succeeded_has_media;
ALTER TABLE tts_jobs
    ADD COLUMN minio_key TEXT,
    ADD COLUMN sha256 TEXT,
    ADD COLUMN size_bytes BIGINT;
ALTER TABLE tts_jobs DROP COLUMN media_id;
ALTER TABLE tts_jobs DROP COLUMN org_id;