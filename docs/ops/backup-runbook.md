# Runbook: резервное копирование

Команды копируются как есть, вместо `<...>` подставляются значения.
Всё выполняется на сервере из каталога `~/zip-backend`.

## Если коротко

**Копии делаются?**

```sh
docker compose -f docker-compose.server.yaml --profile backup exec backup restic snapshots
```

Строка с тегом `postgres` должна быть не старше часа-двух, с тегом `minio` -
не старше 6–7 часов.

**Сколько осталось места?**

```sh
df -h
```

**Что-то сломалось, посмотреть почему:**

```sh
docker compose -f docker-compose.server.yaml --profile backup logs --tail=50 backup
```

> **Одна вещь, которую нельзя потерять** - пароль `RESTIC_PASSWORD` из файла
> `.env.backup`. Без него копии не открыть. Он должен лежать минимум в двух местах

## Как это работает

```
postgres ──pg_dump──┐
                    ├──> контейнер backup ──зашифровано──> /var/backups/linka
minio ────mc mirror─┘      (планировщик + restic)
                    └──метрики──> pushgateway ──> prometheus ──> grafana
```

Контейнер `backup` описан в `docker-compose.server.yaml`, но вынесен в
profile `backup`: основной стек может стартовать без него. После включения
profile контейнер просто живёт, а внутри работает планировщик, который раз в
минуту сверяется с расписанием:

| Когда | Что |
|---|---|
| каждый час, в :00 | копия базы |
| 00:30, 06:30, 12:30, 18:30 | копия объектов MinIO |
| 03:40 | удаление устаревших копий, проверка репозитория |

Постоянно работающего процесса копирования нет: скрипт отрабатывает
полминуты и завершается. Расписание - в `deploy/backup/crontab`.

**Что лежит в `/var/backups/linka`.** Не файлы вида `backup_2026-08-22.dump`,
а репозиторий restic. Он режет данные на блоки, считает хеш каждого и
записывает только те, которых ещё нет. Снапшот - это список ссылок на блоки,
поэтому пятьдесят копий занимают немногим больше, чем одна. Каждый блок
шифруется перед записью: без `RESTIC_PASSWORD` каталог нечитаем даже с
root-доступом.

**Почему у базы и медиа разная частота.** Это важно при выборе снапшотов для
восстановления.

- *Пострадала только база* - восстанавливаем её одну, MinIO не трогаем:
  объекты на месте, ссылки разрешаются. Потеря - до часа.
- *Потеряно и то и другое* - нужна согласованная пара, и снимок медиа должен
  быть **не старше** снимка базы. Иначе в `media_files` окажутся строки без
  объектов, то есть битые картинки и звуки. Потеря - до шести часов.

**Диск.** На сервере один раздел ~29 ГБ на всё: Docker, база, MinIO, логи и
копии. Копия медиа занимает примерно двойной объём бакета, поэтому потолок -
около 5 ГБ медиа. Скрипты отказываются работать, если свободно меньше 3 ГБ.

**Из чего состоит:**

| Файл | Что делает |
|---|---|
| `deploy/backup/Dockerfile` | образ с `pg_dump`, `mc` и `restic` |
| `deploy/backup/crontab` | расписание |
| `scripts/backup/postgres.sh` | копия базы |
| `scripts/backup/minio.sh` | копия объектов |
| `scripts/backup/retention.sh` | чистка и проверка репозитория |
| `scripts/backup/metrics.sh` | общие функции, подключается в остальные |
| `scripts/backup/mc.sh` | обёртка над `mc` с настроенным доступом |
| `scripts/backup/verify-restore.sh` | проверка целостности после восстановления |

| Показатель | Значение |
|---|---|
| Потеря данных при аварии только в базе | до 1 часа |
| Потеря данных при потере обоих источников | до 6 часов |


## Первичная настройка

Делается один раз, занимает около получаса.

### 1. Каталог для копий

```sh
sudo mkdir -p /var/backups/linka
sudo chmod 700 /var/backups/linka
df -h /var/backups
```

### 2. Пароль

```sh
openssl rand -base64 32
```

Создайте `~/zip-backend/.env.backup` - в нём две строки:

```sh
RESTIC_REPOSITORY=/backup-repo
RESTIC_PASSWORD=<пароль из команды выше>
```

