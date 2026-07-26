# Linka Backend — Архитектурные решения

Дата: 2026-06-03
Статус: финал для нарезки задач

---

## 1. Монолит vs Микросервисы

### Вердикт: Модульный монолит на Go + отдельный AI-воркер на Go

**Против 5 микросервисов сразу:**
- 100 RPS — нет масштабного давления для microservices
- 5 сервисов = 5 Dockerfile + 5 CI-пайплайнов + service discovery + распределённые трейсы = overhead без ROI
- Дедлайн скелета 30.09.2025. Microservices замедлят delivery

**Что делаем:**

```
cmd/
  server/main.go       ← Go-монолит (HTTP API)
  ai-worker/main.go    ← Go AI-воркер (gRPC + RabbitMQ consumer)

internal/
  auth/                ← JWT, OAuth2, RBAC
  pack/                ← CRUD наборов, версии, ZIP
  media/               ← upload, дедупликация, presigned URL
  middleware/          ← auth, rate-limit, rbac
  config/
```

**AI-воркер отдельным сервисом (на Go, не Python):**
- LLM (OpenAI o3): долгие операции, не место в HTTP-handler
- TTS (Yandex SpeechKit): async fire-and-forget
- Go имеет `sashabaranov/go-openai`, SpeechKit — чистый REST
- Один язык — один тулчейн, один линтер в CI

**Граница между сервисами — только NATS:**
- `ai.llm.req` / `ai.llm.resp.{req_id}` — LLM streaming (pub/sub)
- `ai.tts.jobs` (JetStream) — TTS задачи
- `ai.clamav.jobs` (JetStream) — антивирус
- `ai.tts.done.{pack_id}` — результаты TTS

**Когда выделять модули в сервисы:** когда конкретный модуль станет боттлнеком. Не раньше.

---

## 2. Redis

### Вердикт: оставляем по ТЗ, scope ограничиваем

**Используем Redis для:**
- Refresh-токены: атомарный TTL, O(1) lookup, мгновенная инвалидация через `DEL`
- Rate-limit counters: атомарный `INCR` + `EXPIRE`
- WebSocket pub/sub при горизонтальном масштабировании (на вырост)

**НЕ используем Redis для:**
- Общий кеш запросов — преждевременно при 100 RPS, добавим когда метрики покажут нужду
- WebSocket-сессии сейчас — `sync.Map` в памяти при 1 инстансе достаточно

---

## 3. Финальный стек

| Компонент | Технология | Примечание |
|-----------|-----------|------------|
| HTTP API | Go + `echo` v4 | Быстро, мало бойлерплейта, встроенный валидатор |
| AI-воркер | Go + NATS consumer | Один язык с монолитом |
| Межсервисный транспорт | NATS JetStream | Заменяет RabbitMQ + gRPC |
| БД | PostgreSQL 15 + pgvector | pgvector — только когда AI-поиск |
| Миграции | `goose` | SQL-файлы, не ORM |
| Токены/rate-limit | Redis 7 | Refresh-токены + counters |
| Объектное хранилище | MinIO | S3-совместимый |
| Мониторинг | Prometheus + Grafana + Loki | По ТЗ |

**Почему NATS вместо gRPC + RabbitMQ:**
- 1 бинарник (~20MB) вместо 2 систем
- Нет proto-файлов, нет кодогенерации в CI
- NATS JetStream = персистентные очереди (TTS, ClamAV)
- NATS Core pub/sub = LLM streaming (монолит подписывается на `ai.llm.resp.{req_id}`)
- `nats.go` — официальный Go-клиент, простой API

---

## 4. Структура репозитория

```
Go/
├── cmd/
│   ├── server/
│   │   └── main.go
│   └── ai-worker/
│       └── main.go
├── internal/
│   ├── auth/
│   │   ├── handler.go
│   │   ├── service.go
│   │   ├── repository.go
│   │   └── jwt.go
│   ├── pack/
│   │   ├── handler.go
│   │   ├── service.go
│   │   ├── repository.go
│   │   └── zip.go
│   ├── media/
│   │   ├── handler.go
│   │   ├── service.go
│   │   └── minio.go
│   ├── ai/
│   │   ├── client.go        ← gRPC-клиент для монолита
│   │   ├── llm.go           ← OpenAI o3 (в ai-worker)
│   │   └── tts.go           ← Yandex SpeechKit (в ai-worker)
│   ├── middleware/
│   │   ├── auth.go
│   │   ├── ratelimit.go
│   │   └── rbac.go
│   └── config/
│       └── config.go
├── pkg/
│   └── linka/
│       └── format.go        ← парсинг/валидация .linka ZIP
├── internal/
│   └── nats/
│       └── subjects.go      ← константы subject-имён
├── migrations/
│   ├── 001_users.sql
│   ├── 002_organizations.sql
│   ├── 003_packs.sql
│   └── 004_media.sql
├── docker-compose.yml
├── Makefile
├── Dockerfile.server
├── Dockerfile.ai-worker
├── go.mod
└── go.sum
```

