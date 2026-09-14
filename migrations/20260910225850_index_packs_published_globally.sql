-- +goose Up
-- +goose NO TRANSACTION
CREATE INDEX CONCURRENTLY IF NOT EXISTS packs_published_globally_idx
    ON packs (org_id)
    WHERE published_globally = true AND published_at IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS packs_published_globally_idx;
