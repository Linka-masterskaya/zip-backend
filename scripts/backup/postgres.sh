#!/usr/bin/env sh
# Регулярный backup PostgreSQL.
#
#   pg_dump -> проверка дампа -> restic (шифрование) -> каталог копий
#
# Запускается по расписанию из контейнера `backup` (см. deploy/backup/crontab).
#
set -eu

# определяет папку, где лежит скрипт
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=scripts/backup/metrics.sh
. "$SCRIPT_DIR/metrics.sh"

target="postgres"
started_at=$(backup_now)
bytes=0

# Проверяем конфигурацию до того, как что-то делать: понятная ошибка лучше,
POSTGRES_HOST=${POSTGRES_HOST:-postgres}
POSTGRES_PORT=${POSTGRES_PORT:-5432}

: "${POSTGRES_USER:?POSTGRES_USER is required}"
: "${POSTGRES_PASSWORD:?POSTGRES_PASSWORD is required}"
: "${POSTGRES_DB:?POSTGRES_DB is required}"
: "${RESTIC_REPOSITORY:?RESTIC_REPOSITORY is required}"
: "${RESTIC_PASSWORD:?RESTIC_PASSWORD is required}"

tmp_dir=/backup-tmp


# `restic forget` группирует снапшоты по (host, paths). Если имя дампа будет
# меняться от запуска к запуску, каждый снапшот попадёт в собственную группу,
# политика вида --keep-daily 7 применится к группе из одного снапшота, и
# retention молча перестанет удалять что-либо вообще.
#
# Время снапшота restic хранит сам, в имени файла оно не нужно.
dump_file="$tmp_dir/linka.dump"

finish() {
  status=$1
  finished_at=$(backup_now)
  duration=$((finished_at - started_at))

  if [ "$status" = "0" ]; then
    backup_push_metrics "$target" 1 "$duration" "$bytes" || true
    echo "postgres backup ok: $bytes bytes in ${duration}s"
  else
    backup_push_metrics "$target" 0 "$duration" "$bytes" || true
    echo "postgres backup FAILED after ${duration}s" >&2
  fi
}

# Незашифрованный дамп не должен пережить работу скрипта.
cleanup() {
  rm -f "$dump_file"
}
trap cleanup EXIT

mkdir -p "$tmp_dir"

# Копии лежат на одном диске с базой: не начинаем, если места впритык.
if ! backup_require_free_space; then
  finish 1
  exit 1
fi

# --format=custom снимает данные в одной транзакции, поэтому дамп
# консистентен даже под нагрузкой.
# --clean --if-exists нужны, чтобы дамп разворачивался в непустую базу.
# --no-owner --no-privileges — чтобы восстановление работало в чистом
# окружении, где ролей из production ещё нет.
if ! PGPASSWORD="$POSTGRES_PASSWORD" pg_dump \
  -h "$POSTGRES_HOST" \
  -p "$POSTGRES_PORT" \
  -U "$POSTGRES_USER" \
  -d "$POSTGRES_DB" \
  --format=custom \
  --clean \
  --if-exists \
  --no-owner \
  --no-privileges \
  -f "$dump_file"; then
  echo "pg_dump failed" >&2
  finish 1
  exit 1
fi

bytes=$(wc -c < "$dump_file" | tr -d ' ')

# Проверяем дамп до отправки. Битый дамп, уехавший в хранилище, со временем
# вытеснит по retention последнюю хорошую копию
if ! pg_restore --list "$dump_file" >/dev/null 2>&1; then
  echo "dump verification failed: pg_restore cannot read the dump" >&2
  finish 1
  exit 1
fi

# restic шифрует данные перед записью: в каталоге копий лежат только
# зашифрованные блоки, прочитать их без RESTIC_PASSWORD нельзя.
if ! restic backup "$dump_file" \
  --tag postgres \
  --host linka-production; then
  echo "restic backup failed" >&2
  finish 1
  exit 1
fi

finish 0
