# Secrets contract and key rotation

## Canonical environment contract

`.env.example` is the canonical list of environment variable names. Every credential stored in it is an explicit local-development fixture and must never be reused outside local development.

The production deploy workflow defines a corresponding `CI_<NAME>` value for every key in `.env.example`. `scripts/render-deploy-env.sh` refuses to render the file when any contract variable is missing, writes it atomically with mode `0600`, and never prints values. Every value is double-quoted using Docker Compose-compatible escaping for backslashes, quotes, and dollar signs, so spaces, `#`, `$`, quotes, and backslashes survive parsing unchanged. `scripts/validate-deploy-env.sh` decodes the same format and validates the rendered file before it is copied to the server.

The validator also enforces value-level consistency:

- the user, password, and database in `DB_URL` must equal `POSTGRES_USER`, `POSTGRES_PASSWORD`, and `POSTGRES_DB` after URL decoding;
- the password in `REDIS_URL` must equal `REDIS_PASSWORD` after URL decoding;
- placeholders are rejected case-insensitively;
- known development credentials and historical JWT/AES/HMAC fingerprints are rejected; AES/HMAC are checked both as tracked source text and as decoded key material;
- AES and HMAC keys must use canonical base64, have the required decoded lengths, and must differ;
- for Yandex, Gmail, Mail.ru, or `SMTP_REQUIRE_FROM_MATCH=true`, `SMTP_FROM_EMAIL` must equal `SMTP_USERNAME`.

Application secrets and their Viper paths:

| Environment variable | Config path | Production policy |
| --- | --- | --- |
| `DB_URL` | `db.url` | Required; password required; Compose credentials must match |
| `REDIS_URL` | `redis.url` | Required; authentication required; Compose password must match |
| `MINIO_ACCESS_KEY` | `minio.access_key` | Required; default/dev credentials rejected |
| `MINIO_SECRET_KEY` | `minio.secret_key` | Required; default/dev credentials rejected |
| `JWT_SECRET` | `jwt.secret` | Required; placeholders and tracked fingerprints rejected |
| `CRYPTO_AES_KEY` | `crypto.aes_key` | Required; canonical base64, exactly 32 bytes; tracked source and decoded-material fingerprints rejected |
| `CRYPTO_HMAC_KEY` | `crypto.hmac_key` | Required; canonical base64, at least 32 bytes; tracked source and decoded-material fingerprints rejected |
| `SMTP_PASSWORD` | `smtp.password` | Required; placeholders/dev fixture rejected |
| `YANDEX_CLIENT_SECRET` | `yandex.client_secret` | Optional until OAuth is enabled; placeholders rejected when set |
| `OPENAI_API_KEY` | `openai.api_key` | Optional until the integration is enabled; placeholders rejected when set |

Compose-only secrets are `POSTGRES_PASSWORD`, `REDIS_PASSWORD`, and `GRAFANA_ADMIN_PASSWORD`. CI-only credentials such as `DEPLOY_KEY` and `GITHUB_TOKEN` are not rendered into the application `.env`.

## Pull-request secret scanning

PR CI runs Gitleaks `v8.30.0` by immutable container digest with redaction enabled. It scans the final pull-request tree and every commit tree introduced between the pull-request base and head. Scanning commit snapshots catches a credential that is added in one PR commit and deleted in a later commit, while avoiding false positives caused solely by deleted lines in a remediation diff. Checkout uses full history (`fetch-depth: 0`) and the job fails if the base/head relationship cannot be verified. The job also creates a credential only in a temporary directory and proves that Gitleaks rejects it. `.gitleaks.toml` extends the default rules and allowlists only exact documented local-development fixtures. Historical leaked values are not allowlisted.

`Required PR checks` depends on Gitleaks and the dedicated PostgreSQL crypto-rotation integration job and fails unless every required job succeeds. Repository administrators must select the stable `Required PR checks` status in the `main` branch ruleset. Branch protection is an external GitHub repository setting and cannot be established by a tracked workflow alone.

## Historical exposure inventory

Git history confirms that seven fixed JWT/AES/HMAC credentials were tracked. Source fingerprints identify the exact tracked text. AES/HMAC material fingerprints identify the decoded bytes, so converting an early raw key to canonical base64 cannot bypass the denylist. One legacy base64 credential was reused for both AES and HMAC, so both its source and material fingerprints are denied in both policies:

