package media

const userOrgQuery = `
	SELECT org_id FROM users
	WHERE id = $1 AND org_id IS NOT NULL AND deleted_at IS NULL`

const lockOrgQuery = `
	SELECT id FROM organizations WHERE id = $1 FOR UPDATE`

const findByDigestQuery = `
	SELECT id, org_id, uploader_id, sha256, mime_type, size_bytes, minio_key, created_at
	FROM media_files WHERE org_id = $1 AND sha256 = $2`

const reserveQuotaQuery = `
	UPDATE organizations
	SET storage_used_bytes = storage_used_bytes + $2
	WHERE id = $1
	  AND storage_used_bytes + $2 <= storage_quota_bytes
	RETURNING true`

const insertMediaQuery = `
	INSERT INTO media_files
		(org_id, uploader_id, sha256, mime_type, size_bytes, minio_key)
	VALUES ($1, $2, $3, $4, $5, $6)
	RETURNING id, org_id, uploader_id, sha256, mime_type, size_bytes, minio_key, created_at`

const getAccessibleMediaQuery = `
	SELECT m.id, m.org_id, m.uploader_id, m.sha256, m.mime_type,
	       m.size_bytes, m.minio_key, m.created_at
	FROM media_files m
	JOIN users u ON u.id = $1 AND u.deleted_at IS NULL
	WHERE m.id = $2
	  AND (
	    m.org_id = u.org_id
	    OR EXISTS (
	      SELECT 1 FROM media_usages mu
	      JOIN packs p ON mu.source_type = 'pack' AND mu.source_id = p.id
	      WHERE mu.media_id = m.id AND p.published_at IS NOT NULL
	    )
	  )`
