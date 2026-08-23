package pack

const createPackQuery = `
	INSERT INTO packs (org_id, owner_id, folder_id, title, config)
	SELECT u.org_id, u.id, f.id, $3, $4
	FROM users u
	JOIN folders f ON f.id = $2
		AND f.owner_id = u.id
		AND f.org_id = u.org_id
		AND f.section IN ('my', 'students')
	WHERE u.id = $1
	  AND u.org_id IS NOT NULL
	  AND u.deleted_at IS NULL
	RETURNING ` + packColumns

const lockDuplicateSourceQuery = `
	SELECT ` + qualifiedPackColumns + `
	FROM packs p
	JOIN users u ON u.id = $1
	WHERE p.id = $2
	  AND u.org_id IS NOT NULL
	  AND u.deleted_at IS NULL
	  AND p.org_id = u.org_id
	  AND (p.owner_id = u.id OR p.published_at IS NOT NULL)
	FOR SHARE OF p`

const lockDuplicateFolderQuery = `
	SELECT f.id
	FROM folders f
	JOIN users u ON u.id = $1
	WHERE f.id = $2
	  AND f.owner_id = u.id
	  AND f.org_id = u.org_id
	  AND f.org_id = $3
	  AND f.section IN ('my', 'students')
	  AND u.org_id IS NOT NULL
	  AND u.deleted_at IS NULL
	FOR SHARE OF f`

const insertDuplicatePackQuery = `
	INSERT INTO packs (
		org_id, owner_id, folder_id, title,
		age_min, age_max, difficulty, goals, notes, config
	)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	RETURNING ` + packColumns

const copyDuplicateMediaUsagesQuery = `
	INSERT INTO media_usages (media_id, source_type, source_id)
	SELECT media_id, 'pack', $2
	FROM media_usages
	WHERE source_type = 'pack' AND source_id = $1`

const getPackQuery = `
	SELECT ` + qualifiedPackColumns + `
	FROM packs p
	JOIN users u ON u.id = $1
	WHERE p.id = $2
	  AND p.org_id = u.org_id
	  AND (p.owner_id = u.id OR p.published_at IS NOT NULL)
	  AND u.deleted_at IS NULL`

const getPackForPublicationQuery = `
	SELECT ` + qualifiedPackColumns + `
	FROM packs p
	JOIN users u ON u.id = $1
	WHERE p.id = $2
	  AND p.org_id = u.org_id
	  AND u.org_id IS NOT NULL
	  AND u.deleted_at IS NULL
	  AND (p.owner_id = u.id OR $3)`

const listPacksQuery = `
	WITH active_user AS (
		SELECT id, org_id
		FROM users
		WHERE id = $1
		  AND org_id IS NOT NULL
		  AND deleted_at IS NULL
	), placements AS (
		SELECT p.id, p.org_id, p.owner_id, p.folder_id AS result_folder_id,
		       p.library_folder_id, p.published_at, p.title, p.status,
		       p.age_min, p.age_max, p.difficulty, p.goals, p.notes, p.config,
		       p.created_at, p.updated_at, f.section
		FROM active_user u
		JOIN packs p ON p.owner_id = u.id AND p.org_id = u.org_id
		JOIN folders f ON f.id = p.folder_id
		              AND f.owner_id = u.id
		              AND f.org_id = u.org_id
		              AND f.section IN ('my', 'students')
		UNION ALL
		SELECT p.id, p.org_id, p.owner_id, student_folder.id AS result_folder_id,
		       p.library_folder_id, p.published_at, p.title, p.status,
		       p.age_min, p.age_max, p.difficulty, p.goals, p.notes, p.config,
		       p.created_at, p.updated_at, student_folder.section
		FROM active_user u
		JOIN students s ON s.defectologist_id = u.id
		               AND s.deleted_at IS NULL
		JOIN folders student_folder ON student_folder.student_id = s.id
		                           AND student_folder.owner_id = u.id
		                           AND student_folder.org_id = u.org_id
		                           AND student_folder.section = 'students'
		                           AND student_folder.kind = 'student'
		JOIN pack_adaptations pa ON pa.student_id = s.id
		                        AND pa.created_by = u.id
		JOIN packs p ON p.id = pa.pack_id
		            AND p.owner_id = u.id
		            AND p.org_id = u.org_id
		WHERE p.folder_id <> student_folder.id
		UNION ALL
		SELECT p.id, p.org_id, p.owner_id, p.library_folder_id AS result_folder_id,
		       p.library_folder_id, p.published_at, p.title, p.status,
		       p.age_min, p.age_max, p.difficulty, p.goals, p.notes, p.config,
		       p.created_at, p.updated_at, f.section
		FROM active_user u
		JOIN packs p ON p.org_id = u.org_id
		            AND p.published_at IS NOT NULL
		JOIN folders f ON f.id = p.library_folder_id
		              AND f.org_id = u.org_id
		              AND f.section = 'library'
	), filtered AS (
		SELECT placements.*,
		       EXISTS (
			   SELECT 1
			   FROM favorite_packs fp
			   WHERE fp.user_id = $1 AND fp.pack_id = placements.id
		       ) AS is_favorite
		FROM placements
		WHERE ($2::text = '' OR title ILIKE '%' || $2::text || '%')
		  AND ($3::int IS NULL OR (age_min <= $3::int AND $3::int <= age_max))
		  AND ($4::text = '' OR difficulty = $4::text)
		  AND ($5::text = '' OR section = $5::text)
	)
	SELECT id, org_id, owner_id, result_folder_id, library_folder_id,
	       published_at, title, status, age_min, age_max, difficulty,
	       goals, notes, config, is_favorite, section, created_at, updated_at
	FROM filtered
	ORDER BY updated_at DESC, id, section, result_folder_id
	LIMIT $6 OFFSET $7`

