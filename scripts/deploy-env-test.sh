#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

fail() {
  printf 'deploy environment test failed: %s\n' "$1" >&2
  exit 1
}

expect_invalid() {
  local file=$1
  local reason=$2
  if bash "$ROOT_DIR/scripts/validate-deploy-env.sh" "$file" >/dev/null 2>&1; then
    fail "$reason was accepted"
  fi
}

url_quote() {
  python3 - "$1" <<'PY'
import sys
from urllib.parse import quote
print(quote(sys.argv[1], safe=""))
PY
}

while IFS='=' read -r key value; do
  case "$key" in
    ''|'#'*) ;;
    *) export "CI_${key}=${value}" ;;
  esac
done < "$ROOT_DIR/.env.example"

export CI_APP_ENV=prod
export CI_CONFIG_PATH=config/config.prod.yml
export CI_DB_URL='postgres://prod_user:prod%40db%3Apassword_123456@postgres:5432/prod_db?sslmode=require'
export CI_POSTGRES_USER=prod_user
export CI_POSTGRES_PASSWORD='prod@db:password_123456'
export CI_POSTGRES_DB=prod_db
export CI_REDIS_URL='redis://:prod%40redis%3Apassword_123456@redis:6379/0'
export CI_REDIS_PASSWORD='prod@redis:password_123456'
export CI_MINIO_ENDPOINT=minio:9000
export CI_MINIO_ACCESS_KEY=prod_minio_access_123456
export CI_MINIO_SECRET_KEY=test-only-minio-secret-aaaaaaaa
export CI_JWT_SECRET=test-only-jwt-secret-aaaaaaaaaaaaaaaaaaaaaaaaaaaa
export CI_CRYPTO_AES_KEY=YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE=
export CI_CRYPTO_HMAC_KEY=YmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJi
export CI_SMTP_HOST=smtp.yandex.ru
export CI_SMTP_USERNAME=noreply@example.com
export CI_SMTP_PASSWORD=prod_smtp_password_123456789
export CI_SMTP_FROM_EMAIL=noreply@example.com
export CI_SMTP_REQUIRE_FROM_MATCH=true
export CI_GRAFANA_ADMIN_PASSWORD=test-only-grafana-password-aaaaaaaa
export CI_CADDY_DOMAIN_NAME=example.com
export CI_CADDY_LETSENCRYPT_EMAIL=admin@example.com

VALID_ENV="$TMP_DIR/valid.env"
bash "$ROOT_DIR/scripts/render-deploy-env.sh" "$ROOT_DIR/.env.example" "$VALID_ENV" CI_
bash "$ROOT_DIR/scripts/validate-deploy-env.sh" "$VALID_ENV" >/dev/null
[ "$(stat -c '%a' "$VALID_ENV")" = "600" ] || fail "rendered file permissions are not 0600"
! grep -Fq 'dev-only-jwt-secret' "$VALID_ENV" || fail "renderer retained a development JWT fixture"
grep -Eq "^JWT_SECRET='.*'$" "$VALID_ENV" || fail "renderer did not quote values"
mapfile -t db_settings < <(bash "$ROOT_DIR/scripts/read-deploy-db-settings.sh" "$VALID_ENV")
[ "${db_settings[0]}" = "$CI_POSTGRES_USER" ] || fail "POSTGRES_USER reader changed the value"
[ "${db_settings[1]}" = "$CI_POSTGRES_DB" ] || fail "POSTGRES_DB reader changed the value"

export FINGERPRINT_TEST_SECRET=do-not-print-this-secret
fingerprint_output=$(bash "$ROOT_DIR/scripts/fingerprint-secret.sh" FINGERPRINT_TEST_SECRET)
[[ "$fingerprint_output" == FINGERPRINT_TEST_SECRET=sha256:* ]] || fail "fingerprint output is malformed"
[[ "$fingerprint_output" != *do-not-print-this-secret* ]] || fail "fingerprint helper printed the secret"

