#!/usr/bin/env bash
set -euo pipefail

python3 - "$@" <<'PY'
from __future__ import annotations

import base64
import binascii
import hashlib
import os
import re
import sys

KNOWN = {
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

MATERIAL = {
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


def fail(message: str, code: int = 1) -> None:
    print(message, file=sys.stderr)
    raise SystemExit(code)


def validate_name(name: str) -> None:
    if name not in KNOWN or not re.fullmatch(r"[A-Z][A-Z0-9_]*", name):
        fail(f"unsupported secret name: {name}", 2)


def report(name: str, fingerprint: str) -> bool:
    validate_name(name)
    matched = fingerprint.casefold() in KNOWN[name]
    status = "KNOWN_TRACKED_CREDENTIAL" if matched else "not-known-tracked"
    print(f"{name}={status}")
    return matched


arguments = sys.argv[1:]
report_only = False
if arguments and arguments[0] == "--report-only":
    report_only = True
    arguments = arguments[1:]
if len(arguments) == 3 and arguments[0] == "--fingerprint":
    secret_name, supplied = arguments[1], arguments[2]
    if not re.fullmatch(r"[0-9a-fA-F]{64}", supplied):
        fail("fingerprint must contain exactly 64 hexadecimal characters", 2)
    matched = report(secret_name, supplied)
    raise SystemExit(0 if report_only else (1 if matched else 0))
if not arguments:
    fail("usage: audit-secret-fingerprints.sh SECRET_NAME [...]", 2)

matched_any = False
for secret_name in arguments:
    validate_name(secret_name)
    value = os.environ.get(secret_name)
    if value is None or value == "":
        fail(f"{secret_name} is empty or unavailable")
    source_fingerprint = hashlib.sha256(value.encode("utf-8")).hexdigest()
    matched = source_fingerprint in KNOWN[secret_name]
    if secret_name in MATERIAL:
        try:
            decoded = base64.b64decode(value, validate=True)
        except (binascii.Error, ValueError):
            decoded = None
        if decoded is not None and base64.b64encode(decoded).decode("ascii") == value:
            material_fingerprint = hashlib.sha256(decoded).hexdigest()
            matched = matched or material_fingerprint in MATERIAL[secret_name]
    status = "KNOWN_TRACKED_CREDENTIAL" if matched else "not-known-tracked"
    print(f"{secret_name}={status}")
    matched_any = matched or matched_any
raise SystemExit(0 if report_only else (1 if matched_any else 0))
PY
