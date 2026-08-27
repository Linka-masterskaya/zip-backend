-- +goose Up
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE picture_bank_images (
    id          UUID PRIMARY KEY,
    category    TEXT NOT NULL CHECK (btrim(category) <> ''),
    title       TEXT NOT NULL CHECK (btrim(title) <> ''),
    mime_type   TEXT NOT NULL CHECK (mime_type IN ('image/png', 'image/jpeg', 'image/webp', 'image/gif')),
    size_bytes  BIGINT NOT NULL CHECK (size_bytes > 0),
    minio_key   TEXT NOT NULL UNIQUE CHECK (minio_key LIKE 'system/pictures-bank/%'),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX picture_bank_images_category_idx
    ON picture_bank_images (category);

CREATE INDEX picture_bank_images_title_trgm_idx
    ON picture_bank_images USING gin (title gin_trgm_ops);

-- +goose Down
DROP INDEX IF EXISTS picture_bank_images_title_trgm_idx;
DROP INDEX IF EXISTS picture_bank_images_category_idx;
DROP TABLE IF EXISTS picture_bank_images;
