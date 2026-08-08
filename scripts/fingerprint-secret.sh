#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 1 ]; then
  printf 'usage: %s ENVIRONMENT_VARIABLE_NAME\n' "$0" >&2
  exit 2
fi

python3 - "$1" <<'PY'
import hashlib
import os
import re
import sys

name = sys.argv[1]
if not re.fullmatch(r"[A-Z][A-Z0-9_]*", name):
    print("invalid environment variable name", file=sys.stderr)
    raise SystemExit(2)
value = os.environ.get(name)
if value is None or value == "":
    print(f"{name} is empty or unavailable", file=sys.stderr)
    raise SystemExit(1)
fingerprint = hashlib.sha256(value.encode("utf-8")).hexdigest()
print(f"{name}=sha256:{fingerprint}")
PY
