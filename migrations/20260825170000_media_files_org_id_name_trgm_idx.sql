-- +goose Up
CREATE EXTENSION IF NOT EXISTS btree_gin;

DROP INDEX IF EXISTS media_files_name_trgm_idx;

CREATE INDEX media_files_org_id_name_trgm_idx
    ON media_files USING gin (org_id, name gin_trgm_ops);

-- +goose Down
DROP INDEX IF EXISTS media_files_org_id_name_trgm_idx;

CREATE INDEX media_files_name_trgm_idx
    ON media_files USING gin (name gin_trgm_ops);
