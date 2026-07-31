-- +goose Up
ALTER TABLE packs
    ADD COLUMN folder_id UUID,
    ADD COLUMN library_folder_id UUID,
    ADD COLUMN published_at TIMESTAMPTZ,
    ADD COLUMN age_min INT,
    ADD COLUMN age_max INT,
    ADD COLUMN difficulty TEXT,
    ADD COLUMN goals TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN notes TEXT NOT NULL DEFAULT '',
    ADD CONSTRAINT packs_age_min_chk CHECK (age_min IS NULL OR age_min BETWEEN 3 AND 18),
    ADD CONSTRAINT packs_age_max_chk CHECK (age_max IS NULL OR age_max BETWEEN 3 AND 18),
    ADD CONSTRAINT packs_age_range_chk CHECK (
        age_min IS NULL OR age_max IS NULL OR age_min <= age_max
    ),
    ADD CONSTRAINT packs_difficulty_chk CHECK (
        difficulty IS NULL OR difficulty IN ('easy', 'medium', 'hard')
    ),
    ADD CONSTRAINT packs_published_chk CHECK (
        (library_folder_id IS NULL) = (published_at IS NULL)
    );

INSERT INTO folders (org_id, owner_id, section, kind, name, depth)
SELECT DISTINCT p.org_id, p.owner_id, 'my', 'folder', 'Мои наборы', 0
FROM packs AS p
WHERE NOT EXISTS (
    SELECT 1
    FROM folders AS f
    WHERE f.org_id = p.org_id
      AND f.owner_id = p.owner_id
      AND f.section = 'my'
      AND f.parent_id IS NULL
);

UPDATE packs AS p
SET folder_id = (
    SELECT f.id
    FROM folders AS f
    WHERE f.org_id = p.org_id
      AND f.owner_id = p.owner_id
      AND f.section = 'my'
      AND f.parent_id IS NULL
    ORDER BY f.created_at, f.id
    LIMIT 1
)
WHERE p.folder_id IS NULL;

ALTER TABLE packs
    ALTER COLUMN folder_id SET NOT NULL,
    ADD CONSTRAINT packs_folder_id_fkey
        FOREIGN KEY (folder_id) REFERENCES folders(id) ON DELETE RESTRICT,
    ADD CONSTRAINT packs_library_folder_id_fkey
        FOREIGN KEY (library_folder_id) REFERENCES folders(id) ON DELETE RESTRICT;

CREATE INDEX packs_folder_idx ON packs(folder_id);
CREATE INDEX packs_library_folder_idx ON packs(library_folder_id)
    WHERE library_folder_id IS NOT NULL;
CREATE INDEX packs_title_lower_idx ON packs(lower(title));
CREATE INDEX packs_difficulty_idx ON packs(difficulty);
CREATE INDEX packs_age_range_idx ON packs(age_min, age_max);

-- +goose Down
DROP INDEX packs_age_range_idx;
DROP INDEX packs_difficulty_idx;
DROP INDEX packs_title_lower_idx;
DROP INDEX packs_library_folder_idx;
DROP INDEX packs_folder_idx;

ALTER TABLE packs
    DROP CONSTRAINT packs_library_folder_id_fkey,
    DROP CONSTRAINT packs_folder_id_fkey,
    DROP CONSTRAINT packs_published_chk,
    DROP CONSTRAINT packs_difficulty_chk,
    DROP CONSTRAINT packs_age_range_chk,
    DROP CONSTRAINT packs_age_max_chk,
    DROP CONSTRAINT packs_age_min_chk,
    DROP COLUMN notes,
    DROP COLUMN goals,
    DROP COLUMN difficulty,
    DROP COLUMN age_max,
    DROP COLUMN age_min,
    DROP COLUMN published_at,
    DROP COLUMN library_folder_id,
    DROP COLUMN folder_id;