export JWT_SECRET=safe-test-jwt-secret-not-tracked-1234567890
export CRYPTO_AES_KEY=safe-test-aes-secret-not-tracked
export CRYPTO_HMAC_KEY=safe-test-hmac-secret-not-tracked
bash "$ROOT_DIR/scripts/audit-secret-fingerprints.sh" \
  JWT_SECRET CRYPTO_AES_KEY CRYPTO_HMAC_KEY >/dev/null
if bash "$ROOT_DIR/scripts/audit-secret-fingerprints.sh" --fingerprint JWT_SECRET \
  f16dd3036e8e542ced86fe8bc25d7d1f6c4b1622f0321069c17600c12b59415b \
  >/dev/null 2>&1; then
  fail "historical fingerprint was not rejected"
fi

# `$$` is reserved by Compose as an escape sequence during model interpolation.
# Use a literal dollar followed by a non-variable character so the fixture still
# exercises dollar preservation without asserting byte-transparency for `$$`.
SPECIAL_PASSWORD='pa$! word#fragment'"'"'quote\backslash"double'
SPECIAL_JWT='jwt-pa$! word#fragment'"'"'quote\backslash"double-12345678901234567890'
encoded_postgres=$(url_quote "$SPECIAL_PASSWORD")
encoded_redis=$(url_quote "$SPECIAL_PASSWORD")
export CI_DB_URL="postgres://prod_user:${encoded_postgres}@postgres:5432/prod_db?sslmode=require"
export CI_POSTGRES_PASSWORD="$SPECIAL_PASSWORD"
export CI_REDIS_URL="redis://:${encoded_redis}@redis:6379/0"
export CI_REDIS_PASSWORD="$SPECIAL_PASSWORD"
export CI_JWT_SECRET="$SPECIAL_JWT"
export CI_SMTP_PASSWORD="$SPECIAL_PASSWORD"
export CI_GRAFANA_ADMIN_PASSWORD="$SPECIAL_PASSWORD"

SPECIAL_ENV="$TMP_DIR/special.env"
bash "$ROOT_DIR/scripts/render-deploy-env.sh" "$ROOT_DIR/.env.example" "$SPECIAL_ENV" CI_
bash "$ROOT_DIR/scripts/validate-deploy-env.sh" "$SPECIAL_ENV" >/dev/null
grep -Fq '$! word#fragment' "$SPECIAL_ENV" || fail "dollar sign was not preserved for Compose"
grep -Fq "\\'" "$SPECIAL_ENV" || fail "single quote was not escaped for Compose"
grep -Fq '\backslash"double' "$SPECIAL_ENV" || fail "backslash or double quote was not preserved for Compose"

if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  printf 'ROUNDTRIP=\n' > "$TMP_DIR/roundtrip.template"
  export CI_ROUNDTRIP="$SPECIAL_PASSWORD"
  bash "$ROOT_DIR/scripts/render-deploy-env.sh" \
    "$TMP_DIR/roundtrip.template" "$TMP_DIR/roundtrip.env" CI_
  cat > "$TMP_DIR/compose.yaml" <<'YAML'
services:
  check:
    image: busybox:1.36
    environment:
      ROUNDTRIP: ${ROUNDTRIP}
YAML
  docker compose --env-file "$TMP_DIR/roundtrip.env" \
    -f "$TMP_DIR/compose.yaml" config --format json > "$TMP_DIR/compose.json"
  EXPECTED_ROUNDTRIP="$SPECIAL_PASSWORD" python3 - "$TMP_DIR/compose.json" <<'PY'
import json
import os
import sys

with open(sys.argv[1], encoding="utf-8") as source:
    config = json.load(source)
actual = config["services"]["check"]["environment"]["ROUNDTRIP"]
expected = os.environ["EXPECTED_ROUNDTRIP"]
if actual != expected:
    raise SystemExit(
        f"Docker Compose changed the rendered fixture value: expected={expected!r}, actual={actual!r}"
    )
PY
fi

