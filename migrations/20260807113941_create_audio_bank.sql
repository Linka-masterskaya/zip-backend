-- +goose Up
CREATE TABLE audio_bank (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    media_id     UUID NOT NULL REFERENCES media_files(id) ON DELETE CASCADE,
    text         TEXT NOT NULL,   -- нормализованная фраза
    voice        TEXT NOT NULL,
    generated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (text, voice)
);
-- +goose Down
DROP TABLE audio_bank;