---

## 5. Задачи для команды — Спринт 0

Параллельные треки — можно раздать разным разработчикам сразу.

---

### Трек A: Фундамент (блокирует всё)

#### [AB-1] Инициализация репозитория
**Зависимости:** нет
**Критерий готовности:** `go build ./...` проходит, структура папок создана

- `go mod init github.com/linka-editor/backend`
- Создать layout из п.4 (пустые пакеты с `package` декларациями)
- `.gitignore`: бинарники, `.env*`, `*.linka`
- `README.md`: локальный запуск за 3 команды

---

#### [AB-2] Docker Compose dev-окружение
**Зависимости:** нет (параллельно с AB-1)
**Критерий готовности:** `make dev-up` поднимает все сервисы, все healthcheck зелёные

Сервисы:
- `postgres:15-alpine` (порт 5432)
- `redis:7-alpine` (порт 6379)
- `minio/minio` (порт 9000/9001)
- `nats:latest -js` (порт 4222, monitoring 8222) ← JetStream включён флагом

Makefile-цели:
```
make dev-up      # docker compose up -d
make dev-down    # docker compose down
make dev-reset   # down -v + up (сброс данных)
make migrate     # goose up
make migrate-down
make lint        # golangci-lint run
make test        # все обычные тесты с -race
make test-e2e    # отдельный HTTP E2E-профиль: Testcontainers + миграции
```

`.env.example`:
```
DB_URL=postgres://linka:linka@localhost:5432/linka?sslmode=disable
REDIS_URL=redis://localhost:6379/0
NATS_URL=nats://localhost:4222
MINIO_ENDPOINT=localhost:9000
MINIO_ACCESS_KEY=minioadmin
MINIO_SECRET_KEY=minioadmin
MINIO_BUCKET=linka-media
JWT_SECRET=changeme
JWT_ACCESS_TTL=15m
JWT_REFRESH_TTL=720h
GOOGLE_CLIENT_ID=
GOOGLE_CLIENT_SECRET=
GOOGLE_REDIRECT_URL=http://localhost:8080/auth/google/callback
```

---

#### [AB-3] Config + logger
**Зависимости:** AB-1
**Критерий готовности:** `config.Load()` читает env, паникует при отсутствии обязательных полей

- Struct-based config через `github.com/ilyakaznacheev/cleanenv`
- Structured logger: `log/slog` (stdlib, Go 1.21+), JSON в prod, text в dev
- `APP_ENV=dev|prod` переключает формат логов

---

#### [AB-4] Миграции БД (goose)
**Зависимости:** AB-2
**Критерий готовности:** `make migrate` проходит чисто, `make migrate-down` откатывает

Схема:

```sql
-- 001_users.sql
CREATE TYPE user_role AS ENUM ('defectologist', 'parent', 'viewer', 'admin');

CREATE TABLE organizations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    storage_used_bytes BIGINT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          UUID REFERENCES organizations(id),
    email           TEXT UNIQUE NOT NULL,
    password_hash   TEXT,                    -- NULL для OAuth-only пользователей
    role            user_role NOT NULL DEFAULT 'viewer',
    google_id       TEXT UNIQUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 002_packs.sql
CREATE TABLE packs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES organizations(id),
    owner_id    UUID NOT NULL REFERENCES users(id),
    title       TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'draft', -- draft | published
    config      JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE pack_versions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pack_id     UUID NOT NULL REFERENCES packs(id) ON DELETE CASCADE,
    version     INT NOT NULL,
    config      JSONB NOT NULL,
    created_by  UUID NOT NULL REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(pack_id, version)
);

-- 003_media.sql
CREATE TABLE media_files (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          UUID NOT NULL REFERENCES organizations(id),
    uploader_id     UUID NOT NULL REFERENCES users(id),
    sha256          TEXT NOT NULL,           -- для дедупликации
    mime_type       TEXT NOT NULL,
    size_bytes      BIGINT NOT NULL,
    minio_key       TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(org_id, sha256)                  -- деdup в рамках организации
);
```

---

#### [AB-5] HTTP-сервер + базовые middleware
**Зависимости:** AB-3
**Критерий готовности:** `curl localhost:8080/health` возвращает `{"status":"ok"}`, сервер gracefully shutdown по SIGTERM