const lockPackForUpdateQuery = `
	SELECT p.org_id
	FROM packs p
	JOIN users u ON u.id = $1
	WHERE p.id = $2
	  AND p.owner_id = u.id
	  AND p.org_id = u.org_id
	  AND u.deleted_at IS NULL
	FOR UPDATE OF p, u`

const lockUpdateFolderQuery = `
	SELECT id
	FROM folders
	WHERE id = $2
	  AND owner_id = $1
	  AND org_id = $3
	  AND section IN ('my', 'students')
	FOR UPDATE`

const updatePackQuery = `
	UPDATE packs p
	SET title = COALESCE($3::text, p.title),
	    folder_id = COALESCE($4::uuid, p.folder_id),
	    age_min = CASE WHEN $5::boolean THEN $6::int ELSE p.age_min END,
	    age_max = CASE WHEN $7::boolean THEN $8::int ELSE p.age_max END,
	    difficulty = CASE WHEN $9::boolean THEN $10::text ELSE p.difficulty END,
	    goals = COALESCE($11::text[], p.goals),
	    notes = CASE WHEN $12::boolean THEN COALESCE($13::text, '') ELSE p.notes END,
	    updated_at = now()
	WHERE p.id = $2
	  AND p.owner_id = $1
	RETURNING ` + qualifiedPackColumns

const deletePackQuery = `
	DELETE FROM packs WHERE id = $2 AND owner_id = $1`

const lockPackForDeleteQuery = `
	SELECT p.published_at IS NOT NULL
	FROM packs p
	JOIN users u ON u.id = $1
	WHERE p.id = $2
	  AND p.owner_id = u.id
	  AND p.org_id = u.org_id
	  AND u.deleted_at IS NULL
	FOR UPDATE OF p, u`

const movePackQuery = `
	UPDATE packs p
	SET folder_id = f.id, updated_at = now()
	FROM users u, folders f
	WHERE p.id = $2
	  AND u.id = $1
	  AND f.id = $3
	  AND f.owner_id = u.id
	  AND f.org_id = u.org_id
	  AND f.section IN ('my', 'students')
	  AND p.owner_id = u.id
	  AND p.org_id = u.org_id
	  AND u.deleted_at IS NULL
	RETURNING ` + qualifiedPackColumns

const publishPackQuery = `
	UPDATE packs p
	SET library_folder_id = f.id,
	    published_at = COALESCE(p.published_at, now()),
	    status = 'published',
	    updated_at = now()
	FROM users u, folders f
	WHERE p.id = $2
	  AND u.id = $1
	  AND u.deleted_at IS NULL
	  AND u.org_id IS NOT NULL
	  AND p.org_id = u.org_id
	  AND f.id = $3
	  AND f.org_id = u.org_id
	  AND f.section = 'library'
	  AND (f.owner_id = u.id OR $4)
	  AND (p.owner_id = u.id OR $4)
	  AND (p.library_folder_id IS NULL OR p.library_folder_id = f.id)
	RETURNING ` + qualifiedPackColumns

