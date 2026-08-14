#!/usr/bin/env bash
set -euo pipefail

ENV_FILE=${1:-.env}

python3 - "$ENV_FILE" <<'PY'
from __future__ import annotations

import base64
import binascii
import hashlib
import re
import sys
from email.utils import parseaddr
from pathlib import Path
from urllib.parse import unquote, urlsplit


LEAKED_SOURCE_FINGERPRINTS = {
    "JWT_SECRET": {
        "f16dd3036e8e542ced86fe8bc25d7d1f6c4b1622f0321069c17600c12b59415b",
    },
    "CRYPTO_AES_KEY": {
        "861009ec4d599fab1f40abc76e6f89880cff5833c79c548c99f9045f191cd90b",
        "2cf5e6ec387461b4bf954f587ad4d957753fcbc48bf892b5e49996b90cf3b476",
    },
    "CRYPTO_HMAC_KEY": {
        "f6d527e6d01865481134f29788be2afe7fc3c702e1a55d7ceafac5f35199e8dc",
        "2cf5e6ec387461b4bf954f587ad4d957753fcbc48bf892b5e49996b90cf3b476",
    },
}

LEAKED_MATERIAL_FINGERPRINTS = {
    "CRYPTO_AES_KEY": {
        "861009ec4d599fab1f40abc76e6f89880cff5833c79c548c99f9045f191cd90b",
        "5f2560c1d6160f95c48ec63ef391d6993b70ceec9e2d9ad68dbab6286115bf0b",
        "3eb1bd439947eb762998e566ccc2e099c791118b2f40579cc4f7da2b5061b7f9",
    },
    "CRYPTO_HMAC_KEY": {
        "f6d527e6d01865481134f29788be2afe7fc3c702e1a55d7ceafac5f35199e8dc",
        "23d328bdaf8da8b816c41b4a70f0f178468fd6c2a66990ee2f083b2496eabf52",
        "3eb1bd439947eb762998e566ccc2e099c791118b2f40579cc4f7da2b5061b7f9",
    },
}


def fail(message: str) -> None:
    print(f"deployment environment validation failed: {message}", file=sys.stderr)
    raise SystemExit(1)


def decode_double_quoted(raw: str, line_number: int) -> tuple[str, str]:
    chars: list[str] = []
    index = 1
    escapes = {
        "a": "\a",
        "b": "\b",
        "c": "c",
        "f": "\f",
        "n": "\n",
        "r": "\r",
        "t": "\t",
        "v": "\v",
        "$": "$",
        '"': '"',
        "\\": "\\",
    }
    while index < len(raw):
        char = raw[index]
        if char == '"':
            return "".join(chars), raw[index + 1 :]
        if char == "$":
            if index + 1 < len(raw) and raw[index + 1] == "$":
                chars.append("$")
                index += 2
                continue
            fail(f"line {line_number} contains unescaped Compose interpolation")
        if char != "\\":
            chars.append(char)
            index += 1
            continue
        if index + 1 >= len(raw):
            fail(f"line {line_number} contains an unterminated escape")
        escaped = raw[index + 1]
        if escaped == "0":
            digits = ""
            cursor = index + 2
            while cursor < len(raw) and len(digits) < 3 and raw[cursor] in "01234567":
                digits += raw[cursor]
                cursor += 1
            chars.append(chr(int(digits or "0", 8)))
            index = cursor
            continue
        replacement = escapes.get(escaped)
        if replacement is None:
            chars.extend(("\\", escaped))
        else:
            chars.append(replacement)
        index += 2
    fail(f"line {line_number} contains an unterminated quoted value")
    return "", ""


def decode_single_quoted(raw: str, line_number: int) -> tuple[str, str]:
    chars: list[str] = []
    index = 1
    while index < len(raw):
        char = raw[index]
        if char == "'":
            return "".join(chars), raw[index + 1 :]
        if char == "\\" and index + 1 < len(raw) and raw[index + 1] == "'":
            chars.append("'")
            index += 2
            continue
        chars.append(char)
        index += 1
    fail(f"line {line_number} contains an unterminated quoted value")
    return "", ""


