# Локальный запуск и проверка API

## Запуск

Из корня `zip-backend`:

```bash
make run-local
```

Команда поднимает PostgreSQL, MinIO, NATS и Redis через `compose.dev.yaml`, применяет встроенные миграции и оставляет Go-сервер в foreground.

Проверка:

```bash
curl http://localhost:9090/health
curl http://localhost:9090/readyz
```

Основной API: `http://localhost:8080/api/v1`. Метрики и health endpoints: `http://localhost:9090`.

Изображения Pictures Bank не копируются в MinIO: набор хранит `source_picture_id`, а proxy кэширует полученные байты в памяти. TTL задаётся через `pictures_bank.cache_ttl` (`1h` в dev-конфиге) или `PICTURES_BANK_CACHE_TTL`. Такой cache локален для процесса и очищается при перезапуске сервера.

Остановка сервера — `Ctrl+C`. Остановка инфраструктуры без удаления данных:

```bash
make dev-down
```

## Postman

Импортируйте оба файла:

1. `postman/Linka-local.postman_collection.json`
2. `postman/Linka-local.postman_environment.json`

Выберите environment **Linka Local** и запускайте папки/запросы сверху вниз. После успешного login коллекция сохраняет `accessToken`; refresh-cookie Postman хранит и отправляет автоматически.

Для текущего локального volume создан demo-пользователь, совпадающий с переменными окружения Postman:

- email: `postman.local@example.com`
- password: `LocalPass123!`

Он позволяет сразу проверить `Login`, `Refresh` и authenticated-запросы. Это seed только в локальной БД, а не результат работы signup endpoint; `make dev-reset` удалит его вместе с volume PostgreSQL.

## Текущее состояние регистрации

На 2026-07-31 `POST /api/v1/auth/register` описан в `docs/api/openapi.yaml`, но отсутствует в маршрутах и реализации сервера. Фактический ответ локального сервиса — `404 Not Found`. Поэтому полный пользовательский сценарий сейчас останавливается на регистрации; коллекция содержит этот запрос как явную диагностическую проверку.

После реализации регистрации ожидаемый сценарий такой:

1. `Register` с `email` и паролем длиной 8–72 байта; ожидается `201`, access token в JSON и `refresh_token` в HttpOnly cookie.
2. `Verify email` с токеном из письма. В текущем dev-конфиге `auth.require_email_verification: false`, поэтому login не блокируется без этого шага.
3. `Login`; test script сохраняет `access_token` в environment.
4. `My profile`, `List folders`, `List packs`, `List students` с Bearer-токеном.
5. `Refresh`; Postman отправляет cookie и сохраняет новый access token.
6. `Forgot password` и `Reset password` требуют рабочей доставки почты. Сейчас dev-конфиг указывает на внешний SMTP (`smtp.yandex.ru`), не на локальный Mailpit.

Также в OpenAPI заявлен `POST /auth/logout`, но в текущем роутере этот endpoint не зарегистрирован.