| Type | First observed commit | Date | Tracked source SHA-256 | Decoded key-material SHA-256 |
| --- | --- | --- | --- | --- |
| JWT | `a456ada` | 2026-06-03 | `f16dd3036e8e542ced86fe8bc25d7d1f6c4b1622f0321069c17600c12b59415b` | — |
| JWT | `737870a` | 2026-07-10 | `32764387e4bc263f87e102ca57010c55ca8b3d9cb9fb0d809696a6f680e4384d` | — |
| AES | `7572693` | 2026-07-09 | `861009ec4d599fab1f40abc76e6f89880cff5833c79c548c99f9045f191cd90b` | `861009ec4d599fab1f40abc76e6f89880cff5833c79c548c99f9045f191cd90b` |
| AES | `737870a` | 2026-07-10 | `cbee3ab7885dfe98049d29d2d92bcb669ce3b37fa9b0f6e46a7a832772332b25` | `5f2560c1d6160f95c48ec63ef391d6993b70ceec9e2d9ad68dbab6286115bf0b` |
| HMAC | `7572693` | 2026-07-09 | `f6d527e6d01865481134f29788be2afe7fc3c702e1a55d7ceafac5f35199e8dc` | `f6d527e6d01865481134f29788be2afe7fc3c702e1a55d7ceafac5f35199e8dc` |
| HMAC | `737870a` | 2026-07-10 | `9a17bb0290ef0facc1b39174d8ebf96578a99f2d091d019eef3ecfaebf40fd6b` | `23d328bdaf8da8b816c41b4a70f0f178468fd6c2a66990ee2f083b2496eabf52` |
| AES/HMAC | `0013390` | 2026-07-31 | `2cf5e6ec387461b4bf954f587ad4d957753fcbc48bf892b5e49996b90cf3b476` | `3eb1bd439947eb762998e566ccc2e099c791118b2f40579cc4f7da2b5061b7f9` |

The deploy workflow now runs `scripts/audit-secret-fingerprints.sh` against the active production JWT/AES/HMAC GitHub Environment secrets before rendering or deploying. The script prints only a status and fails on a known tracked fingerprint. The same audit is the first step of the manual `Production key audit and rotation` workflow.

A repository checkout still cannot read GitHub Environment secret values or a production database by itself. Database-backed `check` or `apply` must therefore run through the protected production workflow or by an operator with secret-manager, SSH, and database access.

### Compare active secrets without printing them

Load each value from the secret manager into an environment variable and run the policy audit without printing the values:

```bash
bash scripts/audit-secret-fingerprints.sh JWT_SECRET CRYPTO_AES_KEY CRYPTO_HMAC_KEY
```

For JWT the audit checks the tracked source fingerprint. For AES/HMAC it checks both the source fingerprint and, when the value is canonical base64, the SHA-256 fingerprint of the decoded bytes. `scripts/fingerprint-secret.sh` remains available when a source-only fingerprint is needed for a deployment record. Do not paste source values into tickets or logs. A JWT match means all currently issued JWTs must be treated as compromised and the JWT secret must be rotated. AES/HMAC usage in stored rows must additionally be checked with the database-backed command below.

## Generate replacement values

Generate every value independently and store it directly in the production secret manager:

```bash
openssl rand -hex 48       # JWT_SECRET
openssl rand -base64 32    # CRYPTO_AES_KEY
openssl rand -base64 48    # CRYPTO_HMAC_KEY
```

## Protected GitHub workflow

`.github/workflows/key-rotation.yml` provides a manual production operation with three modes:

- `audit` compares active JWT/AES/HMAC secret fingerprints without printing values;
- `check` runs the database-backed transaction and rolls it back after count-only inspection;
- `apply` requires the production environment approval, maintenance confirmation, backup confirmation, and the exact `ROTATE_CRYPTO_KEYS` confirmation string.

Configure the restricted production secrets `ROTATION_OLD_AES_KEY`, `ROTATION_OLD_HMAC_KEY`, `ROTATION_NEW_AES_KEY`, and `ROTATION_NEW_HMAC_KEY` before running `check` or `apply`. For database operations, also provide the `rotation_image` workflow input as the exact immutable GHCR digest reference, for example `ghcr.io/linka-masterskaya/zip-backend/server@sha256:<64 lowercase hex characters>`. The normal build workflow publishes this reference in its job summary immediately after pushing the server image, even when the later production deploy is blocked by the active-secret audit. The rotation workflow validates the reference, authenticates to GHCR, pulls that exact digest, and overrides only the `zip-backend` service image for the one-off rotation container. It does not depend on the current production `VERSION`. Preserve the run URL, image digest, and count-only output using `docs/security/rotation-report-template.md`.

## Database-backed preflight

Use a maintenance window and stop application writes so no row can be written with an old key between migration and restart. Export the old and replacement values from the secret manager without placing values in shell history:

