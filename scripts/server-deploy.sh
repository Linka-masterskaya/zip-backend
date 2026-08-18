#!/bin/bash

set -euo pipefail

cd ~/zip-backend

compose() {
  docker compose -f docker-compose.server.yaml "$@"
}

wait_ready() {
  for _ in $(seq 1 30); do
    if curl -sf http://localhost:9091/readyz >/dev/null; then
      return 0
    fi
    sleep 2
  done
  return 1
}

show_backend_state() {
  compose ps zip-backend || true
  compose logs --no-color --tail=200 zip-backend || true
}

if [ -z "${1:-}" ]; then
  echo "Usage: $0 <version>"
  exit 1
fi

mapfile -t database_settings < <(bash scripts/read-deploy-db-settings.sh .env)
if [ "${#database_settings[@]}" -ne 2 ]; then
  echo "POSTGRES_USER and POSTGRES_DB must be set in .env"
  exit 1
fi
POSTGRES_USER=${database_settings[0]}
POSTGRES_DB=${database_settings[1]}

NEW_VERSION=$1
PREV_VERSION=$(cat .version 2>/dev/null || echo "")
BACKUP_FILE=""

export VERSION=$NEW_VERSION

rollback() {
  if [ -z "$PREV_VERSION" ]; then
    echo "Rollback is unavailable: no previous version recorded"
    return 1
  fi

  echo "Rollback to $PREV_VERSION"
  compose stop zip-backend
  RESTORE_FAILED=false
  if [ -n "$BACKUP_FILE" ] && [ -s "$BACKUP_FILE" ]; then
    if ! compose exec -T postgres \
      psql --set ON_ERROR_STOP=1 --single-transaction \
      -U "$POSTGRES_USER" "$POSTGRES_DB" < "$BACKUP_FILE"; then
      echo "Rollback warning: database backup restore failed; attempting to restart the previous application"
      RESTORE_FAILED=true
    fi
  fi

  export VERSION=$PREV_VERSION
  # The backend reaches MinIO through the public HTTPS endpoint served by
  # Caddy, so the proxy must be available before the application starts.
  if ! compose up -d --no-deps caddy; then
    echo "Rollback failed to start caddy"
    return 1
  fi
  if ! compose up -d zip-backend; then
    echo "Rollback failed to start $PREV_VERSION"
    return 1
  fi
  if ! wait_ready; then
    echo "Rollback failed readiness check"
    show_backend_state
    return 1
  fi
  if ! compose up -d --remove-orphans; then
    echo "Rollback failed to start the full stack"
    return 1
  fi
  echo "$PREV_VERSION" > .version
  if [ "$RESTORE_FAILED" = true ]; then
    echo "Previous application is ready, but the database backup was not restored"
  fi
}

compose pull

# Keep the public MinIO route available while the backend is replaced. Caddy
# may briefly return 502 for API traffic until the new backend passes readiness.
compose up -d --no-deps caddy
compose stop zip-backend

if [ -n "$(compose ps -q --status running postgres)" ]; then
  BACKUP_FILE="$PWD/backup_$(date +%Y%m%d_%H%M%S).sql"
  if ! compose exec -T postgres \
    pg_dump --clean --if-exists -U "$POSTGRES_USER" "$POSTGRES_DB" > "$BACKUP_FILE"; then
    echo "Database backup failed"
    BACKUP_FILE=""
    rollback || true
    exit 1
  fi
fi

if ! compose run --rm --entrypoint ./migrate zip-backend; then
  rollback || true
  exit 1
fi

if ! compose up -d zip-backend; then
  rollback || true
  exit 1
fi

if ! wait_ready; then
  echo "New application failed readiness check"
  show_backend_state
  rollback || true
  exit 1
fi

if ! compose up -d --remove-orphans; then
  rollback || true
  exit 1
fi
echo "$NEW_VERSION" > .version

compose ps

find ~/zip-backend -name "backup_*.sql" -mtime +7 -delete

docker image prune -f