def decode_env_value(raw: str, line_number: int) -> str:
    value = raw.strip()
    if not value:
        return ""
    if value.startswith('"'):
        decoded, remainder = decode_double_quoted(value, line_number)
    elif value.startswith("'"):
        decoded, remainder = decode_single_quoted(value, line_number)
    else:
        decoded = value.split(" #", 1)[0].rstrip()
        remainder = ""
        if "$" in decoded:
            fail(f"line {line_number} contains unescaped Compose interpolation")
    remainder = remainder.strip()
    if remainder and not remainder.startswith("#"):
        fail(f"line {line_number} contains trailing data after a quoted value")
    return decoded


def load_env(path: Path) -> dict[str, str]:
    if not path.is_file():
        fail(f"{path} does not exist")

    values: dict[str, str] = {}
    key_pattern = re.compile(r"^[A-Z][A-Z0-9_]*$")
    for number, raw_line in enumerate(path.read_text(encoding="utf-8").splitlines(), start=1):
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("export "):
            line = line[7:]
        if "=" not in line:
            fail(f"line {number} is not KEY=VALUE")
        key, raw_value = line.split("=", 1)
        if not key_pattern.fullmatch(key):
            fail(f"line {number} contains an invalid key")
        if key in values:
            fail(f"{key} is defined more than once")
        values[key] = decode_env_value(raw_value, number)
    return values


def require(values: dict[str, str], key: str) -> str:
    value = values.get(key, "").strip()
    if not value:
        fail(f"{key} is required")
    return value


def reject_unsafe(key: str, value: str) -> None:
    placeholders = {
        "change-me",
        "changeme",
        "your-app-password",
        "replace-me",
        "replace_with_secret",
        "password",
    }
    dev_values = {
        "minioadmin",
        "linka-dev-minio",
        "linka-dev-minio-secret",
        "dev-only-jwt-secret-do-not-use-in-production-2026",
        "ZGV2LW9ubHktYWVzLWtleS1ub3QtZm9yLXByb2QhISE=",
        "ZGV2LW9ubHktaG1hYy1rZXktbm90LWZvci1wcm9kISE=",
        "dev-only-smtp-password",
        "dev-only-grafana-admin",
    }
    trimmed = value.strip()
    if trimmed.casefold() in placeholders or trimmed in dev_values:
        fail(f"{key} contains a placeholder or development credential")
    fingerprint = hashlib.sha256(trimmed.encode("utf-8")).hexdigest()
    if fingerprint in LEAKED_SOURCE_FINGERPRINTS.get(key, set()):
        fail(f"{key} contains a previously tracked credential")


def decode_key(key: str, value: str, exact_length: int | None) -> bytes:
    try:
        decoded = base64.b64decode(value, validate=True)
    except (binascii.Error, ValueError):
        fail(f"{key} must be valid base64")
    canonical = base64.b64encode(decoded).decode("ascii")
    if canonical != value:
        fail(f"{key} must use canonical base64 encoding")
    if exact_length is not None and len(decoded) != exact_length:
        fail(f"{key} must decode to exactly {exact_length} bytes")
    if exact_length is None and len(decoded) < 32:
        fail(f"{key} must decode to at least 32 bytes")
    material_fingerprint = hashlib.sha256(decoded).hexdigest()
    if material_fingerprint in LEAKED_MATERIAL_FINGERPRINTS.get(key, set()):
        fail(f"{key} contains previously tracked key material")
    return decoded


