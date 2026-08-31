#!/usr/bin/env sh
# Регулярный backup объектов MinIO.
#
#   mc mirror + manifest метаданных -> restic -> каталог копий
#
# Запускается по расписанию из контейнера `backup` (см. deploy/backup/crontab).
#
# Зеркало по умолчанию удаляется после успешной копии: на сервере один
# небольшой раздел, и держать между запусками ещё одну копию бакета дорого.
#
# Объекты копируются каждые 6 часов, база — каждый час. Разная частота
# осознанна: при аварии только в базе её восстанавливают отдельно, MinIO не
# трогают, и потеря равна интервалу копий базы. При полной потере нужна
# согласованная пара, и снимок медиа должен быть не старше снимка базы -
# иначе в media_files окажутся строки без объектов, то есть битые ссылки.
set -eu
set -o pipefail

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=scripts/backup/metrics.sh
. "$SCRIPT_DIR/metrics.sh"

target="minio"
started_at=$(backup_now)
bytes=0
mirror_root=/backup-mirror
mirror_dir=""
manifest_file=""
backup_succeeded=0

finish() {
  status=$1
  finished_at=$(backup_now)
  duration=$((finished_at - started_at))

  if [ "$status" = "0" ]; then
    backup_push_metrics "$target" 1 "$duration" "$bytes" || true
    echo "minio backup ok: $bytes bytes in ${duration}s"
  else
    backup_push_metrics "$target" 0 "$duration" "$bytes" || true
    echo "minio backup FAILED after ${duration}s" >&2
  fi
}

# После успешного restic snapshot локальное зеркало больше не нужно. При
# ошибке оставляем его, чтобы следующий запуск мог докачать только разницу.
cleanup() {
  if [ "$backup_succeeded" -eq 1 ]; then
    [ -z "$mirror_dir" ] || rm -rf "$mirror_dir"
    [ -z "$manifest_file" ] || rm -f "$manifest_file"
  fi
}

on_exit() {
  exit_code=$?
  trap - EXIT
  cleanup || true

  if [ "$exit_code" -eq 0 ]; then
    finish 0 || true
  else
    finish 1 || true
  fi

  exit "$exit_code"
}
trap on_exit EXIT

: "${MINIO_ACCESS_KEY:?MINIO_ACCESS_KEY is required}"
: "${MINIO_SECRET_KEY:?MINIO_SECRET_KEY is required}"
: "${MINIO_BUCKET:?MINIO_BUCKET is required}"
: "${RESTIC_REPOSITORY:?RESTIC_REPOSITORY is required}"
: "${RESTIC_PASSWORD:?RESTIC_PASSWORD is required}"

mirror_dir="$mirror_root/$MINIO_BUCKET"
manifest_file="$mirror_root/${MINIO_BUCKET}.metadata.jsonl"

mkdir -p "$mirror_dir"

# Копия объектов занимает на диске больше, чем сам бакет: зеркало плюс
# репозиторий. Не начинаем, если места впритык.
if ! backup_require_free_space; then
  exit 1
fi

# --remove удаляет из зеркала объекты, удалённые в MinIO, иначе зеркало
# росло бы вечно. Удалённые объекты при этом остаются в старых снапшотах
# restic до истечения retention - то есть окно восстановления после
# случайного удаления равно глубине daily-политики (по умолчанию 7 дней).
if ! "$SCRIPT_DIR/mc.sh" mirror --overwrite --remove \
  "linka/$MINIO_BUCKET" "$mirror_dir"; then
  echo "mc mirror failed" >&2
  exit 1
fi

# Файловое зеркало содержит только байты. S3-метаданные сохраняем отдельно,
# иначе при обратной загрузке объекты без расширений получили бы
# application/octet-stream. Одна JSON-строка соответствует одному объекту.
rm -f "$manifest_file"
if ! "$SCRIPT_DIR/mc.sh" find "linka/$MINIO_BUCKET" --print "{}" |
  while IFS= read -r object_path; do
    key=${object_path#"linka/$MINIO_BUCKET/"}
    local_file="$mirror_dir/$key"

    if [ ! -f "$local_file" ]; then
      echo "manifest failed: mirrored file is missing for $key" >&2
      exit 1
    fi

    stat_json=$("$SCRIPT_DIR/mc.sh" stat --json "$object_path")
    source_size=$(printf '%s\n' "$stat_json" | jq -er '.size')
    local_size=$(wc -c < "$local_file" | tr -d ' ')

    if [ "$source_size" -ne "$local_size" ]; then
      echo "manifest failed: size changed while copying $key" >&2
      exit 1
    fi

    printf '%s\n' "$stat_json" | jq -ce --arg key "$key" '{
      key: $key,
      size: .size,
      content_type: (
        .metadata["Content-Type"]
        // .metadata["content-type"]
        // .contentType
        // "application/octet-stream"
      )
    }'
  done > "$manifest_file"; then
  echo "MinIO metadata manifest creation failed" >&2
  exit 1
fi

kbytes=$(du -sk "$mirror_dir" | cut -f1)
bytes=$((kbytes * 1024))

if ! restic backup "$mirror_dir" "$manifest_file" \
  --tag minio \
  --host linka-production; then
  echo "restic backup failed" >&2
  exit 1
fi

backup_succeeded=1
