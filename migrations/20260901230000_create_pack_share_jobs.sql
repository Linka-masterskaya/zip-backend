-- +goose Up
CREATE TABLE pack_share_jobs (
    id              UUID PRIMARY KEY,
    owner_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    pack_id         UUID NOT NULL,
    student_id      UUID NOT NULL,
    request_id      TEXT,
    status          TEXT NOT NULL DEFAULT 'queued'
                    CHECK (status IN ('queued', 'processing', 'sent', 'failed')),
    message         TEXT NOT NULL DEFAULT '',
    attempts        INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_token     UUID,
    lease_until     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at    TIMESTAMPTZ,
    CONSTRAINT pack_share_jobs_lease_state CHECK (
        (status = 'processing' AND lease_token IS NOT NULL AND lease_until IS NOT NULL)
        OR (status <> 'processing' AND lease_token IS NULL AND lease_until IS NULL)
    ),
    CONSTRAINT pack_share_jobs_completion_state CHECK (
        (status IN ('sent', 'failed') AND completed_at IS NOT NULL)
        OR (status IN ('queued', 'processing') AND completed_at IS NULL)
    )
);

CREATE INDEX idx_pack_share_jobs_pending
    ON pack_share_jobs (next_attempt_at, created_at, id)
    WHERE status IN ('queued', 'processing');

CREATE INDEX idx_pack_share_jobs_owner
    ON pack_share_jobs (owner_id, created_at DESC);

CREATE INDEX idx_pack_share_jobs_completed
    ON pack_share_jobs (completed_at)
    WHERE completed_at IS NOT NULL;

-- +goose Down
DROP TABLE IF EXISTS pack_share_jobs;
