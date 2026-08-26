#!/usr/bin/env sh
# Retention и проверка целостности репозитория.
#
# Запускается раз в сутки, отдельно от самих бэкапов: чистка не должна
# удлинять окно, в котором делается копия.
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=scripts/backup/metrics.sh
. "$SCRIPT_DIR/metrics.sh"

target="retention"
started_at=$(backup_now)

: "${RESTIC_REPOSITORY:?RESTIC_REPOSITORY is required}"
: "${RESTIC_PASSWORD:?RESTIC_PASSWORD is required}"

finish() {
  status=$1
  finished_at=$(backup_now)
  duration=$((finished_at - started_at))

  if [ "$status" = "0" ]; then
    backup_push_metrics "$target" 1 "$duration" 0 || true
    echo "retention ok in ${duration}s"
  else
    backup_push_metrics "$target" 0 "$duration" 0 || true
    echo "retention FAILED after ${duration}s" >&2
  fi
}

# --group-by host,tags: политика применяется к postgres и minio раздельно,
# поэтому "14 daily" считаются для каждого типа копии свои. Без группировки
# по тегам политика перемешала бы разнородные снапшоты и могла бы, например,
# удалить все копии MinIO за день, где было много копий PostgreSQL.
#
# --keep-last 1 явно защищает последний снапшот каждой группы: даже при
# кривых остальных параметрах последняя успешная копия не удалится.
#
# Глубина хранения подобрана под маленький диск сервера (один раздел на всё,
# ~20 ГБ свободно). Три уровня - daily/weekly/monthly - сохранены, но короче
# обычного: год истории медиа сюда физически не поместится.
if ! restic forget --prune \
  --group-by host,tags \
  --keep-last 1 \
  --keep-within 48h \
  --keep-daily 7 \
  --keep-weekly 4 \
  --keep-monthly 3; then
  echo "restic forget failed" >&2
  finish 1
  exit 1
fi

# Проверка структуры репозитория
# Это быстрая проверка метаданных.
if ! restic check; then
  echo "restic check failed: repository integrity problem" >&2
  finish 1
  exit 1
fi

finish 0
