-- +goose Up
CREATE TABLE pack_adaptations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pack_id     UUID NOT NULL REFERENCES packs(id) ON DELETE CASCADE,
    student_id  UUID NOT NULL REFERENCES students(id) ON DELETE CASCADE,
    config      JSONB NOT NULL DEFAULT '{}',
    created_by  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(pack_id, student_id)
);

-- +goose Down
DROP TABLE pack_adaptations;
