-- +goose Up
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX packs_title_trgm_idx
    ON packs USING gin (title gin_trgm_ops);

-- +goose Down
DROP INDEX IF EXISTS packs_title_trgm_idx;
