-- +goose Up
CREATE TABLE favorite_packs (
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    pack_id     UUID NOT NULL REFERENCES packs(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, pack_id)
);

-- +goose Down
DROP TABLE favorite_packs;
