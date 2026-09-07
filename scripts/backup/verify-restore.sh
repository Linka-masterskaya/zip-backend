#!/usr/bin/env sh
# Проверяет восстановленные PostgreSQL и MinIO.
#
# Проверка состоит из двух частей:
#   1. каждый объект из backup manifest существует и сохранил размер/MIME;
#   2. все MinIO-ссылки из БД присутствуют в выбранном backup и в бакете.
#
# Аргумент - каталог, в который выполнен `restic restore`. Если аргумент не
# передан, используется стандартный /backup-tmp/restore.
set -eu
set -o pipefail

: "${POSTGRES_USER:?POSTGRES_USER is required}"
: "${POSTGRES_PASSWORD:?POSTGRES_PASSWORD is required}"
: "${POSTGRES_DB:?POSTGRES_DB is required}"
: "${MINIO_ACCESS_KEY:?MINIO_ACCESS_KEY is required}"
: "${MINIO_SECRET_KEY:?MINIO_SECRET_KEY is required}"
: "${MINIO_BUCKET:?MINIO_BUCKET is required}"

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
restore_root=${1:-/backup-tmp/restore}
manifest_file="$restore_root/backup-mirror/${MINIO_BUCKET}.metadata.jsonl"

if [ ! -f "$manifest_file" ]; then
  echo "MinIO metadata manifest not found: $manifest_file" >&2
  exit 1
fi

work_dir=$(mktemp -d)
cleanup() {
  rm -rf "$work_dir"
}
trap cleanup EXIT

errors=0

report_error() {
  echo "ОШИБКА: $1" >&2
  errors=$((errors + 1))
}

psql_query() {
  PGPASSWORD="$POSTGRES_PASSWORD" psql \
    -h "${POSTGRES_HOST:-postgres}" \
    -p "${POSTGRES_PORT:-5432}" \
    -U "$POSTGRES_USER" \
    -d "$POSTGRES_DB" \
    -A -t -F '|' -c "$1"
}

object_stat() {
  "$SCRIPT_DIR/mc.sh" stat --json "linka/$MINIO_BUCKET/$1" 2>/dev/null
}

stat_size() {
  printf '%s\n' "$1" | jq -er '.size'
}

stat_content_type() {
  printf '%s\n' "$1" | jq -er '
    .metadata["Content-Type"]
    // .metadata["content-type"]
    // .contentType
    // "application/octet-stream"
  '
}

verify_object() {
  key=$1
  expected_size=${2:-}
  expected_content_type=${3:-}
  source=$4

  if ! stat_json=$(object_stat "$key"); then
    report_error "$source ссылается на отсутствующий объект: $key"
    return
  fi

  actual_size=$(stat_size "$stat_json")
  actual_content_type=$(stat_content_type "$stat_json")

  if [ -n "$expected_size" ] && [ "$actual_size" -ne "$expected_size" ]; then
    report_error "$source: размер $key равен $actual_size, ожидался $expected_size"
  fi

  if [ -n "$expected_content_type" ] && [ "$actual_content_type" != "$expected_content_type" ]; then
    report_error "$source: Content-Type $key равен $actual_content_type, ожидался $expected_content_type"
  fi
}

verify_db_reference() {
  key=$1
  expected_size=${2:-}
  expected_content_type=${3:-}
  source=$4

  if ! grep -Fqx -- "$key" "$work_dir/manifest_keys.txt"; then
    report_error "$source ссылается на объект, которого нет в выбранном MinIO snapshot: $key"
  fi

  printf '%s\n' "$key" >> "$work_dir/db_keys.txt"
  verify_object "$key" "$expected_size" "$expected_content_type" "$source"
}

echo "== Проверка восстановленных данных =="
echo ""

# --- PostgreSQL: ключевые таблицы -----------------------------------------
users_count=$(psql_query "SELECT count(*) FROM users")
students_count=$(psql_query "SELECT count(*) FROM students")
packs_count=$(psql_query "SELECT count(*) FROM packs")
media_count=$(psql_query "SELECT count(*) FROM media_files")

