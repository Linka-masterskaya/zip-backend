-- +goose Up
-- +goose StatementBegin
-- Аватар ученика хранится ссылкой на media_files, а не строкой URL:
-- ссылки на объекты presigned и живут 15 минут, поэтому сохранённый URL
-- протух бы в тот же час. При удалении файла из банка ссылка обнуляется,
-- и карточка ученика остаётся без битого аватара.
ALTER TABLE students
    ADD COLUMN avatar_media_id UUID REFERENCES media_files(id) ON DELETE SET NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE students DROP COLUMN avatar_media_id;
-- +goose StatementEnd
