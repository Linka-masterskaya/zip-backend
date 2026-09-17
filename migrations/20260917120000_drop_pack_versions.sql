-- +goose Up
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
