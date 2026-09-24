-- +goose Up
-- Recordings can now reuse an already-uploaded minio_key within the same org, so
-- (org_id, minio_key) is no longer unique.
DROP INDEX media_files_org_key_uniq;
CREATE INDEX media_files_org_key_idx ON media_files(org_id, minio_key);

-- +goose Down
-- WARNING: rollback fails if duplicate (org_id, minio_key) rows were created
-- while the unique index was absent. Before running Down, manually resolve
-- duplicates:
--   SELECT org_id, minio_key, array_agg(id)
--   FROM media_files GROUP BY org_id, minio_key HAVING count(*) > 1;
-- Merging duplicates loses per-row references and does not adjust
-- storage_used_bytes — coordinate with data cleanup before rollback.
DROP INDEX media_files_org_key_idx;
CREATE UNIQUE INDEX media_files_org_key_uniq ON media_files(org_id, minio_key);