- `github.com/labstack/echo/v4`
- Middleware: `RequestID`, `Logger` (slog), `Recover`, `CORS`
- `GET /health` → `{"status":"ok","version":"..."}`
- Graceful shutdown: 30s timeout

---

#### [AB-9] CI: GitHub Actions
**Зависимости:** AB-1
**Критерий готовности:** PR-чек зелёный на чистом коде, красный на lint-ошибке

```yaml
# PR
- golangci-lint (errcheck, gosec, staticcheck, govet)
- go test ./... -race -count=1
- Trivy (scan образа)

# push main
- docker build → push GHCR
```

---

### Трек B: Auth (после AB-5)

#### [AB-6] Auth: email/password + JWT
**Зависимости:** AB-4, AB-5
**Критерий готовности:** интеграционный тест: register → login → refresh → logout

Эндпоинты:
- `POST /auth/register` → `{access_token, expires_in}`
- `POST /auth/login` → `{access_token, expires_in}` + refresh в httpOnly cookie
- `POST /auth/refresh` → новый access JWT
- `POST /auth/logout` → DEL refresh токен из Redis

Детали:
- bcrypt cost=12 для паролей
- Access JWT: HS256, payload `{sub, role, org_id, exp}`
- Refresh токен: `SETEX refresh:{uuid} 720h {user_id}` в Redis
- Refresh через httpOnly cookie (не Bearer) — защита от XSS

---

#### [AB-7] Auth: Google OAuth2
**Зависимости:** AB-6
**Критерий готовности:** в браузере можно войти через Google, пользователь создаётся в БД

- `GET /auth/google` → redirect на Google
- `GET /auth/google/callback` → JWT (та же схема что в AB-6)
- Upsert пользователя: нашли по `google_id` → обновили, не нашли → создали
- Если email уже есть (email/pass) → привязать google_id к существующему

---

#### [AB-8] RBAC middleware
**Зависимости:** AB-6
**Критерий готовности:** unit-тесты на все 4 роли + forbidden на неверной роли

```go
// Использование:
e.GET("/admin/users", handler, middleware.RequireRole("admin"))
e.GET("/packs", handler, middleware.RequireRole("defectologist", "parent", "admin"))
```

- Читает роль из JWT claims
- 403 если роль не разрешена
- Unit-тесты: таблица разрешений для каждого эндпоинта

---

### Трек C: AI-воркер (параллельно треку B)

#### [AB-10] NATS-скелет + subjects-контракт AI-воркера
**Зависимости:** AB-2 (docker-compose с NATS)
**Критерий готовности:** ai-worker стартует, подключается к NATS, монолит публикует — воркер получает

Subjects:
```go
// internal/nats/subjects.go
const (
    SubjectLLMRequest  = "ai.llm.req"
    SubjectLLMResponse = "ai.llm.resp.%s"   // %s = request_id
    SubjectTTSJobs     = "ai.tts.jobs"       // JetStream
    SubjectTTSDone     = "ai.tts.done.%s"   // %s = pack_id
    SubjectClamAVJobs  = "ai.clamav.jobs"   // JetStream
)
```

Сообщения (JSON):
```go
type LLMRequest  struct { RequestID string; Prompt string; Count int }
type LLMChunk    struct { RequestID string; JSONPatch string; Done bool }
type TTSJob      struct { PackID string; CardID string; Text string; Voice string }
type TTSResult   struct { PackID string; CardID string; AudioURL string; Error string }
```

- NATS JetStream stream `AI_JOBS`: subjects `ai.tts.jobs`, `ai.clamav.jobs`
- ai-worker: JetStream push consumer для TTS/ClamAV, Core sub для LLM
- Монолит: Core pub для LLM запросов, Core sub для LLM ответов по SSE → клиенту

---

### Что НЕ делаем в Спринте 0

| Что | Почему |
|-----|--------|
| Pack CRUD | После auth (нужна авторизация) |
| Media upload | После Pack (нужна привязка к набору) |
| WebSocket | После базового CRUD |
| OpenAI/SpeechKit интеграция | После proto-контракта (AB-10) |
| pgvector | Только когда AI-поиск по изображениям |
| Кеширование запросов | После метрик — не раньше |
| mTLS | После базового деплоя |

---

## 6. Открытые вопросы (уточнить с командой)

1. **Pictures Bank API** — чей Bearer-токен? Как ротируется? Есть rate-limit?
2. **ClamAV** — кто консьюмер в RabbitMQ? AI-воркер или отдельный воркер?
3. **Лимит 10 ГиБ/орг** — проверяем перед загрузкой в Postgres или в MinIO?
4. **mTLS edge-01 ↔ app-01** — Traefik сам или вручную через cfssl?
5. **Версионирование наборов** — сколько версий хранить? Лимит на откат?

