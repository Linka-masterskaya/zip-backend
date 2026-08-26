#!/usr/bin/env sh
# Регулярный backup объектов MinIO.
#
#   mc mirror -> локальное зеркало -> restic (шифрование) -> каталог копий
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

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=scripts/backup/metrics.sh
. "$SCRIPT_DIR/metrics.sh"

target="minio"
started_at=$(backup_now)
bytes=0

: "${MINIO_ACCESS_KEY:?MINIO_ACCESS_KEY is required}"
: "${MINIO_SECRET_KEY:?MINIO_SECRET_KEY is required}"
: "${MINIO_BUCKET:?MINIO_BUCKET is required}"
: "${RESTIC_REPOSITORY:?RESTIC_REPOSITORY is required}"
: "${RESTIC_PASSWORD:?RESTIC_PASSWORD is required}"

mirror_root=/backup-mirror
mirror_dir="$mirror_root/$MINIO_BUCKET"

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

mkdir -p "$mirror_dir"

# Копия объектов занимает на диске больше, чем сам бакет: зеркало плюс
# репозиторий. Не начинаем, если места впритык.
if ! backup_require_free_space; then
  finish 1
  exit 1
fi

# --remove удаляет из зеркала объекты, удалённые в MinIO, иначе зеркало
# росло бы вечно. Удалённые объекты при этом остаются в старых снапшотах
# restic до истечения retention - то есть окно восстановления после
# случайного удаления равно глубине daily-политики (по умолчанию 7 дней).
if ! "$SCRIPT_DIR/mc.sh" mirror --overwrite --remove \
  "linka/$MINIO_BUCKET" "$mirror_dir"; then
  echo "mc mirror failed" >&2
  finish 1
  exit 1
fi

kbytes=$(du -sk "$mirror_dir" | cut -f1)
bytes=$((kbytes * 1024))

if ! restic backup "$mirror_dir" \
  --tag minio \
  --host linka-production; then
  echo "restic backup failed" >&2
  # Зеркало намеренно остаётся: следующая попытка докачает только разницу.
  finish 1
  exit 1
fi

# Копия в репозитории есть - зеркало больше не нужно, освобождаем место.
rm -rf "$mirror_dir"

finish 0
