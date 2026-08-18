#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 /path/to/linka.looks-electron-v3.2.10/src/common/interfaces/ConfigFile.ts" >&2
  exit 2
fi

ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
DIR="$ROOT/docs/compatibility/linka-looks"
FIXTURE="$DIR/testdata/backend-v2-export.linka"
TMP_OFFICIAL="$(mktemp)"
TMP_ROUNDTRIP="$(mktemp)"
trap 'rm -f "$TMP_OFFICIAL" "$TMP_ROUNDTRIP"' EXIT

cd "$ROOT"
go test ./internal/pack -run '^TestLinkaLooksCompatibilityFixture$' -count=1
node "$DIR/verify-official-parser.mjs" "$1" "$FIXTURE" > "$TMP_OFFICIAL"
diff -u "$DIR/testdata/looks-v3.2.10-official-parser-run.json" "$TMP_OFFICIAL"
node "$DIR/looks-v3.2.10-harness.mjs" "$FIXTURE" > "$TMP_ROUNDTRIP"
diff -u "$DIR/testdata/looks-v3.2.10-run.json" "$TMP_ROUNDTRIP"

echo "N5 compatibility checks passed"
