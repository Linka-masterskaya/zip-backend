# Образ для регулярных backup-задач (PostgreSQL + MinIO).
#
# Базовый образ - postgres:17-alpine, чтобы pg_dump был той же мажорной
# версии, что и сервер из docker-compose.server.yaml. pg_dump более старой
# версии, чем сервер, просто откажется снимать дамп.
#
# При обновлении PostgreSQL в compose нужно обновить и эту строку.
FROM postgres:17-alpine

# mc берём готовым бинарником из официального образа MinIO: в репозиториях
# alpine его нет, а качать по сети во время сборки - лишняя точка отказа.
COPY --from=minio/mc:RELEASE.2025-08-13T08-35-41Z /usr/bin/mc /usr/local/bin/mc

# restic - шифрование, дедупликация и retention; curl - отправка метрик;
# jq - manifest MinIO; flock из util-linux - защита от пересечения задач.
RUN apk add --no-cache restic curl jq util-linux

COPY scripts/backup/ /scripts/
COPY deploy/backup/crontab /etc/crontabs/root

RUN chmod +x /scripts/*.sh

# busybox crond в foreground: контейнер живёт, пока живёт планировщик,
# и docker restart-policy работает как для обычного сервиса.
ENTRYPOINT ["crond", "-f", "-d", "8"]
