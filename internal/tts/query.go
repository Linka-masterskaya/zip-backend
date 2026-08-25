package tts

const createSucceededJob = `
INSERT INTO tts_jobs(org_id, text, voice, status, media_id)
VALUES($1, $2, $3, 'succeeded', $4)
RETURNING id`

const completeJob = `
UPDATE tts_jobs
SET status='succeeded', media_id=$2, updated_at=NOW()
WHERE id = $1`

const updateStatusTTS = `
UPDATE tts_jobs
SET status=$2, updated_at=NOW()
WHERE id = $1`

const insertJobQuery = `
INSERT INTO tts_jobs (org_id, text, voice, status)
VALUES ($1, $2, $3, 'pending')
ON CONFLICT (org_id, text, voice) WHERE status IN ('pending', 'in_progress') DO NOTHING
RETURNING id`

const findInflightJobQuery = `
SELECT id FROM tts_jobs
WHERE org_id = $1 AND text = $2 AND voice = $3 AND status IN ('pending', 'in_progress')`

const getFromBankQuery = `
UPDATE audio_bank
SET last_used_at = now()
WHERE text = $1 AND voice = $2
RETURNING text, voice, minio_key, sha256, size_bytes`

const putToBank = `
INSERT INTO audio_bank
(minio_key, text, voice, sha256, size_bytes)
VALUES($1, $2, $3, $4, $5)
ON CONFLICT (text, voice) DO NOTHING`

const getJob = `
SELECT status, media_id
FROM tts_jobs
WHERE id=$1`

const insertMediaFromTTS = `
WITH ins AS (
    INSERT INTO media_files (org_id, uploader_id, sha256, mime_type, size_bytes, minio_key, name, media_type)
    VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
    ON CONFLICT (org_id, minio_key) DO NOTHING
    RETURNING id
)
SELECT id FROM ins
UNION ALL
SELECT id FROM media_files WHERE minio_key = $6 AND org_id = $1
LIMIT 1`

const updateOrgQuota = `
UPDATE organizations 
	SET storage_used_bytes = storage_used_bytes + $2 
	WHERE id = $1 
	AND storage_used_bytes + $2 <= storage_quota_bytes 
	RETURNING true`

const upsertCache = `
	INSERT INTO app_cache (key, data, updated_at)
	VALUES ($1, $2, NOW())
	ON CONFLICT (key) DO UPDATE
	SET data = $2, updated_at = NOW()`

const getCache = `
	SELECT data FROM app_cache WHERE key = $1`

const deleteOldJobs = `
	DELETE FROM tts_jobs
	WHERE status IN ('succeeded', 'failed')
	AND created_at < $1`

const deleteFromBank = `
	DELETE FROM audio_bank
	WHERE minio_key = ANY($1)`

const selectExpiredBank = `
	SELECT ab.minio_key
	FROM audio_bank ab
	LEFT JOIN media_files mf ON mf.minio_key = ab.minio_key
	WHERE mf.id IS NULL
	AND ab.last_used_at < $1
	LIMIT $2`

const isQuotaLow = `
SELECT storage_quota_bytes - storage_used_bytes < 1048576
FROM organizations
WHERE id = $1`
