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

const getPackQuery = `
	SELECT ` + qualifiedPackColumns + `
	FROM packs p
	JOIN users u ON u.id = $1
	WHERE p.id = $2
	  AND (
		(p.owner_id = u.id AND p.org_id = u.org_id)
		OR p.published_at IS NOT NULL
	  )
	  AND u.deleted_at IS NULL`

const listPacksQuery = `
	SELECT ` + qualifiedPackColumns + `
	FROM packs p
	JOIN users u ON u.id = $1
	WHERE p.folder_id = $2
	  AND p.owner_id = u.id
	  AND p.org_id = u.org_id
	  AND u.deleted_at IS NULL
	ORDER BY p.updated_at DESC, p.id
	LIMIT $3 OFFSET $4`

const updatePackQuery = `
	UPDATE packs p
	SET title = COALESCE($3::text, p.title),
	    folder_id = CASE WHEN $4::uuid IS NULL THEN p.folder_id ELSE f.id END,
	    age_min = CASE WHEN $5::boolean THEN $6::int ELSE p.age_min END,
	    age_max = CASE WHEN $7::boolean THEN $8::int ELSE p.age_max END,
	    difficulty = CASE WHEN $9::boolean THEN $10::text ELSE p.difficulty END,
	    goals = COALESCE($11::text[], p.goals),
	    notes = CASE WHEN $12::boolean THEN COALESCE($13::text, '') ELSE p.notes END,
	    updated_at = now()
	FROM users u
	LEFT JOIN folders f ON f.id = $4::uuid
		AND f.owner_id = u.id
		AND f.org_id = u.org_id
		AND f.section IN ('my', 'students')
	WHERE p.id = $2
	  AND u.id = $1
	  AND ($4::uuid IS NULL OR f.id IS NOT NULL)
	  AND p.owner_id = u.id
	  AND p.org_id = u.org_id
	  AND u.deleted_at IS NULL
	RETURNING ` + qualifiedPackColumns

const deletePackQuery = `
	DELETE FROM packs p
	USING users u
	WHERE p.id = $2
	  AND u.id = $1
	  AND p.owner_id = u.id
	  AND p.org_id = u.org_id
	  AND p.published_at IS NULL
	  AND u.deleted_at IS NULL`

const packPublishedQuery = `
	SELECT EXISTS (
		SELECT 1 FROM packs
		WHERE id = $2 AND owner_id = $1 AND published_at IS NOT NULL
	)`

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
	    updated_at = now()
	FROM users u, folders f
	WHERE p.id = $2
	  AND u.id = $1
	  AND u.deleted_at IS NULL
	  AND f.id = $3
	  AND f.section = 'library'
	  AND (f.owner_id = u.id OR $4)
	  AND (p.owner_id = u.id OR $4)
	  AND (p.library_folder_id IS NULL OR p.library_folder_id = f.id)
	RETURNING ` + qualifiedPackColumns

const packPublishedInOtherFolderQuery = `
	SELECT EXISTS (
		SELECT 1 FROM packs
		WHERE id = $2
		  AND (owner_id = $1 OR $3)
		  AND library_folder_id IS NOT NULL
		  AND library_folder_id <> $4
	)`

const unpublishPackQuery = `
	UPDATE packs
	SET library_folder_id = NULL, published_at = NULL, updated_at = now()
	WHERE id = $2 AND (owner_id = $1 OR $3)`

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

const packColumns = `
	id, org_id, owner_id, folder_id, library_folder_id, published_at,
	title, status, age_min, age_max,
	difficulty, goals, notes, config, created_at, updated_at`

const qualifiedPackColumns = `
	p.id, p.org_id, p.owner_id, p.folder_id, p.library_folder_id,
	p.published_at, p.title, p.status,
	p.age_min, p.age_max, p.difficulty, p.goals, p.notes, p.config,
	p.created_at, p.updated_at`
