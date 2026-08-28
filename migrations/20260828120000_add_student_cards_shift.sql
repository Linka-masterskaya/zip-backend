-- +goose Up
-- +goose StatementBegin
-- Смещение карточек в наборах задаётся на ученика: одним детям удобнее
-- держать материал слева, другим справа. Колонка NOT NULL с дефолтом
-- 'full' — старые карточки получают привычную раскладку без миграции
-- данных, а сброс через null в PATCH возвращает то же значение.
ALTER TABLE students
    ADD COLUMN cards_shift VARCHAR(8) NOT NULL DEFAULT 'full'
        CHECK (cards_shift IN ('left', 'full', 'right'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE students DROP COLUMN cards_shift;
-- +goose StatementEnd
