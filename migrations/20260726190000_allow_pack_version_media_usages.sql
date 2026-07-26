-- +goose Up
ALTER TABLE media_usages
    DROP CONSTRAINT media_usages_source_type_check,
    ADD CONSTRAINT media_usages_source_type_check
        CHECK (source_type IN ('pack', 'pack_adaptation', 'pack_version'));

-- +goose Down
DELETE FROM media_usages WHERE source_type = 'pack_version';

ALTER TABLE media_usages
    DROP CONSTRAINT media_usages_source_type_check,
    ADD CONSTRAINT media_usages_source_type_check
        CHECK (source_type IN ('pack', 'pack_adaptation'));
