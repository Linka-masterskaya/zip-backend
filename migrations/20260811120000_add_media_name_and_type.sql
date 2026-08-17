-- +goose Up
ALTER TABLE media_files
    ADD COLUMN name TEXT,
    ADD COLUMN media_type TEXT;

UPDATE media_files
SET name =  '',
    media_type = split_part(mime_type, '/', 1)
WHERE name IS NULL OR media_type IS NULL;

ALTER TABLE media_files
    ALTER COLUMN name SET NOT NULL,
    ALTER COLUMN media_type SET NOT NULL;

CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX media_files_name_trgm_idx
    ON media_files USING gin (name gin_trgm_ops);

CREATE INDEX media_files_media_type_idx
    ON media_files (media_type);

-- +goose Down
DROP INDEX IF EXISTS media_files_media_type_idx;
DROP INDEX IF EXISTS media_files_name_trgm_idx;

ALTER TABLE media_files
    DROP COLUMN media_type,
    DROP COLUMN name;
