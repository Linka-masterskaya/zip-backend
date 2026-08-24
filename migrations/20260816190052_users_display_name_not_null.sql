-- +goose Up
UPDATE users
SET display_name = 'Пользователь'
WHERE display_name IS NULL;

ALTER TABLE users
    ALTER COLUMN display_name SET NOT NULL;

-- +goose Down
ALTER TABLE users
    ALTER COLUMN display_name DROP NOT NULL;