def parse_postgres(values: dict[str, str]) -> None:
    raw = require(values, "DB_URL")
    parsed = urlsplit(raw)
    if parsed.scheme not in {"postgres", "postgresql"} or not parsed.hostname:
        fail("DB_URL is invalid")
    if parsed.username is None or parsed.password is None:
        fail("DB_URL must contain a non-empty username and password")

    username = unquote(parsed.username)
    password = unquote(parsed.password)
    database = unquote(parsed.path.lstrip("/"))
    if not username or not password or not database or "/" in database:
        fail("DB_URL must contain a username, password, and database name")
    if username != require(values, "POSTGRES_USER"):
        fail("DB_URL username must match POSTGRES_USER")
    if password != require(values, "POSTGRES_PASSWORD"):
        fail("DB_URL password must match POSTGRES_PASSWORD")
    if database != require(values, "POSTGRES_DB"):
        fail("DB_URL database must match POSTGRES_DB")
    if password.casefold() in {"linka", "postgres", "pass"}:
        fail("POSTGRES_PASSWORD contains a known development credential")
    reject_unsafe("POSTGRES_PASSWORD", password)


def parse_redis(values: dict[str, str]) -> None:
    raw = require(values, "REDIS_URL")
    parsed = urlsplit(raw)
    if parsed.scheme not in {"redis", "rediss"} or not parsed.hostname:
        fail("REDIS_URL is invalid")
    if parsed.password is None:
        fail("REDIS_URL must contain a non-empty password")

    password = unquote(parsed.password)
    if not password:
        fail("REDIS_URL must contain a non-empty password")
    if password != require(values, "REDIS_PASSWORD"):
        fail("REDIS_URL password must match REDIS_PASSWORD")
    if password.casefold() in {"pass", "linka", "redis"}:
        fail("REDIS_PASSWORD contains a known development credential")
    reject_unsafe("REDIS_PASSWORD", password)


values = load_env(Path(sys.argv[1]))
if values.get("APP_ENV", "").strip().casefold() != "prod":
    fail("APP_ENV must be prod")

required_keys = (
    "CONFIG_PATH",
    "DB_URL",
    "POSTGRES_USER",
    "POSTGRES_PASSWORD",
    "POSTGRES_DB",
    "REDIS_URL",
    "REDIS_PASSWORD",
    "MINIO_ENDPOINT",
    "MINIO_ACCESS_KEY",
    "MINIO_SECRET_KEY",
    "JWT_SECRET",
    "CRYPTO_AES_KEY",
    "CRYPTO_HMAC_KEY",
    "SMTP_HOST",
    "SMTP_USERNAME",
    "SMTP_PASSWORD",
    "SMTP_FROM_EMAIL",
    "SMTP_REQUIRE_FROM_MATCH",
    "GRAFANA_ADMIN_PASSWORD",
    "CADDY_DOMAIN_NAME",
    "CADDY_LETSENCRYPT_EMAIL",
)
for required_key in required_keys:
    reject_unsafe(required_key, require(values, required_key))

parse_postgres(values)
parse_redis(values)

jwt_secret = require(values, "JWT_SECRET")
if len(jwt_secret) < 32:
    fail("JWT_SECRET must be at least 32 characters")
aes_key = decode_key("CRYPTO_AES_KEY", require(values, "CRYPTO_AES_KEY"), 32)
hmac_key = decode_key("CRYPTO_HMAC_KEY", require(values, "CRYPTO_HMAC_KEY"), None)
if aes_key == hmac_key:
    fail("CRYPTO_AES_KEY and CRYPTO_HMAC_KEY must be different")

smtp_host = require(values, "SMTP_HOST").casefold()
smtp_user = require(values, "SMTP_USERNAME")
smtp_from = require(values, "SMTP_FROM_EMAIL")
require_match = require(values, "SMTP_REQUIRE_FROM_MATCH").casefold()
if require_match not in {"true", "false"}:
    fail("SMTP_REQUIRE_FROM_MATCH must be true or false")
provider_requires_match = (
    "yandex." in smtp_host
    or smtp_host == "smtp.gmail.com"
    or smtp_host.endswith(".mail.ru")
)
smtp_user_address = parseaddr(smtp_user)[1] or smtp_user.strip()
smtp_from_address = parseaddr(smtp_from)[1] or smtp_from.strip()
if (
    require_match == "true" or provider_requires_match
) and smtp_user_address.casefold() != smtp_from_address.casefold():
    fail("SMTP_FROM_EMAIL must match SMTP_USERNAME for the configured provider")

print("deployment environment contract is valid")
PY
