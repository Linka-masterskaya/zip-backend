-- +goose Up
CREATE TABLE folders (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    owner_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    parent_id   UUID REFERENCES folders(id) ON DELETE RESTRICT,
    section     TEXT NOT NULL CHECK (section IN ('library', 'my', 'students')),
    kind        TEXT NOT NULL CHECK (kind IN ('folder', 'student')),
    student_id  UUID REFERENCES students(id) ON DELETE RESTRICT,
    name        TEXT NOT NULL,
    depth       INT NOT NULL CHECK (depth BETWEEN 0 AND 4),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT folders_kind_student_chk CHECK (
        (kind = 'student' AND student_id IS NOT NULL AND section = 'students') OR
        (kind = 'folder' AND student_id IS NULL)
    )
);

CREATE INDEX folders_org_section_parent_idx ON folders(org_id, section, parent_id);
CREATE INDEX folders_owner_section_parent_idx ON folders(owner_id, section, parent_id);
CREATE INDEX folders_student_idx ON folders(student_id) WHERE student_id IS NOT NULL;

-- +goose Down
DROP TABLE folders;
