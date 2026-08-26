#!/usr/bin/env sh
# Обёртка над `mc`: настраивает подключение к MinIO из переменных окружения
# и передаёт остальные аргументы дальше.
#
#   /scripts/mc.sh ls linka/linka-media
#   /scripts/mc.sh version enable linka/linka-media

set -eu

: "${MINIO_ACCESS_KEY:?MINIO_ACCESS_KEY is required}"
: "${MINIO_SECRET_KEY:?MINIO_SECRET_KEY is required}"

MC_HOST_linka="http://${MINIO_ACCESS_KEY}:${MINIO_SECRET_KEY}@minio:9000"
export MC_HOST_linka

exec mc "$@"
