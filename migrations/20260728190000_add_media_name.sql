-- +goose Up
ALTER TABLE media_files
    ADD COLUMN name TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE media_files
    DROP COLUMN name;
