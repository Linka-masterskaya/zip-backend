#!/usr/bin/env sh
# Восстанавливает MinIO из файлового зеркала и manifest метаданных.
#
# Аргумент - каталог, в который выполнен `restic restore`. Если аргумент не
# передан, используется стандартный /backup-tmp/restore.
set -eu
set -o pipefail

: "${MINIO_ACCESS_KEY:?MINIO_ACCESS_KEY is required}"
: "${MINIO_SECRET_KEY:?MINIO_SECRET_KEY is required}"
: "${MINIO_BUCKET:?MINIO_BUCKET is required}"

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
restore_root=${1:-/backup-tmp/restore}
mirror_dir="$restore_root/backup-mirror/$MINIO_BUCKET"
manifest_file="$restore_root/backup-mirror/${MINIO_BUCKET}.metadata.jsonl"

if [ ! -d "$mirror_dir" ]; then
  echo "restored MinIO files not found: $mirror_dir" >&2
  exit 1
fi

if [ ! -f "$manifest_file" ]; then
  echo "MinIO metadata manifest not found: $manifest_file" >&2
  exit 1
fi

"$SCRIPT_DIR/mc.sh" mb --ignore-existing "linka/$MINIO_BUCKET" >/dev/null

restored=0
while IFS= read -r record || [ -n "$record" ]; do
  key=$(printf '%s\n' "$record" | jq -er '.key')
  expected_size=$(printf '%s\n' "$record" | jq -er '.size')
  content_type=$(printf '%s\n' "$record" | jq -er '.content_type')

  case "$key" in
    ""|/*|../*|*/../*|*/..)
      echo "unsafe object key in manifest: $key" >&2
      exit 1
      ;;
  esac

  local_file="$mirror_dir/$key"
  if [ ! -f "$local_file" ]; then
    echo "restored file is missing: $key" >&2
    exit 1
  fi

  actual_size=$(wc -c < "$local_file" | tr -d ' ')
  if [ "$actual_size" -ne "$expected_size" ]; then
    echo "restored file size mismatch: $key (expected $expected_size, got $actual_size)" >&2
    exit 1
  fi

  "$SCRIPT_DIR/mc.sh" cp \
    --attr "Content-Type=$content_type" \
    "$local_file" "linka/$MINIO_BUCKET/$key"
  restored=$((restored + 1))
done < "$manifest_file"

echo "MinIO restore ok: $restored objects uploaded with metadata"
