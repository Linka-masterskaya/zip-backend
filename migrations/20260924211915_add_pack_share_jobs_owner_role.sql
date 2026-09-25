-- +goose Up
ALTER TABLE pack_share_jobs ADD COLUMN owner_role TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE pack_share_jobs DROP COLUMN owner_role;
