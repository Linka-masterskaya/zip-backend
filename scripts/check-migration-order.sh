#!/usr/bin/env bash
# Не даёт добавить миграцию с timestamp'ом меньше уже смерженных.
#
# Goose определяет миграцию по числовому префиксу имени файла и по
# умолчанию отказывается применять пропущенные версии. Если две ветки
# параллельно добавляют миграции, а мержатся в обратном порядке, деплой
# падает с "found N missing migrations" и откатывается. Так уже было с
# AB-40/AB-43 в июле и с AB-51/AB-23 в августе.
#
# Проверка сравнивает миграции, добавленные пулл-реквестом, с максимальной
# версией на текущем BASE_SHA. Это важно: merge-base может быть старее
# актуальной base-ветки и ошибочно считать уже смерженную в main миграцию
# новой миграцией pull request.
set -euo pipefail

BASE_SHA="${BASE_SHA:-origin/main}"
HEAD_SHA="${HEAD_SHA:-HEAD}"
MIGRATIONS_DIR="${MIGRATIONS_DIR:-migrations}"

# Даем понятную ошибку, если workflow не получил нужную историю.
git cat-file -e "${BASE_SHA}^{commit}"
git cat-file -e "${HEAD_SHA}^{commit}"

version_of() {
  # 20260819190000_create_x.sql -> 20260819190000
  basename "$1" | sed -n 's/^\([0-9][0-9]*\)_.*\.sql$/\1/p'
}

max_base_version=0
while IFS= read -r path; do
  [ -n "$path" ] || continue
  version="$(version_of "$path")"
  [ -n "$version" ] || continue
  if [ "$version" -gt "$max_base_version" ]; then
    max_base_version="$version"
  fi
done < <(git ls-tree -r --name-only "$BASE_SHA" -- "$MIGRATIONS_DIR" | grep '\.sql$' || true)

if [ "$max_base_version" -eq 0 ]; then
  echo "На базовой ветке миграций нет — проверять нечего."
  exit 0
fi

failed=0
while IFS= read -r path; do
  [ -n "$path" ] || continue
  version="$(version_of "$path")"
  if [ -z "$version" ]; then
    echo "ОШИБКА: $path не начинается с числового timestamp'а."
    failed=1
    continue
  fi
  if [ "$version" -le "$max_base_version" ]; then
    echo "ОШИБКА: $path имеет версию $version, а на базовой ветке уже есть $max_base_version."
    echo "        Goose не применит её после более новой и уронит деплой."
    echo "        Переименуйте файл, дав timestamp больше $max_base_version."
    failed=1
  fi
done < <(git diff --name-only --diff-filter=A "$BASE_SHA" "$HEAD_SHA" -- "$MIGRATIONS_DIR" | grep '\.sql$' || true)

if [ "$failed" -ne 0 ]; then
  exit 1
fi

echo "Порядок миграций корректен: все новые версии больше $max_base_version."
