-- +goose Up
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE packs (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id               UUID NOT NULL REFERENCES organizations(id),
    owner_id             UUID NOT NULL REFERENCES users(id),
    folder_id            UUID NOT NULL REFERENCES folders(id),
    title                TEXT NOT NULL,
    status               TEXT NOT NULL DEFAULT 'draft',
    age                  INT,
    difficulty           TEXT CHECK (difficulty IN ('easy', 'medium', 'hard')),
    voice                TEXT,
    notes                TEXT,
    show_keyboard_input  BOOLEAN NOT NULL DEFAULT false,
    hide_input_autoplay  BOOLEAN NOT NULL DEFAULT false,
    quiz_mode            BOOLEAN NOT NULL DEFAULT false,
    config               JSONB NOT NULL DEFAULT '{}',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_packs_org_id     ON packs(org_id);
CREATE INDEX idx_packs_owner_id   ON packs(owner_id);
CREATE INDEX idx_packs_folder_id  ON packs(folder_id);
CREATE INDEX idx_packs_difficulty ON packs(difficulty);
CREATE INDEX idx_packs_age        ON packs(age);
CREATE INDEX idx_packs_title_trgm ON packs USING gin (title gin_trgm_ops);

CREATE TABLE pack_versions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pack_id     UUID NOT NULL REFERENCES packs(id) ON DELETE CASCADE,
    version     INT NOT NULL,
    config      JSONB NOT NULL,
    created_by  UUID NOT NULL REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(pack_id, version)
);

-- +goose Down
DROP TABLE pack_versions;
DROP TABLE packs;