# Restore ordinary valid values for mutation-based negative tests.
export CI_DB_URL='postgres://prod_user:prod%40db%3Apassword_123456@postgres:5432/prod_db?sslmode=require'
export CI_POSTGRES_PASSWORD='prod@db:password_123456'
export CI_REDIS_URL='redis://:prod%40redis%3Apassword_123456@redis:6379/0'
export CI_REDIS_PASSWORD='prod@redis:password_123456'
export CI_JWT_SECRET=test-only-jwt-secret-aaaaaaaaaaaaaaaaaaaaaaaaaaaa
export CI_SMTP_PASSWORD=prod_smtp_password_123456789
export CI_GRAFANA_ADMIN_PASSWORD=test-only-grafana-password-aaaaaaaa
bash "$ROOT_DIR/scripts/render-deploy-env.sh" "$ROOT_DIR/.env.example" "$VALID_ENV" CI_


LEGACY_TRACKED_CRYPTO=$(python3 - <<'PY'
import base64
print(base64.b64encode((b"0123456789abcdef" * 2)).decode("ascii"))
PY
)
LEGACY_AES="$TMP_DIR/legacy-aes.env"
cp "$VALID_ENV" "$LEGACY_AES"
sed -i "s|^CRYPTO_AES_KEY=.*|CRYPTO_AES_KEY=\"$LEGACY_TRACKED_CRYPTO\"|" "$LEGACY_AES"
expect_invalid "$LEGACY_AES" "historical shared credential as AES"

LEGACY_HMAC="$TMP_DIR/legacy-hmac.env"
cp "$VALID_ENV" "$LEGACY_HMAC"
sed -i "s|^CRYPTO_HMAC_KEY=.*|CRYPTO_HMAC_KEY=\"$LEGACY_TRACKED_CRYPTO\"|" "$LEGACY_HMAC"
expect_invalid "$LEGACY_HMAC" "historical shared credential as HMAC"

if bash "$ROOT_DIR/scripts/audit-secret-fingerprints.sh" --fingerprint CRYPTO_AES_KEY \
  2cf5e6ec387461b4bf954f587ad4d957753fcbc48bf892b5e49996b90cf3b476 \
  >/dev/null 2>&1; then
  fail "shared historical AES fingerprint was not rejected"
fi
if bash "$ROOT_DIR/scripts/audit-secret-fingerprints.sh" --fingerprint CRYPTO_HMAC_KEY \
  2cf5e6ec387461b4bf954f587ad4d957753fcbc48bf892b5e49996b90cf3b476 \
  >/dev/null 2>&1; then
  fail "shared historical HMAC fingerprint was not rejected"
fi

LEGACY_RAW_AES_B64=$(python3 - <<'PY_AES'
import base64
print(base64.b64encode((b"0123456789" * 3) + b"01").decode("ascii"))
PY_AES
)
LEGACY_RAW_HMAC_B64=$(python3 - <<'PY_HMAC'
import base64
print(base64.b64encode(b"abcdefghijklmnopqrstuvwxyz" + b"123456").decode("ascii"))
PY_HMAC
)

LEGACY_RAW_AES_ENV="$TMP_DIR/legacy-raw-material-aes.env"
cp "$VALID_ENV" "$LEGACY_RAW_AES_ENV"
sed -i "s|^CRYPTO_AES_KEY=.*|CRYPTO_AES_KEY=\"$LEGACY_RAW_AES_B64\"|" "$LEGACY_RAW_AES_ENV"
expect_invalid "$LEGACY_RAW_AES_ENV" "base64-encoded historical raw AES material"

LEGACY_RAW_HMAC_ENV="$TMP_DIR/legacy-raw-material-hmac.env"
cp "$VALID_ENV" "$LEGACY_RAW_HMAC_ENV"
sed -i "s|^CRYPTO_HMAC_KEY=.*|CRYPTO_HMAC_KEY=\"$LEGACY_RAW_HMAC_B64\"|" "$LEGACY_RAW_HMAC_ENV"
expect_invalid "$LEGACY_RAW_HMAC_ENV" "base64-encoded historical raw HMAC material"

