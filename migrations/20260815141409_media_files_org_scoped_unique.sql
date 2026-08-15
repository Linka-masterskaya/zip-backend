-- +goose Up
ALTER TABLE media_files DROP CONSTRAINT media_files_minio_key_key;
CREATE UNIQUE INDEX media_files_org_key_uniq ON media_files(org_id, minio_key);

-- +goose Down
DROP INDEX media_files_org_key_uniq;
ALTER TABLE media_files ADD CONSTRAINT media_files_minio_key_key UNIQUE (minio_key);