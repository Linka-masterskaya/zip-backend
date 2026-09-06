-- +goose Up
CREATE TABLE storage_objects (
    key          TEXT PRIMARY KEY,
    size         BIGINT      NOT NULL CHECK (size >= 0),
    content_type TEXT        NOT NULL,
    sha256       TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX storage_objects_updated_at_idx ON storage_objects(updated_at);

-- Reaper looks up references by object key. media_files lost a key-leading
-- index when uniqueness became organization-scoped, and avatars also need a
-- direct lookup path.
CREATE INDEX media_files_minio_key_idx ON media_files(minio_key);
CREATE INDEX users_avatar_key_idx ON users(avatar_key) WHERE avatar_key IS NOT NULL;

-- Prefer media_files when a shared TTS object is referenced both from media
-- and from audio_bank: it contains the authoritative MIME type.
INSERT INTO storage_objects (key, size, content_type, sha256, created_at, updated_at)
SELECT DISTINCT ON (mf.minio_key)
       mf.minio_key,
       mf.size_bytes,
       mf.mime_type,
       mf.sha256,
       mf.created_at,
       mf.created_at
FROM media_files mf
ORDER BY mf.minio_key, mf.created_at ASC;

-- audio_bank predates storage_objects and does not persist content_type.
-- Existing TTS objects are MPEG audio in the currently supported flow.
INSERT INTO storage_objects (key, size, content_type, sha256, created_at, updated_at)
SELECT ab.minio_key,
       ab.size_bytes,
       'audio/mpeg',
       ab.sha256,
       ab.generated_at,
       GREATEST(ab.generated_at, ab.last_used_at)
FROM audio_bank ab
ON CONFLICT (key) DO NOTHING;

-- picture_bank_images predates object hashing and therefore has no SHA-256 to
-- backfill. Empty sha256 explicitly means "legacy hash unavailable"; every
-- object written after this migration is registered with a real SHA-256 by
-- storage.PutObject.
INSERT INTO storage_objects (key, size, content_type, sha256, created_at, updated_at)
SELECT pbi.minio_key,
       pbi.size_bytes,
       pbi.mime_type,
       '',
       pbi.created_at,
       pbi.created_at
FROM picture_bank_images pbi
ON CONFLICT (key) DO NOTHING;

-- +goose Down
DROP INDEX IF EXISTS users_avatar_key_idx;
DROP INDEX IF EXISTS media_files_minio_key_idx;
DROP INDEX IF EXISTS storage_objects_updated_at_idx;
DROP TABLE IF EXISTS storage_objects;
