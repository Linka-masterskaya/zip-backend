-- +goose Up
ALTER TABLE packs ADD COLUMN cover_source_picture_id UUID;

-- +goose Down
ALTER TABLE packs DROP cover_source_picture_id;