-- +goose Up
-- +goose NO TRANSACTION
CREATE INDEX CONCURRENTLY pack_adaptations_student_idx ON pack_adaptations(student_id);
CREATE INDEX CONCURRENTLY packs_org_published_idx ON packs(org_id) WHERE published_at IS NOT NULL;

-- +goose Down
DROP INDEX packs_org_published_idx;
DROP INDEX pack_adaptations_student_idx;