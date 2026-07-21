-- +goose Up
ALTER TABLE packs
    ADD COLUMN folder_id UUID,
    ADD COLUMN age_min INT,
    ADD COLUMN age_max INT,
    ADD COLUMN difficulty TEXT,
    ADD COLUMN goals TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN notes TEXT,
    ADD CONSTRAINT packs_age_min_chk CHECK (age_min IS NULL OR age_min >= 0),
    ADD CONSTRAINT packs_age_max_chk CHECK (age_max IS NULL OR age_max >= 0),
    ADD CONSTRAINT packs_age_range_chk CHECK (
        age_min IS NULL OR age_max IS NULL OR age_min <= age_max
    ),
    ADD CONSTRAINT packs_difficulty_chk CHECK (
        difficulty IS NULL OR difficulty IN ('easy', 'medium', 'hard')
    );

-- Existing packs predate folders. Reuse a root "my" folder for the owner
-- when possible; otherwise create one before backfilling legacy rows.
INSERT INTO folders (owner_id, section, kind)
SELECT DISTINCT p.owner_id, 'my', 'folder'
FROM packs AS p
WHERE NOT EXISTS (
    SELECT 1
    FROM folders AS f
    WHERE f.owner_id = p.owner_id
      AND f.section = 'my'
      AND f.kind = 'folder'
      AND f.parent_id IS NULL
      AND f.student_id IS NULL
);

UPDATE packs AS p
SET folder_id = (
    SELECT f.id
    FROM folders AS f
    WHERE f.owner_id = p.owner_id
      AND f.section = 'my'
      AND f.kind = 'folder'
      AND f.parent_id IS NULL
      AND f.student_id IS NULL
    ORDER BY f.created_at, f.id
    LIMIT 1
)
WHERE p.folder_id IS NULL;

-- Validate constraints before making the column mandatory. NOT VALID avoids a
-- long initial table scan while holding the strongest ALTER TABLE lock.
ALTER TABLE packs
    ADD CONSTRAINT packs_folder_id_fkey
        FOREIGN KEY (folder_id) REFERENCES folders(id) NOT VALID,
    ADD CONSTRAINT packs_folder_id_not_null_chk
        CHECK (folder_id IS NOT NULL) NOT VALID;

ALTER TABLE packs VALIDATE CONSTRAINT packs_folder_id_fkey;
ALTER TABLE packs VALIDATE CONSTRAINT packs_folder_id_not_null_chk;

ALTER TABLE packs ALTER COLUMN folder_id SET NOT NULL;
ALTER TABLE packs DROP CONSTRAINT packs_folder_id_not_null_chk;

CREATE INDEX idx_packs_org_id     ON packs(org_id);
CREATE INDEX idx_packs_owner_id   ON packs(owner_id);
CREATE INDEX idx_packs_folder_id  ON packs(folder_id);
CREATE INDEX idx_packs_difficulty ON packs(difficulty);
CREATE INDEX idx_packs_age_min    ON packs(age_min);
CREATE INDEX idx_packs_age_max    ON packs(age_max);

-- +goose Down
DROP INDEX idx_packs_age_max;
DROP INDEX idx_packs_age_min;
DROP INDEX idx_packs_difficulty;
DROP INDEX idx_packs_folder_id;
DROP INDEX idx_packs_owner_id;
DROP INDEX idx_packs_org_id;

ALTER TABLE packs
    DROP CONSTRAINT packs_folder_id_fkey,
    DROP CONSTRAINT packs_difficulty_chk,
    DROP CONSTRAINT packs_age_range_chk,
    DROP CONSTRAINT packs_age_max_chk,
    DROP CONSTRAINT packs_age_min_chk,
    DROP COLUMN notes,
    DROP COLUMN goals,
    DROP COLUMN difficulty,
    DROP COLUMN age_max,
    DROP COLUMN age_min,
    DROP COLUMN folder_id;