export CRYPTO_AES_KEY="$LEGACY_RAW_AES_B64"
if bash "$ROOT_DIR/scripts/audit-secret-fingerprints.sh" CRYPTO_AES_KEY >/dev/null 2>&1; then
  fail "audit accepted base64-encoded historical raw AES material"
fi
export CRYPTO_HMAC_KEY="$LEGACY_RAW_HMAC_B64"
if bash "$ROOT_DIR/scripts/audit-secret-fingerprints.sh" CRYPTO_HMAC_KEY >/dev/null 2>&1; then
  fail "audit accepted base64-encoded historical raw HMAC material"
fi
unset CRYPTO_AES_KEY CRYPTO_HMAC_KEY

NONCANONICAL_AES="$TMP_DIR/noncanonical-aes.env"
cp "$VALID_ENV" "$NONCANONICAL_AES"
sed -i 's|^CRYPTO_AES_KEY=.*|CRYPTO_AES_KEY="YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWF="|' "$NONCANONICAL_AES"
expect_invalid "$NONCANONICAL_AES" "non-canonical AES base64"

NONCANONICAL_HMAC="$TMP_DIR/noncanonical-hmac.env"
cp "$VALID_ENV" "$NONCANONICAL_HMAC"
sed -i 's|^CRYPTO_HMAC_KEY=.*|CRYPTO_HMAC_KEY="YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWF="|' "$NONCANONICAL_HMAC"
expect_invalid "$NONCANONICAL_HMAC" "non-canonical HMAC base64"

UPPERCASE_PLACEHOLDER="$TMP_DIR/uppercase-placeholder.env"
cp "$VALID_ENV" "$UPPERCASE_PLACEHOLDER"
sed -i 's/^GRAFANA_ADMIN_PASSWORD=.*/GRAFANA_ADMIN_PASSWORD="CHANGE-ME"/' "$UPPERCASE_PLACEHOLDER"
expect_invalid "$UPPERCASE_PLACEHOLDER" "uppercase placeholder"

DB_MISMATCH="$TMP_DIR/db-mismatch.env"
cp "$VALID_ENV" "$DB_MISMATCH"
sed -i 's/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD="another_prod_password_123456"/' "$DB_MISMATCH"
expect_invalid "$DB_MISMATCH" "DB_URL and POSTGRES_PASSWORD mismatch"

REDIS_MISMATCH="$TMP_DIR/redis-mismatch.env"
cp "$VALID_ENV" "$REDIS_MISMATCH"
sed -i 's/^REDIS_PASSWORD=.*/REDIS_PASSWORD="another_redis_password_123456"/' "$REDIS_MISMATCH"
expect_invalid "$REDIS_MISMATCH" "REDIS_URL and REDIS_PASSWORD mismatch"

SMTP_MISMATCH="$TMP_DIR/smtp-mismatch.env"
cp "$VALID_ENV" "$SMTP_MISMATCH"
sed -i 's/^SMTP_FROM_EMAIL=.*/SMTP_FROM_EMAIL="other@example.com"/' "$SMTP_MISMATCH"
expect_invalid "$SMTP_MISMATCH" "SMTP sender mismatch"

UNESCAPED_INTERPOLATION="$TMP_DIR/unescaped-interpolation.env"
cp "$VALID_ENV" "$UNESCAPED_INTERPOLATION"
sed -i 's/^SMTP_PASSWORD=.*/SMTP_PASSWORD="$UNSET_SECRET"/' "$UNESCAPED_INTERPOLATION"
expect_invalid "$UNESCAPED_INTERPOLATION" "unescaped Compose interpolation"

unset CI_VERSION
if bash "$ROOT_DIR/scripts/render-deploy-env.sh" "$ROOT_DIR/.env.example" "$TMP_DIR/missing.env" CI_ >/dev/null 2>&1; then
  fail "renderer accepted a missing contract variable"
fi

printf 'deployment environment scripts are valid\n'
