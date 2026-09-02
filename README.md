## Миграции БД

Goose определяет миграцию по числовому префиксу имени файла и по умолчанию
отказывается применять версию меньше уже применённой. Отсюда два правила:

* **Timestamp новой миграции должен быть больше всех, что уже есть в `main`.**
  Если ветка живёт долго, перед PR обновитесь от `main` и при необходимости
  переименуйте свои файлы.
* **Имя файла неизменяемо после мержа.** Смена префикса создаёт для goose новую
  версию, и там, где старая уже применена, миграция уедет повторно.

Порядок проверяется в PR-пайплайне и локально:

```bash
bash scripts/check-migration-order.sh
```

Дважды — в июле (AB-40 и AB-43) и в августе (AB-51 и AB-23) — параллельные ветки
добавили миграции с пересекающимися timestamp'ами, и деплой падал с
`found N missing migrations`.

Уже смерженные расхождения применяются: `migrations.Run` вызывает goose с
`WithAllowMissing`, поэтому пропущенная версия накатывается, а не блокирует
деплой.

---

## TTS (Text-to-Speech)

Озвучка текста через внешний сервис [tts.linka.su](https://tts.linka.su). Доступно только авторизованным пользователям.

### API

| Метод | Эндпоинт | Описание |
|-------|----------|----------|
| `POST /api/v1/tts` | `{ "text": "...", "voice": "alena" }` | Создать задачу на озвучку. Возвращает `job_id` |
| `GET /api/v1/tts/{id}` | — | Статус задачи: `pending`, `in_progress`, `succeeded` (+ `media_id`), `failed` |
| `GET /api/v1/tts/voices` | — | Список доступных голосов |

### Архитектура

POST /tts --> Service --> NATS (AI_JOBS) --> Worker --> tts.linka.su
                |                              |
           bank hit?                    MinIO + audio_bank
           media_files                  + media_files
                |                              |
GET /tts/{id} <-- succeeded + media_id

- **Дедупликация**: повторный запрос с тем же `text + voice` возвращает результат из `audio_bank` без повторного синтеза
- **Worker**: получает задачу из NATS, вызывает внешний API, загружает аудио в MinIO, записывает в `audio_bank`
- **media_files**: запись создаётся воркером после синтеза (bank miss) или сервисом при попадании в кеш (bank hit), привязывается к организации и пользователю. GET возвращает готовый media_id.

### Cron-задачи

| Задача | Интервал (prod) | Описание |
|--------|----------------|----------|
| `VoiceRefresher` | 1h | Обновляет кеш голосов в `app_cache` |
| `TTSCleaner` | 6h | Удаляет старые jobs и записи из `audio_bank` без привязки к `media_files` |

### Конфигурация

```yaml
ttsapi:
  service_url: "https://tts.linka.su"
  mime_type: "audio/mpeg"
  timeout: 30s
  rate_limit: 30
  max_text_len: 5000
  max_body_size: 65536

cron:
  voice_refresh:
    interval: 1h
  tts_cleanup:
    interval: 6h
    clean_period: 2160h  # 90 дней
    jobs_ttl: 72h
    limit: 100
```

`TTSAPI_SERVICE_URL` — единственная переменная окружения, остальное в `config.prod.yml`.

### Таблицы

- `tts_jobs` — задачи на озвучку (org_id, text, voice, status, media_id)
- `audio_bank` — кеш синтезированных аудио (text+voice -> minio_key), дедупликация
- `app_cache` — кеш голосов (generic JSONB)

## Работа с текстом для TTS

### Ударения

Для указания ударения в русском тексте используйте знак `+` перед ударной гласной:

- `"поч+ти"` → *поч**и***
- `"з+амок з+амка"` → *з**а**мок з**а**мка*
- `"мук+а мук+и"` → *мук**а** мук**и***

Позволяет однозначно задать произношение для слов с подвижным ударением или омографов.

### Регистр букв

Текст нормализуется в нижний регистр перед синтезом и хранением в кеше. Это означает:

- Регистр не влияет на произношение
- `"Привет"` и `"привет"` — одна запись в кеше (дедупликация)

### Известные ограничения

Английские аббревиатуры (`IBM`, `USA`, `HTML`) произносятся как слова, а не по буквам. Если такое произношение критично — обратитесь к разработчикам, потребуется поддержка SSML-разметки.

---

## Local Pictures Bank

N11 supports a PostgreSQL + MinIO Pictures Bank adapter selected by `feature_flags.local_bank`. System images use the private `system/pictures-bank/` MinIO namespace and are managed with the supported `cmd/picturebank-seed` operator command; the public frontend contract remains unchanged.

See `docs/picturebank/local-bank.md` and `docs/picturebank/ADR-001-local-ingestion.md`.