const packPublishedInOtherFolderQuery = `
	SELECT EXISTS (
		SELECT 1
		FROM packs p
		JOIN users u ON u.id = $1
		WHERE p.id = $2
		  AND p.org_id = u.org_id
		  AND u.org_id IS NOT NULL
		  AND u.deleted_at IS NULL
		  AND (p.owner_id = u.id OR $3)
		  AND p.library_folder_id IS NOT NULL
		  AND p.library_folder_id <> $4
	)`

const unpublishPackQuery = `
	UPDATE packs p
	SET library_folder_id = NULL,
	    published_at = NULL,
	    status = 'draft',
	    updated_at = now()
	FROM users u
	WHERE p.id = $2
	  AND u.id = $1
	  AND u.org_id IS NOT NULL
	  AND u.deleted_at IS NULL
	  AND p.org_id = u.org_id
	  AND (p.owner_id = u.id OR $3)`

const folderAllowedQuery = `
	SELECT EXISTS (
		SELECT 1
		FROM users u
		JOIN folders f ON f.owner_id = u.id
		WHERE u.id = $1
		  AND f.id = $2
		  AND f.org_id = u.org_id
		  AND f.section IN ('my', 'students')
		  AND u.org_id IS NOT NULL
		  AND u.deleted_at IS NULL
	)`

const countAccessibleMediaQuery = `
	SELECT count(*) FROM media_files
	WHERE org_id = $1 AND id = ANY($2::uuid[])`

const deletePackMediaUsagesQuery = `
	DELETE FROM media_usages WHERE source_type = 'pack' AND source_id = $1`

const insertPackMediaUsagesQuery = `
	INSERT INTO media_usages (media_id, source_type, source_id)
	SELECT unnest($1::uuid[]), 'pack', $2`

const savePackConfigQuery = `
	UPDATE packs p SET config = $3, updated_at = now()
	WHERE p.id = $2 AND p.owner_id = $1
	RETURNING ` + qualifiedPackColumns

const archiveMediaQuery = `
	SELECT m.id, m.org_id, m.uploader_id, m.sha256, m.mime_type,
	       m.size_bytes, m.minio_key, m.created_at
	FROM media_usages mu
	JOIN media_files m ON m.id = mu.media_id
	WHERE mu.source_type = 'pack' AND mu.source_id = $1
	ORDER BY m.id`

const adaptationArchiveQuery = `
	SELECT pa.config, p.title
	FROM pack_adaptations pa
	JOIN packs p ON p.id = pa.pack_id
	JOIN students s ON s.id = pa.student_id
	JOIN users u ON u.id = $1
	WHERE pa.id = $2
	  AND pa.created_by = u.id
	  AND p.owner_id = u.id
	  AND p.org_id = u.org_id
	  AND s.defectologist_id = u.id
	  AND s.deleted_at IS NULL
	  AND u.deleted_at IS NULL`

const adaptationArchiveMediaQuery = `
	SELECT m.id, m.org_id, m.uploader_id, m.sha256, m.mime_type,
	       m.size_bytes, m.minio_key, m.created_at
	FROM media_usages mu
	JOIN media_files m ON m.id = mu.media_id
	WHERE mu.source_type = 'pack_adaptation' AND mu.source_id = $1
	ORDER BY m.id`

const lockPackConfigQuery = `
	SELECT p.org_id, p.config
	FROM packs p
	JOIN users u ON u.id = $1
	WHERE p.id = $2
	  AND p.owner_id = u.id
	  AND p.org_id = u.org_id
	  AND u.deleted_at IS NULL
	FOR UPDATE OF p, u`

const countOwnedStudentsQuery = `
	SELECT count(*) FROM students
	WHERE defectologist_id = $1
	  AND id = ANY($2::uuid[])
	  AND deleted_at IS NULL`

const upsertAdaptationQuery = `
	INSERT INTO pack_adaptations (pack_id, student_id, config, created_by)
	VALUES ($1, $2, $3, $4)
	ON CONFLICT (pack_id, student_id) DO UPDATE
	SET config = EXCLUDED.config, updated_at = now()
	RETURNING id, pack_id, student_id, config, created_by, created_at, updated_at`

