-- +goose Up
-- +goose StatementBegin
CREATE TABLE students (
    id                UUID         PRIMARY KEY,
    defectologist_id  UUID         NOT NULL REFERENCES users(id),
    email_encrypted   BYTEA        NOT NULL,
    email_verified    BOOLEAN      NOT NULL DEFAULT FALSE,
    name              VARCHAR(255) NOT NULL,
    age               INT          CHECK (age IS NULL OR (age >= 0 AND age <= 100)),
    status            VARCHAR(32)  NOT NULL CHECK (status IN ('active', 'paused', 'archived', 'one_time')),
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deleted_at        TIMESTAMPTZ
);
CREATE INDEX students_defectologist_idx ON students (defectologist_id) WHERE deleted_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE students;
-- +goose StatementEnd