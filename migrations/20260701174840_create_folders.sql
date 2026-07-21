-- +goose Up
CREATE TABLE folders (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    section     TEXT NOT NULL CHECK (section IN ('library', 'my', 'students')),
    kind        TEXT NOT NULL CHECK (kind IN ('folder', 'student')),
    parent_id   UUID REFERENCES folders(id) ON DELETE CASCADE,
    student_id  UUID REFERENCES students(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT folders_kind_student_id_chk CHECK (
        (kind = 'student' AND student_id IS NOT NULL) OR
        (kind = 'folder' AND student_id IS NULL)
    )
);

CREATE INDEX idx_folders_owner_id   ON folders(owner_id);
CREATE INDEX idx_folders_student_id ON folders(student_id) WHERE student_id IS NOT NULL;
CREATE INDEX idx_folders_parent_id  ON folders(parent_id) WHERE parent_id IS NOT NULL;

-- +goose Down
DROP TABLE folders;
