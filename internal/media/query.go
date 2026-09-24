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

// mediaReferencedPredicate is the single source of truth for whether a media file
// is occupied. It is shared by list/unused, single delete and batch delete so the
// three paths cannot drift apart.
//
// An archived student does not keep avatar media referenced because archived
// students cannot be restored through the API. TTS media is referenced only while
// the job is pending or in progress; terminal jobs release the media.
const mediaReferencedPredicate = `
	    EXISTS (SELECT 1 FROM media_usages mu WHERE mu.media_id = media_files.id)
	    OR EXISTS (
	      SELECT 1 FROM students s
	      WHERE s.avatar_media_id = media_files.id AND s.deleted_at IS NULL
	    )
	    OR EXISTS (SELECT 1 FROM tts_jobs j
				WHERE j.media_id = media_files.id
				AND j.status IN ('pending', 'in_progress'))`

const mediaUnreferencedPredicate = `NOT (` + mediaReferencedPredicate + `)`

const listMediaQuery = `
	SELECT id, org_id, uploader_id, name, sha256, mime_type, media_type,
	       size_bytes, minio_key, created_at,
				 (` + mediaUnreferencedPredicate + `) AS can_delete
	FROM media_files
	WHERE org_id = $1
	  AND ($2::text = '' OR name ILIKE '%' || $2::text || '%')
	  AND ($3::text = '' OR media_type = $3::text)
	  AND ($4::timestamptz IS NULL OR (created_at, id) < ($4::timestamptz, $5::uuid))
	  AND (
	    NOT $6::boolean
	    OR ` + mediaUnreferencedPredicate + `
	  )
	ORDER BY created_at DESC, id DESC
	LIMIT $7`

const countMediaQuery = `
	SELECT count(*)
	FROM media_files
	WHERE org_id = $1
	  AND ($2::text = '' OR name ILIKE '%' || $2::text || '%')
	  AND ($3::text = '' OR media_type = $3::text)
	  AND (
	    NOT $4::boolean
	    OR ` + mediaUnreferencedPredicate + `
	  )`

const lockMediaQuery = `
	SELECT m.id, m.org_id, m.uploader_id, m.name, m.sha256, m.mime_type, m.media_type,
	       m.size_bytes, m.minio_key, m.created_at
	FROM media_files m
	JOIN users u ON u.id = $1 AND u.org_id = m.org_id AND u.deleted_at IS NULL
	WHERE m.id = $2
	FOR UPDATE OF m, u`

const lockMediaBatchQuery = `
	SELECT m.id
	FROM media_files m
	JOIN users u ON u.id = $1 AND u.org_id = m.org_id AND u.deleted_at IS NULL
	WHERE m.id = ANY($2::uuid[])
	ORDER BY m.id
	FOR UPDATE OF m, u`

const mediaInUseQuery = `
	SELECT (` + mediaReferencedPredicate + `)
	FROM media_files
	WHERE id = $1`

const referencedMediaBatchQuery = `
	SELECT id FROM media_files
	WHERE id = ANY($1::uuid[])
	  AND (` + mediaReferencedPredicate + `)`

const deleteMediaQuery = `
	DELETE FROM media_files WHERE id = $1`

const deleteMediaBatchQuery = `
	WITH deleted AS (
		DELETE FROM media_files
		WHERE id = ANY($1::uuid[])
		RETURNING org_id, minio_key, size_bytes
	), released AS (
		SELECT DISTINCT ON (d.org_id, d.minio_key) d.org_id, d.size_bytes
		FROM deleted d
		WHERE NOT EXISTS (
			SELECT 1
			FROM media_files mf2
			WHERE mf2.org_id = d.org_id
			  AND mf2.minio_key = d.minio_key
			  AND mf2.id != ALL($1::uuid[])
		)
	), updated AS (
		UPDATE organizations o
		SET storage_used_bytes = GREATEST(o.storage_used_bytes - r.total_bytes, 0)
		FROM (
			SELECT org_id, SUM(size_bytes) AS total_bytes
			FROM released
			GROUP BY org_id
		) r
		WHERE o.id = r.org_id
	)
	SELECT COALESCE(SUM(size_bytes), 0) FROM released`

const previewMediaBatchFreedBytesQuery = `
	SELECT COALESCE(SUM(x.size_bytes), 0)
	FROM (
		SELECT DISTINCT ON (mf.org_id, mf.minio_key) mf.org_id, mf.minio_key, mf.size_bytes
		FROM media_files mf
		WHERE mf.id = ANY($1::uuid[])
	) x
	WHERE NOT EXISTS (
		SELECT 1
		FROM media_files mf2
		WHERE mf2.org_id = x.org_id
		  AND mf2.minio_key = x.minio_key
		  AND mf2.id != ALL($1::uuid[])
	)`

const releaseMediaQuotaQuery = `
	UPDATE organizations
	SET storage_used_bytes = GREATEST(storage_used_bytes - $2, 0)
	WHERE id = $1`