```bash
export ROTATION_MODE=check
export ROTATION_IMAGE='ghcr.io/linka-masterskaya/zip-backend/server@sha256:<digest>'
export ROTATION_OLD_AES_KEY
export ROTATION_OLD_HMAC_KEY
export ROTATION_NEW_AES_KEY
export ROTATION_NEW_HMAC_KEY

override_file=$(mktemp)
trap 'rm -f "$override_file"' EXIT HUP INT TERM
printf 'services:\n  zip-backend:\n    image: %s\n' "$ROTATION_IMAGE" > "$override_file"
chmod 600 "$override_file"

# Authenticate first when the GHCR package is private.
docker pull "$ROTATION_IMAGE"
docker compose \
  --project-directory "$PWD" \
  -f docker-compose.server.yaml \
  -f "$override_file" \
  run --rm --no-deps \
  --entrypoint ./rotate-crypto \
  -e ROTATION_MODE \
  -e ROTATION_OLD_AES_KEY \
  -e ROTATION_OLD_HMAC_KEY \
  -e ROTATION_NEW_AES_KEY \
  -e ROTATION_NEW_HMAC_KEY \
  zip-backend
```

`check` starts one transaction, locks `auth_cred` and `students`, reports count-only fields, and rolls back. It aborts if any ciphertext cannot be decrypted by either candidate AES key or any `auth_cred.email_hash` matches neither candidate HMAC key. Preserve the count-only output in the deployment record.

Before opening a database connection, the rotation binary validates the replacement AES/HMAC pair with the same production policy used by normal application startup. Replacement keys are rejected when they are placeholders, documented development fixtures, match a previously tracked source representation or decoded key material, use malformed or non-canonical base64, have an incorrect size, or decode to the same bytes. Old keys are intentionally allowed to match known historical/development values so the utility can rotate away from them. For deployments that used the pre-base64 raw-key format, pass `ROTATION_OLD_*` as canonical base64 of the exact historical bytes; this changes only transport representation, not key material. A no-op invocation with identical old and new AES/HMAC pairs is also rejected. If more than one historical key pair was deployed in different environments, audit each environment against its actual active pair separately.

## Apply rotation

1. Stop the backend or enable maintenance mode.
2. Create and verify a PostgreSQL backup.
3. Run `check` and preserve its count-only output.
4. Run `apply` with the explicit confirmation variable:

```bash
export ROTATION_MODE=apply
export ROTATION_CONFIRM=ROTATE_CRYPTO_KEYS

# Reuse the same immutable ROTATION_IMAGE and override_file prepared for check.
docker compose \
  --project-directory "$PWD" \
  -f docker-compose.server.yaml \
  -f "$override_file" \
  run --rm --no-deps \
  --entrypoint ./rotate-crypto \
  -e ROTATION_MODE \
  -e ROTATION_CONFIRM \
  -e ROTATION_OLD_AES_KEY \
  -e ROTATION_OLD_HMAC_KEY \
  -e ROTATION_NEW_AES_KEY \
  -e ROTATION_NEW_HMAC_KEY \
  zip-backend
```

5. Update `JWT_SECRET`, `CRYPTO_AES_KEY`, and `CRYPTO_HMAC_KEY` in the production secret manager.
6. Restart the backend and verify `/readyz`, login by email, profile email reading, and student email reading.
7. Run the original old/new `check` again; all AES/HMAC counts must be reported on the new side.
8. Keep previous keys in restricted rollback storage until the rollback window closes.

The apply operation uses a single database transaction. It re-encrypts `auth_cred.email_encrypted` and `students.email_encrypted`, recalculates `auth_cred.email_hash`, rereads every row, verifies it with the replacement keys, and commits only after all checks succeed. The `auth_cred_email_hash_uniq` index stays active during the transaction.

JWT rotation is independent of the database operation. Restarting with a new JWT secret invalidates all access and refresh JWTs signed with the previous secret, so users must authenticate again.

## Rollback

If apply fails before commit, the transaction rolls back automatically and the running application must remain on the previous keys.

If apply commits but post-deploy verification fails:

1. Keep writes stopped.
2. Run `rotate-crypto` in `apply` mode with the replacement keys supplied as `ROTATION_OLD_*` and the previous keys supplied as `ROTATION_NEW_*`.
3. Restore the previous JWT/AES/HMAC values in the production secret manager.
4. Restart the previous application version and repeat login/profile/student readability checks.
5. Restore the database backup only if reverse rotation cannot complete.

`TestDatabaseRotationAndRollbackPreserveReadableData` applies real PostgreSQL migrations, rotates persisted `auth_cred` and `students` rows forward and backward, verifies authentication lookup through the production repository, verifies plaintext readability, and checks transaction rollback. `TestRotateAndRollbackPreserveReadableData` separately covers the key-rotation algorithm in memory.
