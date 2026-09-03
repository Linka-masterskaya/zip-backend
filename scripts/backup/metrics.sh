#!/usr/bin/env sh
# Общие функции для backup-скриптов. Файл только подключается через `.`
# и сам ничего не делает.

set -eu
set -o pipefail

# Адрес Pushgateway внутри docker-сети. Если он недоступен, отправка метрик
# тихо пропускается и на сам backup это не влияет.
BACKUP_PUSHGATEWAY_URL="http://pushgateway:9091"

# Минимум свободного места, при котором копия ещё делается.
# Диск на сервере один на всё: база, MinIO и копии. Если его забить,
# PostgreSQL перестанет писать - то есть backup уронит приложение.
BACKUP_MIN_FREE_MB=3072

backup_now() {
  date +%s
}

# backup_push_metrics <target> <success 0|1> <duration_seconds> <bytes>

backup_push_metrics() {
  target=$1
  success=$2
  duration_seconds=$3
  bytes=$4
  timestamp=$(backup_now)

  {
    echo "linka_backup_success{target=\"$target\"} $success"
    echo "linka_backup_last_run_timestamp_seconds{target=\"$target\"} $timestamp"
    echo "linka_backup_duration_seconds{target=\"$target\"} $duration_seconds"
    echo "linka_backup_bytes{target=\"$target\"} $bytes"
    if [ "$success" = "1" ]; then
      echo "linka_backup_last_success_timestamp_seconds{target=\"$target\"} $timestamp"
    fi

    # Свободное место на диске с копиями - чтобы Grafana предупредила
    # заранее, а не когда копии уже перестали делаться.
    repository_path=${RESTIC_REPOSITORY:-/backup-repo}
    free_kb=$(df -Pk "$repository_path" 2>/dev/null | awk 'NR==2 {print $4}')
    if [ -n "$free_kb" ]; then
      echo "linka_backup_disk_free_bytes{target=\"$target\"} $((free_kb * 1024))"
    fi
  } | curl -fsS --max-time 10 --data-binary @- \
    "$BACKUP_PUSHGATEWAY_URL/metrics/job/linka_backup/instance/production/target/$target"
}

# backup_require_free_space
#
# Проверяем место ДО того, как начать писать.

backup_require_free_space() {
  repository_path=${RESTIC_REPOSITORY:-/backup-repo}
  free_kb=$(df -Pk "$repository_path" | awk 'NR==2 {print $4}')
  free_mb=$((free_kb / 1024))

  if [ "$free_mb" -lt "$BACKUP_MIN_FREE_MB" ]; then
    echo "недостаточно места в $repository_path: свободно ${free_mb} МБ, нужно минимум ${BACKUP_MIN_FREE_MB} МБ" >&2
    return 1
  fi

  return 0
}
