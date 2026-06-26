-- +goose Up
CREATE TABLE packs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES organizations(id),
    owner_id    UUID NOT NULL REFERENCES users(id),
    title       TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'draft',
    config      JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

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