```sh
chmod 600 ~/zip-backend/.env.backup
```

Пароль сразу запишите в менеджер паролей. Восстановить его невозможно.
Файл `.env.backup` уже добавлен в `.gitignore`, коммитить его нельзя.

Если пропустить этот шаг, основной стек всё равно запустится. Но команды с
`--profile backup` завершатся с ошибкой: compose увидит, что сервису `backup`
нужен файл `.env.backup`.

Если каталог `/var/backups/linka` не создать заранее, Docker может создать
его сам с неудобными владельцем и правами. Backup может заработать, но
сопровождать и восстанавливать копии с host-машины будет сложнее.

### 3. Проверить конфигурацию

Сервисы `backup` и `pushgateway` уже описаны в `docker-compose.server.yaml`
и включаются через compose profile `backup`. Prometheus уже настроен читать
метрики из Pushgateway. Вручную вставлять YAML-блоки не нужно.

Проверьте, что итоговый compose собирается:

```sh
docker compose -f docker-compose.server.yaml --profile backup config --quiet
```

Если команда ничего не вывела и завершилась без ошибки - конфигурация
валидна.

### 4. Включить backup

После подготовки каталога и `.env.backup` один раз создайте restic repository,
снимите контрольные копии вручную и запустите backup-сервис. К этому моменту
PostgreSQL и MinIO уже должны быть доступны в compose-сети. Бакет
`MINIO_BUCKET` тоже должен уже существовать: в обычном запуске его создаёт
приложение при старте. Если включить backup до первого успешного запуска
приложения с MinIO, `minio.sh` упадёт на `mc mirror`.

```sh
docker compose -f docker-compose.server.yaml --profile backup build backup
docker compose -f docker-compose.server.yaml --profile backup run --rm --entrypoint restic backup init
docker compose -f docker-compose.server.yaml --profile backup run --rm --entrypoint /scripts/postgres.sh backup
docker compose -f docker-compose.server.yaml --profile backup run --rm --entrypoint /scripts/minio.sh backup
docker compose -f docker-compose.server.yaml --profile backup up -d backup pushgateway
docker compose -f docker-compose.server.yaml --profile backup exec backup restic snapshots --group-by host,tags
```

`--entrypoint` обязателен: контейнер по умолчанию запускает планировщик, и
без флага команда уйдёт ему в аргументы.

В выводе `snapshots` должны быть группы с тегами `postgres` и `minio`.

Если включить profile `backup` без `.env.backup`, compose завершится с ошибкой
чтения env-файла. Если забыть `restic init`, ручной backup и cron будут
падать до инициализации repository.

### 5. Два алерта в Grafana

Alerting → New alert rule, источник Prometheus.

Копии не делаются - упали, давно не было или контейнер не подняли:
```promql
(time() - linka_backup_last_success_timestamp_seconds > 25200)
  or absent(linka_backup_last_success_timestamp_seconds)
```

Кончается место:
```promql
min(linka_backup_disk_free_bytes) < 5e9
```

### 6. Проверка через час

Ручной запуск и запуск по расписанию - разные условия. Копия базы ежечасная,
так что ждать недолго:

```sh
docker compose -f docker-compose.server.yaml --profile backup logs --tail=50 backup
```

Должна быть строка `postgres backup ok: ...` со временем, кратным часу.


## Когда пришёл алерт

### «Копии не делаются»

```sh
docker compose -f docker-compose.server.yaml --profile backup ps backup
docker compose -f docker-compose.server.yaml --profile backup logs --tail=200 backup
docker compose -f docker-compose.server.yaml --profile backup exec backup /scripts/postgres.sh
```

| В логе | Причина | Что делать |
|---|---|---|
| `недостаточно места` | Диск заполнен | См. ниже |
| `... is required` | Не задан `.env.backup` | Проверить файл, перезапустить контейнер |
| `pg_dump failed` | База недоступна | Проверить контейнер postgres |
| `dump verification failed` | Дамп снялся битым | Запустить повторно, проверить диск |
| `Fatal: wrong password` | Изменился `RESTIC_PASSWORD` | Вернуть исходный из менеджера паролей |

Пока алерт не погашен, копий нет.

### «Кончается место»

Диск на сервере один на всё: заполнится - встанет и база.

```sh
df -h /var/backups
sudo du -sh /var/backups/linka
docker system df
```

