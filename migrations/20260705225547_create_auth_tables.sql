-- +goose Up
CREATE TABLE auth_cred (
                           user_id         UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
                           email_hash      BYTEA NOT NULL,
                           email_encrypted BYTEA NOT NULL,
                           password_hash   TEXT,
                           role            TEXT NOT NULL DEFAULT 'viewer',
                           created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
                           updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE auth_identities (
                                 id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
                                 user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
                                 provider      TEXT NOT NULL,
                                 provider_uid  TEXT NOT NULL,
                                 created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_auth_cred_email_hash ON auth_cred(email_hash);
CREATE UNIQUE INDEX idx_auth_identities_provider_uid ON auth_identities(provider, provider_uid);

-- +goose Down
DROP TABLE auth_identities;
DROP TABLE auth_cred;
