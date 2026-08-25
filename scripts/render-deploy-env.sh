#!/usr/bin/env bash
set -euo pipefail

TEMPLATE=${1:-.env.example}
OUTPUT=${2:-.env}
PREFIX=${3:-CI_}

python3 - "$TEMPLATE" "$OUTPUT" "$PREFIX" <<'PY'
from __future__ import annotations

import os
import re
import sys
import tempfile
from pathlib import Path


def fail(message: str) -> None:
    print(f"deployment environment render failed: {message}", file=sys.stderr)
    raise SystemExit(1)


def compose_quote(value: str, source_key: str) -> str:
    if "\x00" in value or "\n" in value or "\r" in value:
        fail(f"{source_key} contains a forbidden control character")

    # Compose treats single-quoted .env values literally. This prevents `$`
    # interpolation while preserving backslashes and double quotes. Only an
    # embedded single quote needs escaping in this format.
    escaped = value.replace("'", "\\'")
    return f"'{escaped}'"


template = Path(sys.argv[1])
output = Path(sys.argv[2])
prefix = sys.argv[3]
if not template.is_file():
    fail(f"{template} does not exist")

key_pattern = re.compile(r"^([A-Z][A-Z0-9_]*)=(.*)$")
rendered: list[str] = []
missing: list[str] = []
empty: list[str] = []
for raw_line in template.read_text(encoding="utf-8").splitlines():
    match = key_pattern.match(raw_line)
    if not match:
        rendered.append(raw_line)
        continue
    key = match.group(1)
    source_key = prefix + key
    if source_key not in os.environ:
        missing.append(source_key)
        rendered.append(raw_line)
        continue
    if not os.environ[source_key]:
        empty.append(source_key)
        rendered.append(raw_line)
        continue
    rendered.append(f"{key}={compose_quote(os.environ[source_key], source_key)}")

if missing or empty:
    parts = []
    if missing:
        parts.append("missing: " + ", ".join(missing))
    if empty:
        parts.append("empty: " + ", ".join(empty))
    fail("; ".join(parts))

output.parent.mkdir(parents=True, exist_ok=True)
fd, temporary_name = tempfile.mkstemp(prefix=output.name + ".", dir=output.parent, text=True)
try:
    with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as temporary:
        temporary.write("\n".join(rendered) + "\n")
    os.chmod(temporary_name, 0o600)
    os.replace(temporary_name, output)
except BaseException:
    try:
        os.unlink(temporary_name)
    except FileNotFoundError:
        pass
    raise
PY