echo "PostgreSQL:"
echo "  users:       $users_count"
echo "  students:    $students_count"
echo "  packs:       $packs_count"
echo "  media_files: $media_count"
echo ""

if [ "$users_count" = "0" ] || [ "$packs_count" = "0" ]; then
  report_error "база восстановлена, но ключевые таблицы пусты"
fi

# --- Manifest: размер и Content-Type каждого восстановленного объекта -----
: > "$work_dir/manifest_keys.txt"
: > "$work_dir/db_keys.txt"

manifest_count=0
while IFS= read -r record || [ -n "$record" ]; do
  key=$(printf '%s\n' "$record" | jq -er '.key')
  expected_size=$(printf '%s\n' "$record" | jq -er '.size')
  expected_content_type=$(printf '%s\n' "$record" | jq -er '.content_type')

  printf '%s\n' "$key" >> "$work_dir/manifest_keys.txt"
  verify_object "$key" "$expected_size" "$expected_content_type" "backup manifest"
  manifest_count=$((manifest_count + 1))
done < "$manifest_file"

sort -u "$work_dir/manifest_keys.txt" -o "$work_dir/manifest_keys.txt"
unique_manifest_count=$(wc -l < "$work_dir/manifest_keys.txt" | tr -d ' ')
if [ "$manifest_count" -ne "$unique_manifest_count" ]; then
  report_error "manifest содержит повторяющиеся MinIO keys"
fi

# --- Ссылки из БД ---------------------------------------------------------
psql_query "SELECT minio_key, size_bytes, mime_type FROM media_files ORDER BY minio_key" \
  > "$work_dir/media_files.txt"
while IFS='|' read -r key expected_size expected_content_type; do
  [ -z "$key" ] || verify_db_reference \
    "$key" "$expected_size" "$expected_content_type" "media_files"
done < "$work_dir/media_files.txt"

psql_query "SELECT avatar_key FROM users WHERE avatar_key IS NOT NULL AND avatar_key <> '' ORDER BY avatar_key" \
  > "$work_dir/avatars.txt"
while IFS= read -r key; do
  [ -z "$key" ] || verify_db_reference "$key" "" "" "users.avatar_key"
done < "$work_dir/avatars.txt"

psql_query "SELECT minio_key, size_bytes FROM audio_bank ORDER BY minio_key" \
  > "$work_dir/audio_bank.txt"
while IFS='|' read -r key expected_size; do
  [ -z "$key" ] || verify_db_reference "$key" "$expected_size" "" "audio_bank"
done < "$work_dir/audio_bank.txt"

psql_query "SELECT minio_key, size_bytes FROM tts_jobs WHERE minio_key IS NOT NULL ORDER BY minio_key" \
  > "$work_dir/tts_jobs.txt"
while IFS='|' read -r key expected_size; do
  [ -z "$key" ] || verify_db_reference "$key" "$expected_size" "" "tts_jobs"
done < "$work_dir/tts_jobs.txt"

# Дополнительные объекты в бакете не считаются ошибкой, но показываются как
# возможный мусор после удалённых записей.
sort -u "$work_dir/db_keys.txt" -o "$work_dir/db_keys.txt"
"$SCRIPT_DIR/mc.sh" find "linka/$MINIO_BUCKET" --print "{}" 2>/dev/null \
  | sed "s|^linka/$MINIO_BUCKET/||" \
  | sort -u > "$work_dir/minio_keys.txt"
orphan_count=$(comm -13 "$work_dir/db_keys.txt" "$work_dir/minio_keys.txt" | wc -l | tr -d ' ')

echo "MinIO:"
echo "  объектов в manifest: $manifest_count"
echo "  объектов без ссылки из БД (не ошибка): $orphan_count"
echo ""

if [ "$errors" -ne 0 ]; then
  echo "Проверка восстановления завершилась с ошибками: $errors" >&2
  exit 1
fi

echo "OK: размеры и Content-Type восстановлены, ссылки из БД разрешаются."
