-- +goose Up
ALTER TABLE media_files DROP CONSTRAINT media_files_org_id_sha256_key;

-- +goose Down
-- WARNING: rollback fails if duplicate (org_id, sha256) rows were created
-- while this constraint was absent. Before running Down, manually resolve
-- duplicates:
--   SELECT org_id, sha256, array_agg(id)
--   FROM media_files GROUP BY org_id, sha256 HAVING count(*) > 1;
-- Merging duplicates loses per-row references and does not adjust
-- storage_used_bytes — coordinate with data cleanup before rollback.
ALTER TABLE media_files ADD CONSTRAINT media_files_org_id_sha256_key UNIQUE (org_id, sha256);
