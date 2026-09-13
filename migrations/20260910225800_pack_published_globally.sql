-- +goose Up
ALTER TABLE packs 
    ADD COLUMN published_globally BOOL NOT NULL DEFAULT false;

UPDATE packs p
  SET published_globally = true
  FROM auth_cred ac
  WHERE p.owner_id = ac.user_id
    AND ac.role = 'head_defectologist'
    AND p.published_at IS NOT NULL;

ALTER TABLE packs
    ADD CONSTRAINT packs_published_globally_chk
    CHECK (published_globally = false OR published_at IS NOT NULL);

-- +goose Down
ALTER TABLE packs DROP CONSTRAINT IF EXISTS packs_published_globally_chk;
ALTER TABLE packs DROP COLUMN IF EXISTS published_globally;