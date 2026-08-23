-- +goose Up
CREATE TABLE user_settings (
    user_id     UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    settings    JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT user_settings_settings_object CHECK (jsonb_typeof(settings) = 'object')
);

CREATE TABLE user_setting_templates (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        VARCHAR(100) NOT NULL,
    body        JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT user_setting_templates_name_nonempty CHECK (length(btrim(name)) > 0),
    CONSTRAINT user_setting_templates_body_object CHECK (jsonb_typeof(body) = 'object')
);

CREATE INDEX user_setting_templates_user_updated_idx
    ON user_setting_templates (user_id, updated_at DESC, id);

-- +goose Down
DROP TABLE user_setting_templates;
DROP TABLE user_settings;
