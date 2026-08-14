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
                                                  |
                                             MinIO + audio_bank
                                                  |
    GET /tts/{id} --> succeeded + media_id

- **Дедупликация**: повторный запрос с тем же `text + voice` возвращает результат из `audio_bank` без повторного синтеза
- **Worker**: получает задачу из NATS, вызывает внешний API, загружает аудио в MinIO, записывает в `audio_bank`
- **media_files**: при `GET /tts/{id}` со статусом `succeeded` создаётся запись в `media_files` с привязкой к пользователю

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

- `tts_jobs` — задачи на озвучку (статус, text, voice, minio_key)
- `audio_bank` — кеш синтезированных аудио (text+voice -> minio_key), дедупликация
- `app_cache` — кеш голосов (generic JSONB)