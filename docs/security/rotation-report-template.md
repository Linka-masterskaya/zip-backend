# Production key audit and rotation evidence

Do not place raw JWT, AES, HMAC, database, SMTP, MinIO, or deployment credentials in this document.

## Environment

- Environment:
- Operator:
- UTC start time:
- Application version before operation:
- Immutable rotation image digest:
- GitHub Actions run URL:
- Change/deployment ticket:

## Required PR policy

- `Required PR checks` selected in the `main` ruleset: yes/no
- Ruleset verification date:
- Verifier:

## Active secret fingerprint audit

Record only the status emitted by `audit-secret-fingerprints.sh`.

- `JWT_SECRET`: `not-known-tracked` / `KNOWN_TRACKED_CREDENTIAL`
- `CRYPTO_AES_KEY`: `not-known-tracked` / `KNOWN_TRACKED_CREDENTIAL`
- `CRYPTO_HMAC_KEY`: `not-known-tracked` / `KNOWN_TRACKED_CREDENTIAL`

## Database preflight

- PostgreSQL backup identifier:
- Backup restore verification:
- Writes stopped or maintenance mode enabled:
- `records`:
- `aes_old`:
- `aes_new`:
- `hmac_old`:
- `hmac_new`:
- Preflight result:

## Rotation

- JWT rotated: yes/no/not required
- Users informed that existing JWTs become invalid: yes/no/not required
- AES/HMAC `apply` run URL:
- `changed_aes`:
- `changed_hmac`:
- Commit result:

## Post-rotation verification

- `/readyz`:
- Login by email:
- Refresh token behavior:
- Profile email readable:
- Student email readable:
- Lookup by recalculated HMAC:
- Post-check reports all records on new keys:

## Rollback verification

- Reverse rotation tested before maintenance window:
- Previous keys retained in restricted rollback storage:
- Rollback deadline:
- Rollback performed: yes/no
- Rollback result, when performed:

## Approval

- Technical reviewer:
- Security reviewer:
- Final decision:
