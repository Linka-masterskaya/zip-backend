-- +goose Up
ALTER TABLE students
    ADD COLUMN IF NOT EXISTS last_lesson_at DATE;

-- +goose Down
ALTER TABLE students
    DROP COLUMN IF EXISTS last_lesson_at;