const replaceAdaptationUsagesQuery = `
	WITH cleared AS (
		DELETE FROM media_usages
		WHERE source_type = 'pack_adaptation' AND source_id = $2
	)
	INSERT INTO media_usages (media_id, source_type, source_id)
	SELECT media_id, 'pack_adaptation', $2
	FROM media_usages
	WHERE source_type = 'pack' AND source_id = $1`

const listAdaptationsQuery = `
	SELECT pa.id, pa.pack_id, pa.student_id, pa.config,
	       pa.created_by, pa.created_at, pa.updated_at
	FROM pack_adaptations pa
	JOIN packs p ON p.id = pa.pack_id
	JOIN students s ON s.id = pa.student_id
	JOIN users u ON u.id = $1
	WHERE pa.pack_id = $2
	  AND pa.created_by = u.id
	  AND p.owner_id = u.id
	  AND p.org_id = u.org_id
	  AND s.defectologist_id = u.id
	  AND s.deleted_at IS NULL
	  AND u.deleted_at IS NULL
	ORDER BY pa.updated_at DESC, pa.id`

const getAdaptationQuery = `
	SELECT pa.id, pa.pack_id, pa.student_id, pa.config,
	       pa.created_by, pa.created_at, pa.updated_at
	FROM pack_adaptations pa
	JOIN packs p ON p.id = pa.pack_id
	JOIN students s ON s.id = pa.student_id
	JOIN users u ON u.id = $1
	WHERE pa.id = $2
	  AND pa.created_by = u.id
	  AND p.owner_id = u.id
	  AND p.org_id = u.org_id
	  AND s.defectologist_id = u.id
	  AND s.deleted_at IS NULL
	  AND u.deleted_at IS NULL`

const lockAdaptationForUpdateQuery = `
	SELECT p.org_id
	FROM pack_adaptations pa
	JOIN packs p ON p.id = pa.pack_id
	JOIN students s ON s.id = pa.student_id
	JOIN users u ON u.id = $1
	WHERE pa.id = $2
	  AND pa.created_by = u.id
	  AND p.owner_id = u.id
	  AND p.org_id = u.org_id
	  AND s.defectologist_id = u.id
	  AND s.deleted_at IS NULL
	  AND u.deleted_at IS NULL
	FOR UPDATE OF pa`

const updateAdaptationConfigQuery = `
	UPDATE pack_adaptations
	SET config = $2, updated_at = now()
	WHERE id = $1
	RETURNING id, pack_id, student_id, config, created_by, created_at, updated_at`

const insertAdaptationMediaUsagesQuery = `
	INSERT INTO media_usages (media_id, source_type, source_id)
	SELECT unnest($1::uuid[]), 'pack_adaptation', $2`

const ownedPackExistsQuery = `
	SELECT EXISTS (
		SELECT 1
		FROM packs p
		JOIN users u ON u.id = $1
		WHERE p.id = $2
		  AND p.owner_id = u.id
		  AND p.org_id = u.org_id
		  AND u.deleted_at IS NULL
	)`

const lockAdaptationForUnassignQuery = `
	SELECT pa.id
	FROM pack_adaptations pa
	JOIN packs p ON p.id = pa.pack_id
	JOIN students s ON s.id = pa.student_id
	WHERE pa.pack_id = $2 AND pa.student_id = $3
	  AND p.owner_id = $1 AND s.defectologist_id = $1
	FOR UPDATE OF pa`

const deleteAdaptationUsagesQuery = `
	DELETE FROM media_usages
	WHERE source_type = 'pack_adaptation' AND source_id = $1`

const deleteAdaptationQuery = `
	DELETE FROM pack_adaptations WHERE id = $1`

const adaptationIDsForPackQuery = `
	SELECT id FROM pack_adaptations WHERE pack_id = $1`

const deleteAdaptationUsagesForIDsQuery = `
	DELETE FROM media_usages
	WHERE source_type = 'pack_adaptation' AND source_id = ANY($1::uuid[])`

const createVersionQuery = `
	INSERT INTO pack_versions (pack_id, version, config, created_by)
	SELECT $1, COALESCE(MAX(version), 0) + 1, $2, $3
	FROM pack_versions
	WHERE pack_id = $1
	RETURNING id, pack_id, version, config, created_by, created_at`

const insertVersionMediaUsagesQuery = `
	INSERT INTO media_usages (media_id, source_type, source_id)
	SELECT media_id, 'pack_version', $2
	FROM media_usages
	WHERE source_type = 'pack' AND source_id = $1`