По возрастанию радикальности:

1. Прогнать чистку, не дожидаясь ночи:
   `docker compose -f docker-compose.server.yaml --profile backup exec backup /scripts/retention.sh`
2. Проверить старые дампы деплоя: `ls -lh ~/zip-backend/backup_*.sql`
3. Ужать глубину хранения в `scripts/backup/retention.sh` и пересобрать образ

Порог, ниже которого копия не делается, задан в `scripts/backup/metrics.sh` -
3 ГБ. Понижать его, чтобы «заработало», не стоит: это подушка, которая не даёт
положить базу.

## Восстановление базы

Занимает 20–40 минут.

```sh
# 1. выбрать копию, записать ID
docker compose -f docker-compose.server.yaml --profile backup exec backup restic snapshots --tag postgres

# 2. очистить старую временную папку и достать дамп
docker compose -f docker-compose.server.yaml --profile backup exec backup rm -rf /backup-tmp/restore
docker compose -f docker-compose.server.yaml --profile backup exec backup \
  restic restore <SNAPSHOT_ID> --target /backup-tmp/restore

# 3. остановить приложение
docker compose -f docker-compose.server.yaml stop zip-backend

# 4. развернуть
docker compose -f docker-compose.server.yaml --profile backup exec backup sh -c '
  PGPASSWORD="$POSTGRES_PASSWORD" pg_restore \
    -h postgres -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
    --clean --if-exists --no-owner --no-privileges \
    /backup-tmp/restore/backup-tmp/linka.dump
'

# 5. запустить и проверить
docker compose -f docker-compose.server.yaml up -d zip-backend
curl -sf http://localhost:9091/readyz && echo OK

# 6. убрать за собой
docker compose -f docker-compose.server.yaml --profile backup exec backup rm -rf /backup-tmp/restore
```

Сообщения `does not exist, skipping` - норма.

## Восстановление медиа

```sh
# 1. выбрать копию - не старше той, что взяли для базы
docker compose -f docker-compose.server.yaml --profile backup exec backup restic snapshots --tag minio

# 2. очистить старую временную папку и достать объекты
docker compose -f docker-compose.server.yaml --profile backup exec backup rm -rf /backup-tmp/restore
docker compose -f docker-compose.server.yaml --profile backup exec backup \
  restic restore <SNAPSHOT_ID> --target /backup-tmp/restore

# 3. залить обратно
docker compose -f docker-compose.server.yaml --profile backup exec backup \
  /scripts/mc.sh mirror --overwrite \
    /backup-tmp/restore/backup-mirror/<MINIO_BUCKET> \
    linka/<MINIO_BUCKET>
```

Без `--remove`: восстановление не должно удалять то, что уже есть в бакете.

## Проверка после восстановления

```sh
docker compose -f docker-compose.server.yaml --profile backup exec backup /scripts/verify-restore.sh
```

Скрипт сверяет каждую запись `media_files` с объектами в MinIO. Код возврата
`0` - целостность в порядке. Если жалуется на недостающие объекты, значит
копия медиа старше копии базы: возьмите более свежую.

Если скрипт падает с `relation "users" does not exist`, значит проверяется не
восстановленная рабочая база, а пустая база без схемы приложения.

Дальше глазами:

- [ ] вход в аккаунт работает
- [ ] список студентов открывается
- [ ] список наборов открывается
- [ ] карточка с картинкой открывается
- [ ] карточка со звуком воспроизводится

## Что стоит знать

**Глубина хранения.** Все копии за 48 часов, 7 ежедневных, 4 еженедельных,
3 ежемесячных - около трёх месяцев. Задано в `scripts/backup/retention.sh`.

**Нельзя коммитить и логировать:** `RESTIC_PASSWORD`, `POSTGRES_PASSWORD`,
`MINIO_ACCESS_KEY`, `MINIO_SECRET_KEY`.

**Правка скриптов требует пересборки образа** - они попадают внутрь при
сборке, а не подключаются живьём.

**Дамп перед деплоем** (`backup_*.sql` в `~/zip-backend`) - отдельный
механизм быстрого отката, а не резервная копия: он не по расписанию, без
медиа, без шифрования и живёт неделю. Оба нужны.

**Чего эта схема не покрывает:** отказ диска, потерю сервера, шифровальщик,
восстановление на произвольную минуту.
