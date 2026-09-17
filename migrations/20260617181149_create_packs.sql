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

-- +goose Down
DROP TABLE packs;
