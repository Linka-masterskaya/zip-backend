-- +goose Up
ALTER TABLE media_files DROP CONSTRAINT media_files_minio_key_key;
CREATE UNIQUE INDEX media_files_org_key_uniq ON media_files(org_id, minio_key);

-- +goose Down
-- WARNING: rollback fails if TTS deduplication has created rows with
-- the same minio_key across different orgs. Before running Down, manually
-- resolve duplicates:
--   SELECT minio_key, array_agg(id), array_agg(org_id)
--   FROM media_files GROUP BY minio_key HAVING count(*) > 1;
-- Deleting duplicates loses user references (audio remains in MinIO but
-- unreferenced) and does not restore storage_used_bytes — coordinate
-- with data cleanup before rollback.
DROP INDEX media_files_org_key_uniq;
ALTER TABLE media_files ADD CONSTRAINT media_files_minio_key_key UNIQUE (minio_key);