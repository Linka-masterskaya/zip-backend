-- +goose Up
ALTER TABLE packs
    ADD COLUMN age INT,
    ADD CONSTRAINT packs_age_chk CHECK (age IS NULL OR age BETWEEN 3 AND 18);

UPDATE packs
SET age = COALESCE(age_min, age_max);

ALTER TABLE packs
    DROP COLUMN age_max,
    DROP COLUMN age_min;

CREATE INDEX packs_age_idx ON packs(age);

-- +goose Down
ALTER TABLE packs
    ADD COLUMN age_min INT,
    ADD COLUMN age_max INT;

UPDATE packs
SET age_min = age,
    age_max = age;

ALTER TABLE packs
    DROP COLUMN age;

ALTER TABLE packs
    ADD CONSTRAINT packs_age_min_chk CHECK (age_min IS NULL OR age_min BETWEEN 3 AND 18),
    ADD CONSTRAINT packs_age_max_chk CHECK (age_max IS NULL OR age_max BETWEEN 3 AND 18),
    ADD CONSTRAINT packs_age_range_chk CHECK (
        age_min IS NULL OR age_max IS NULL OR age_min <= age_max
    );

CREATE INDEX packs_age_range_idx ON packs(age_min, age_max);
