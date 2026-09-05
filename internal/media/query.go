package media

const userOrgQuery = `
	SELECT org_id FROM users
	WHERE id = $1 AND org_id IS NOT NULL AND deleted_at IS NULL`

const lockOrgQuery = `
	SELECT id FROM organizations WHERE id = $1 FOR UPDATE`

const findByDigestQuery = `
	SELECT id, org_id, uploader_id, name, sha256, mime_type, media_type,
	       size_bytes, minio_key, created_at
	FROM media_files WHERE org_id = $1 AND sha256 = $2`

const reserveQuotaQuery = `
	UPDATE organizations
	SET storage_used_bytes = storage_used_bytes + $2
	WHERE id = $1
	  AND storage_used_bytes + $2 <= storage_quota_bytes
	RETURNING true`

const insertMediaQuery = `
	INSERT INTO media_files
		(org_id, uploader_id, name, sha256, mime_type, media_type, size_bytes, minio_key)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	RETURNING id, org_id, uploader_id, name, sha256, mime_type, media_type,
	          size_bytes, minio_key, created_at`

const getAccessibleMediaQuery = `
	SELECT m.id, m.org_id, m.uploader_id, m.name, m.sha256, m.mime_type, m.media_type,
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

const listMediaQuery = `
	SELECT id, org_id, uploader_id, name, sha256, mime_type, media_type,
	       size_bytes, minio_key, created_at
	FROM media_files
	WHERE org_id = $1
	  AND ($2::text = '' OR name ILIKE '%' || $2::text || '%')
	  AND ($3::text = '' OR media_type = $3::text)
	  AND ($4::timestamptz IS NULL OR (created_at, id) < ($4::timestamptz, $5::uuid))
	ORDER BY created_at DESC, id DESC
	LIMIT $6`

const lockMediaQuery = `
	SELECT m.id, m.org_id, m.uploader_id, m.name, m.sha256, m.mime_type, m.media_type,
	       m.size_bytes, m.minio_key, m.created_at
	FROM media_files m
	JOIN users u ON u.id = $1 AND u.org_id = m.org_id AND u.deleted_at IS NULL
	WHERE m.id = $2
	FOR UPDATE OF m, u`

const mediaInUseQuery = `
	SELECT EXISTS (SELECT 1 FROM media_usages WHERE media_id = $1)
			OR EXISTS (SELECT 1 FROM students WHERE avatar_media_id = $1 AND deleted_at IS NULL)
			OR EXISTS (SELECT 1 FROM tts_jobs WHERE media_id = $1)`

const deleteMediaQuery = `
	DELETE FROM media_files WHERE id = $1`

const releaseMediaQuotaQuery = `
	UPDATE organizations
	SET storage_used_bytes = GREATEST(storage_used_bytes - $2, 0)
	WHERE id = $1`
