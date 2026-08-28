-- +goose Up
DROP INDEX packs_age_range_idx;

ALTER TABLE packs
    DROP CONSTRAINT packs_age_range_chk,
    DROP CONSTRAINT packs_age_max_chk,
    DROP CONSTRAINT packs_age_min_chk,
    DROP COLUMN age_max,
    DROP COLUMN age_min;

ALTER TABLE packs
    ADD COLUMN age INT,
    ADD CONSTRAINT packs_age_chk CHECK (age IS NULL OR age BETWEEN 3 AND 18);

CREATE INDEX packs_age_idx ON packs(age);

-- +goose Down
DROP INDEX packs_age_idx;

ALTER TABLE packs
    DROP CONSTRAINT packs_age_chk,
    DROP COLUMN age;

ALTER TABLE packs
    ADD COLUMN age_min INT,
    ADD COLUMN age_max INT;

ALTER TABLE packs
    ADD CONSTRAINT packs_age_min_chk CHECK (age_min IS NULL OR age_min BETWEEN 3 AND 18),
    ADD CONSTRAINT packs_age_max_chk CHECK (age_max IS NULL OR age_max BETWEEN 3 AND 18),
    ADD CONSTRAINT packs_age_range_chk CHECK (
        age_min IS NULL OR age_max IS NULL OR age_min <= age_max
    );

CREATE INDEX packs_age_range_idx ON packs(age_min, age_max);
