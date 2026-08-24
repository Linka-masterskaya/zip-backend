-- +goose Up
ALTER TABLE user_setting_templates
    ADD CONSTRAINT user_setting_templates_user_name_unique UNIQUE (user_id, name);

-- +goose Down
ALTER TABLE user_setting_templates
    DROP CONSTRAINT IF EXISTS user_setting_templates_user_name_unique;
