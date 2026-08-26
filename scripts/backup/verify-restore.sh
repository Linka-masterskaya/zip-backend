#!/usr/bin/env sh
# Проверка восстановленного окружения: ссылочная целостность media.
#
# Сравнивает minio_key из таблицы media_files со списком объектов,
# реально лежащих в MinIO. Каждая строка media_files должна иметь объект,
# иначе приложение покажет битые ссылки на медиа.
#
# Запускать ПОСЛЕ восстановления и PostgreSQL, и MinIO.
# Код возврата 0 — целостность в порядке, 1 — найдены битые ссылки.
#
set -eu

: "${POSTGRES_USER:?POSTGRES_USER is required}"
: "${POSTGRES_PASSWORD:?POSTGRES_PASSWORD is required}"
: "${POSTGRES_DB:?POSTGRES_DB is required}"
: "${MINIO_ACCESS_KEY:?MINIO_ACCESS_KEY is required}"
: "${MINIO_SECRET_KEY:?MINIO_SECRET_KEY is required}"
: "${MINIO_BUCKET:?MINIO_BUCKET is required}"

work_dir=$(mktemp -d)
cleanup() {
  rm -rf "$work_dir"
}
trap cleanup EXIT

echo "== Проверка восстановленных данных =="
echo ""

# --- PostgreSQL: содержимое ключевых таблиц -------------------------------
psql_query() {
  PGPASSWORD="$POSTGRES_PASSWORD" psql \
    -h "${POSTGRES_HOST:-postgres}" \
    -p "${POSTGRES_PORT:-5432}" \
    -U "$POSTGRES_USER" \
    -d "$POSTGRES_DB" \
    -At -c "$1"
}

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

# Пустая база - это тоже провал восстановления, просто менее очевидный:
# все дальнейшие проверки на пустых данных пройдут "успешно".
if [ "$users_count" = "0" ] || [ "$packs_count" = "0" ]; then
  echo "ОШИБКА: база восстановлена, но ключевые таблицы пусты." >&2
  echo "Проверьте, что восстанавливали нужный snapshot." >&2
  exit 1
fi

# --- MinIO: список реально существующих объектов --------------------------
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

psql_query "SELECT minio_key FROM media_files ORDER BY 1" \
  | sort > "$work_dir/db_keys.txt"

# mc find печатает по одному полному пути на строку и корректно переживает
# пробелы в именах объектов, в отличие от разбора вывода `mc ls`.
"$SCRIPT_DIR/mc.sh" find "linka/$MINIO_BUCKET" --print "{}" 2>/dev/null \
  | sed "s|^linka/$MINIO_BUCKET/||" \
  | sort > "$work_dir/minio_keys.txt"

objects_count=$(wc -l < "$work_dir/minio_keys.txt" | tr -d ' ')

echo "MinIO:"
echo "  объектов в бакете $MINIO_BUCKET: $objects_count"
echo ""

# --- Сверка ---------------------------------------------------------------
# Есть в БД, но нет в MinIO - битая ссылка, приложение не покажет медиа.
missing=$(comm -23 "$work_dir/db_keys.txt" "$work_dir/minio_keys.txt")

# Есть в MinIO, но нет в БД. Это не ошибка: объект мог остаться
# после удаления карточки. Показываем только количество.
orphan_count=$(comm -13 "$work_dir/db_keys.txt" "$work_dir/minio_keys.txt" | wc -l | tr -d ' ')

if [ -n "$missing" ]; then
  echo "ОШИБКА: ссылочная целостность media нарушена." >&2
  echo "Эти minio_key есть в media_files, но объектов в MinIO нет:" >&2
  echo "$missing" >&2
  echo "" >&2
  echo "Обычная причина: snapshot MinIO старше snapshot PostgreSQL." >&2
  echo "Возьмите snapshot MinIO, снятый не раньше выбранного snapshot БД." >&2
  exit 1
fi

echo "OK: у каждой строки media_files есть объект в MinIO."
echo "    объектов без ссылки из БД (не ошибка): $orphan_count"
