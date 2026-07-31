-- +goose Up
UPDATE packs
SET status = CASE
    WHEN published_at IS NULL THEN 'draft'
    ELSE 'published'
END;

ALTER TABLE packs
    ADD CONSTRAINT packs_status_chk
        CHECK (status IN ('draft', 'published')),
    ADD CONSTRAINT packs_publication_status_chk
        CHECK (
            (published_at IS NULL AND status = 'draft')
            OR (published_at IS NOT NULL AND status = 'published')
        );

-- +goose Down
ALTER TABLE packs
    DROP CONSTRAINT packs_publication_status_chk,
    DROP CONSTRAINT packs_status_chk;
