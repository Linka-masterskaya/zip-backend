#!/usr/bin/env bash
set -euo pipefail

# Типы для фронта генерируются из той же спеки, что и серверные стабы,
# поэтому расходиться им негде. Версия закреплена: с @latest файл менялся
# бы сам по себе при выходе новой мажорной версии генератора.
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

npx --yes openapi-typescript@7.13.0 \
  docs/api/openapi.yaml \
  -o docs/api/openapi.types.ts
