-- +goose Up
CREATE TABLE audio_bank (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    minio_key    TEXT NOT NULL,
    text         TEXT NOT NULL,
    voice        TEXT NOT NULL,
    sha256       TEXT NOT NULL,
    size_bytes   BIGINT NOT NULL,
    generated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (text, voice)
);
-- +goose Down
DROP TABLE audio_bank;
