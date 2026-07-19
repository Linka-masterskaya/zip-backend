-- +goose Up
CREATE TABLE media_usages (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    media_id     UUID NOT NULL REFERENCES media_files(id) ON DELETE RESTRICT,
    source_type  TEXT NOT NULL CHECK (source_type IN ('pack', 'pack_adaptation')),
    source_id    UUID NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(media_id, source_type, source_id)
);

CREATE INDEX idx_media_usages_source ON media_usages(source_type, source_id);

-- +goose Down
DROP TABLE media_usages;
