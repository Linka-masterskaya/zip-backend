#!/bin/bash

set -euo pipefail

cd ~/zip-backend

NEW_VERSION=$1
PREV_VERSION=$(cat .version 2>/dev/null || echo "")
echo "$NEW_VERSION" > .version

export VERSION=$NEW_VERSION

docker compose -f docker-compose.server.yaml pull

docker compose -f docker-compose.server.yaml run --rm zip-backend --migrate

docker compose -f docker-compose.server.yaml up -d --remove-orphans

docker compose -f docker-compose.server.yaml restart caddy

for i in $(seq 1 30); do
  docker compose -f docker-compose.server.yaml exec zip-backend wget -qO- http://localhost:9091/health && break
  sleep 2
done

if ! docker compose -f docker-compose.server.yaml exec zip-backend wget -qO- http://localhost:9091/health; then
  if [ -n "$PREV_VERSION" ]; then
    echo "Rollback to $PREV_VERSION"
    echo "$PREV_VERSION" > .version
    export VERSION=$PREV_VERSION
    docker compose -f docker-compose.server.yaml up -d
  fi
  exit 1
fi

docker compose -f docker-compose.server.yaml ps

docker image prune -f