const listVersionsQuery = `
	SELECT pv.id, pv.pack_id, pv.version, pv.created_by, pv.created_at
	FROM pack_versions pv
	JOIN packs p ON p.id = pv.pack_id
	JOIN users u ON u.id = $1
	WHERE pv.pack_id = $2
	  AND p.owner_id = u.id
	  AND p.org_id = u.org_id
	  AND u.deleted_at IS NULL
	ORDER BY pv.version DESC
	LIMIT $3 OFFSET $4`

const getVersionQuery = `
	SELECT pv.id, pv.pack_id, pv.version, pv.config, pv.created_by, pv.created_at
	FROM pack_versions pv
	WHERE pv.pack_id = $1 AND pv.version = $2`

const getAccessibleVersionQuery = `
	SELECT pv.id, pv.pack_id, pv.version, pv.config, pv.created_by, pv.created_at
	FROM pack_versions pv
	JOIN packs p ON p.id = pv.pack_id
	JOIN users u ON u.id = $1
	WHERE pv.pack_id = $2
	  AND pv.version = $3
	  AND p.owner_id = u.id
	  AND p.org_id = u.org_id
	  AND u.deleted_at IS NULL`

const replacePackUsagesFromVersionQuery = `
	INSERT INTO media_usages (media_id, source_type, source_id)
	SELECT media_id, 'pack', $1
	FROM media_usages
	WHERE source_type = 'pack_version' AND source_id = $2`

const deleteVersionMediaUsagesForPackQuery = `
	DELETE FROM media_usages
	WHERE source_type = 'pack_version'
	  AND source_id IN (SELECT id FROM pack_versions WHERE pack_id = $1)`

const putFavoriteQuery = `
	WITH accessible AS (
		SELECT p.id
		FROM packs p
		JOIN users u ON u.id = $1
		WHERE p.id = $2
		  AND p.org_id = u.org_id
		  AND (p.owner_id = u.id OR p.published_at IS NOT NULL)
		  AND u.deleted_at IS NULL
	)
	INSERT INTO favorite_packs (user_id, pack_id)
	SELECT $1, id FROM accessible
	ON CONFLICT (user_id, pack_id) DO UPDATE SET pack_id = EXCLUDED.pack_id
	RETURNING pack_id`

const deleteFavoriteQuery = `
	DELETE FROM favorite_packs WHERE user_id = $1 AND pack_id = $2`

const listFavoritePacksQuery = `
	WITH active_user AS (
		SELECT id, org_id
		FROM users
		WHERE id = $1
		  AND org_id IS NOT NULL
		  AND deleted_at IS NULL
	), favorites AS (
		SELECT p.id, p.org_id, p.owner_id,
		       CASE WHEN p.owner_id = u.id THEN p.folder_id ELSE p.library_folder_id
		       END AS result_folder_id,
		       p.library_folder_id, p.published_at, p.title, p.status,
		       p.age_min, p.age_max, p.difficulty, p.goals, p.notes, p.config,
		       p.created_at, p.updated_at, f.section, fp.created_at AS favorited_at
		FROM active_user u
		JOIN favorite_packs fp ON fp.user_id = u.id
		JOIN packs p ON p.id = fp.pack_id AND p.org_id = u.org_id
		JOIN folders f ON f.id = CASE WHEN p.owner_id = u.id THEN p.folder_id ELSE p.library_folder_id END
		              AND f.org_id = u.org_id
		WHERE p.owner_id = u.id OR p.published_at IS NOT NULL
	)
	SELECT id, org_id, owner_id, result_folder_id, library_folder_id,
	       published_at, title, status, age_min, age_max, difficulty,
	       goals, notes, config, true AS is_favorite, section, created_at, updated_at
	FROM favorites
	ORDER BY favorited_at DESC, id
	LIMIT $2 OFFSET $3`

const packColumns = `
	id, org_id, owner_id, folder_id, library_folder_id, published_at,
	title, status, age_min, age_max,
	difficulty, goals, notes, config, created_at, updated_at`

const qualifiedPackColumns = `
	p.id, p.org_id, p.owner_id, p.folder_id, p.library_folder_id,
	p.published_at, p.title, p.status,
	p.age_min, p.age_max, p.difficulty, p.goals, p.notes, p.config,
	p.created_at, p.updated_at`
