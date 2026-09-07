# broker

NATS JetStream — асинхронная обработка задач: генерация озвучки (TTS),
проверка файлов антивирусом (ClamAV), редактирование карточек через LLM.

## Стрим

Один стрим `AI_JOBS` с политикой `WorkQueuePolicy` — сообщение удаляется
после успешного ACK. Хранение на диске, лимиты по возрасту, размеру и
количеству сообщений (см. `config.StreamConfig`).

Subjects:

    ai.llm.req          - запрос на генерацию/правку карточек через LLM
    ai.llm.resp.*       - чанки ответа от LLM (JSON-patch), по request_id
    ai.tts.jobs         - задача на синтез речи
    ai.tts.done.*       - зарезервировано, пока не используется (см. ниже)
    ai.clamav.jobs      - задача на проверку файла антивирусом

## Publisher

`cmd/server` может публиковать задачи через `Publisher`:

    publisher := broker.NewPublisher(js)
    err := publisher.PublishTTSJob(ctx, broker.TTSJob{
        JobId:  "...",
        OrgID:  "...",
        UserID: "...",
        Text:   "...",
        Voice:  "...",
    })

`PublishLLMRequest` и `PublishClamAVJob` работают аналогично.

## Consumer

`Consumer` читает задачи и вызывает обработчик на каждое сообщение.
Отдельного worker-процесса нет: когда появятся прикладные обработчики,
консьюмеры запускаются внутри `cmd/server` в отдельных горутинах с общим
контекстом приложения:

    consumer := broker.NewConsumer(js, cfg.Stream.Name, cfg.Consumers)

    go consumer.ConsumeTTSJobs(ctx, ttsService.HandleTTSJob)

При ошибке хендлера сообщение получает `Nak` и доставляется повторно
(до `MaxDeliver` попыток из конфига). При успехе — `Ack`, сообщение
удаляется из стрима.

## Результаты задач (ClamAVResult)

Структуры объявлены, но сейчас не используются — обратная публикация
результата в NATS и прикладные обработчики пока не реализованы.

## LLM consumer

Subjects `ai.llm.req`/`ai.llm.resp.*` и структуры `LLMRequest`/`LLMChunk`
готовы, но `ConsumeLLMResponses` пока не реализован — фича "AI-ассистент"
ещё не в разработке.

## Конфиг

Все параметры — в `config.NATSConfig` (`config.dev.yml`, секция `nats`):
подключение (reconnect, ping), параметры стрима (лимиты, retention),
настройки консьюмеров (`ack_wait`, `max_deliver`, `fetch_max_wait`) —
отдельно для TTS и ClamAV.

## Деградация при отказе NATS

TECH DEBT: если NATS окончательно недоступен (после исчерпания попыток
реконнекта), сервис продолжает работать — основной функционал (карточки,
авторизация, существующие медиа) не затрагивается. `Publisher` должен
проверять `nc.Status()` перед публикацией и возвращать
`apperr.ErrServiceUnavailable`, если соединение не активно — пока не
реализовано.

## Тесты

`broker_test.go` использует embedded NATS server
(`github.com/nats-io/nats-server/v2/server`) — не требует Docker.
Конфиг берётся из `config.dev.yml`, URL подменяется на адрес embedded
сервера.

    go test ./internal/broker/... -v

## Известные ограничения / Tech debt

- **Деградация при отказе NATS.** Если соединение закрыто окончательно
  (исчерпаны попытки реконнекта), сервис продолжает работать, но
  `Publisher` не проверяет состояние соединения перед публикацией.
  Нужно: `Publisher` должен хранить `*nats.Conn`, проверять `nc.Status()`
  и возвращать `apperr.ErrServiceUnavailable`, если не подключён —
  чтобы AI-функции (TTS, ClamAV, LLM) деградировали явно, а не падали
  с неясной ошибкой публикации.

- **Метрика на исчерпание `MaxDeliver`.** Если сообщение не обработано
  за `MaxDeliver` попыток, оно остаётся в стриме до `MaxAge`, но никак
  не сигнализирует об этом. Нужно: метрика
  (например `broker_jobs_exhausted_total{consumer="..."}`) на основе
  `msg.Metadata().NumDelivered >= MaxDeliver`, плюс алерт в Prometheus.

- **Метрика на недоставленные публикации.** Если `Publisher.Publish*`
  вернул ошибку (NATS недоступен), это сейчас видно только в логах
  вызывающего сервиса. Нужно: счётчик
  `broker_publish_errors_total{subject="..."}` для алертинга.

- **LLM consumer не реализован.** Subjects и структуры (`LLMRequest`,
  `LLMChunk`) готовы, `ConsumeLLMResponses` отсутствует — фича
  "AI-ассистент с естественным языком" пока не в разработке.

- **Обратная публикация результатов не реализована.** Результаты задач (TTS, ClamAV) пишутся воркерами прямо в БД. Subjects ai.tts.done.* и структура ClamAVResult зарезервированы, но не используются. Если требования изменятся (например появится WebSocket-уведомление вместо поллинга) — понадобятся.
