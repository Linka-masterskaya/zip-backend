-- +goose Up
ALTER TABLE pack_share_jobs
    ADD COLUMN last_error TEXT NOT NULL DEFAULT '',
    ADD COLUMN email_sent_at TIMESTAMPTZ;

CREATE INDEX idx_pack_share_jobs_email_sent_pending
    ON pack_share_jobs (email_sent_at, lease_until)
    WHERE status = 'processing' AND email_sent_at IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_pack_share_jobs_email_sent_pending;
ALTER TABLE pack_share_jobs
    DROP COLUMN IF EXISTS email_sent_at,
    DROP COLUMN IF EXISTS last_error;
