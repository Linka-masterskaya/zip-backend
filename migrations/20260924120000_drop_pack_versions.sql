-- +goose Up
DELETE FROM media_usages WHERE source_type = 'pack_version';

ALTER TABLE media_usages 
    DROP CONSTRAINT IF EXISTS media_usages_source_type_check,
    ADD CONSTRAINT media_usages_source_type_check 
        CHECK (source_type IN ('pack', 'pack_adaptation'));

DROP TABLE IF EXISTS pack_versions;

-- +goose Down
ALTER TABLE media_usages 
    DROP CONSTRAINT IF EXISTS media_usages_source_type_check,
    ADD CONSTRAINT media_usages_source_type_check 
        CHECK (source_type IN ('pack', 'pack_adaptation', 'pack_version'));

-- Воссоздаем таблицу для совместимости с циклом отката миграций.
-- Данные не восстанавливаются: таблица создаётся пустой, удалённые usages не возвращаются.
CREATE TABLE pack_versions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pack_id     UUID NOT NULL REFERENCES packs(id) ON DELETE CASCADE,
    version     INT NOT NULL,
    config      JSONB NOT NULL,
    created_by  UUID NOT NULL REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(pack_id, version)
);
