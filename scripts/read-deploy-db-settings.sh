#!/usr/bin/env bash
set -euo pipefail

ENV_FILE=${1:-.env}

python3 - "$ENV_FILE" <<'PY'
from __future__ import annotations

import sys
from pathlib import Path


def fail(message: str) -> None:
    print(f"read deployment database settings failed: {message}", file=sys.stderr)
    raise SystemExit(1)


def decode_rendered_value(raw: str, line_number: int) -> str:
    value = raw.strip()
    if len(value) < 2 or not value.startswith('"') or not value.endswith('"'):
        fail(f"line {line_number} is not in rendered quoted format")
    chars: list[str] = []
    index = 1
    while index < len(value) - 1:
        char = value[index]
        if char == "$":
            if index + 1 < len(value) - 1 and value[index + 1] == "$":
                chars.append("$")
                index += 2
                continue
            fail(f"line {line_number} contains unescaped Compose interpolation")
        if char != "\\":
            chars.append(char)
            index += 1
            continue
        if index + 1 >= len(value) - 1:
            fail(f"line {line_number} contains an unterminated escape")
        escaped = value[index + 1]
        if escaped not in {'\\', '"', '$'}:
            fail(f"line {line_number} contains an unsupported escape")
        chars.append(escaped)
        index += 2
    return "".join(chars)


path = Path(sys.argv[1])
if not path.is_file():
    fail(f"{path} does not exist")

wanted = {"POSTGRES_USER", "POSTGRES_DB"}
values: dict[str, str] = {}
for line_number, raw_line in enumerate(path.read_text(encoding="utf-8").splitlines(), start=1):
    if "=" not in raw_line:
        continue
    key, raw_value = raw_line.split("=", 1)
    if key in wanted:
        if key in values:
            fail(f"{key} is defined more than once")
        values[key] = decode_rendered_value(raw_value, line_number)

for key in ("POSTGRES_USER", "POSTGRES_DB"):
    value = values.get(key, "")
    if not value:
        fail(f"{key} is empty or unavailable")
    print(value)
PY