---

# Решения от 2026-07-26 (модуль «наборы и папки»)

## 7. Владение миграциями

### Вердикт: миграции добавляет одна задача-владелец, остальные PR на неё ребейзятся

**Что случилось:** PR #89 (AB-40) и PR #92 (AB-43) независимо создали таблицу `folders` — двумя файлами с разными timestamp и побайтово одинаковым телом. Второй по порядку упадёт с `relation "folders" already exists`. Дополнительно #89 переименовал уже применённую миграцию `20260701174839_create_students_table.sql` на версию ниже уже применённых миграций. `goose.Up` без `AllowMissing` остановится с `found 1 missing migrations before current version`, а `goose down` больше не сопоставит прежнюю версию с файлом.

**Правила:**

- В каждый момент открыта ровно одна задача, добавляющая файлы в `migrations/`. Остальные PR туда не пишут — нужна колонка, идёшь к владельцу миграций.
- Числовой префикс и имя файла миграции неизменяемы после мержа. В зависимости от нового timestamp переименование создаёт новую либо пропущенную версию; оба варианта ломают линейную историю миграций.
- Порядок разбора текущего затора: **#88 → #89 → #92**. #88 одобрен, не меняет `migrations/` и потому не зависит от #89; #89 задаёт схему, после него #92 ребейзится и удаляет дублирующие миграции.

**Контроль:**

- `migrations/` закреплён через CODEOWNERS за владельцем миграций; одновременно открыт только один PR, добавляющий туда файлы.
- CI накатывает миграции не только на чистую базу, но и поверх схемы текущего `main`, затем выполняет rollback.
- Сравнение имён файлов между открытыми PR не является достаточной проверкой: конфликтующие миграции могут иметь разные timestamp и создавать один объект.

**Чем платим:** PR ждут владельца миграций. Это дешевле, чем три взаимно заблокированных PR.

---

## 8. Содержимое набора хранится в JSONB, карточки не нормализуем

### Вердикт: `packs.config` JSONB — канон; в SQL выносим только то, по чему ищем, фильтруем и раздаём права

Решение принято в `TEMP-packs-spec.md §2`, здесь фиксируется как обязательное для команды.

**Причина:** если разложить карточки по таблицам, появятся две правды — JSON для `.linka`-экспорта и SQL для редактора. Они разойдутся на первом же рефакторинге. Фронт присылает `config` целиком, backend валидирует его JSON Schema 2020-12 (`pkg/linka`) и пишет снапшот в `pack_versions`.

**В SQL выносим:** `folder_id`, `title`, `age_min`/`age_max`, `difficulty`, `goals`, `notes`, избранное, привязку адаптации к ученику, метаданные медиа.

**Следствие для API:** у редактора нет endpoint'а на каждую кнопку. Добавить карточку, дублировать страницу, переключить режим блока, отметить правильный ответ — всё это фронт делает в `config` и присылает документ целиком.

---

## 9. Корневые разделы — значение поля, а не таблица

### Вердикт: `folders.section IN ('library', 'my', 'students')`

Таблицы `sections` нет, endpoint'а `GET /sections` нет. Разделов ровно три, они зашиты в дизайн и у них разная семантика доступа — отдельная таблица создала бы ложное впечатление, что раздел можно добавить через админку.

Фронт получает содержимое раздела запросом `GET /folders?section=my` без `parent_id`.

---

## 10. Публикация в библиотеку — ссылкой, а не копией

### Вердикт: опубликованный набор остаётся одной строкой в `packs`, библиотека показывает его по ссылке

```sql
ALTER TABLE packs
    ADD COLUMN library_folder_id UUID REFERENCES folders(id),
    ADD COLUMN published_at TIMESTAMPTZ,
    ADD CONSTRAINT packs_published_chk
        CHECK ((library_folder_id IS NULL) = (published_at IS NULL));
```

**Почему не копия:** копия расходится с оригиналом с первой же правки. Автор исправляет ошибку в наборе — в библиотеке остаётся ошибка, и никто об этом не узнает.

**Чем платим:** правки автора немедленно видны всем, кто уже взял набор в работу. Отсюда два жёстких правила:

- удаление опубликованного набора возвращает `409` — сначала снять с публикации;
- снятие с публикации не удаляет набор, только убирает его из библиотеки.

**Права:** автор с ролью `defectologist` публикует набор только в собственную папку `library`; `admin` может публиковать в любую папку библиотеки. Снимать публикацию может автор набора или `admin`. `viewer` и `parent` — нет.

**Открыто:** нужна ли модерация публикаций; что происходит с адаптацией под ученика, когда автор правит оригинал.
