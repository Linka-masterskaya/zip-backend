-- +goose Up
-- +goose StatementBegin
-- +goose NO TRANSACTION
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS folders_one_per_student ON folders (student_id) WHERE kind = 'student' AND student_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
DROP INDEX IF EXISTS folders_one_per_student;