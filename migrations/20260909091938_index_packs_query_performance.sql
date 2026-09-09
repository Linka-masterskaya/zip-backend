-- +goose Up
-- +goose NO TRANSACTION
CREATE INDEX CONCURRENTLY IF NOT EXISTS pack_adaptations_student_idx ON pack_adaptations(student_id);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS pack_adaptations_student_idx;
