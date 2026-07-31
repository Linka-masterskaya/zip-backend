# Рефакторинг bootstrap: cmd/server → internal/app

Дата: 2026-07-31
Статус: согласовано, готово к нарезке плана

## Цель и контекст

`cmd/server/main.go` — 447 строк, из них `run()` занимает 225. В нём собрана вся инфраструктура,
все доменные модули, десять rate-limit'ов и девять HTTP-роутов вперемешку с уже вынесенными
в `internal/httpapi` регистрациями. Два дефекта в этом коде ломают продакшн-поведение:

1. `os.Exit(1)` внутри горутины `ListenAndServe` (main.go:255) убивает процесс в обход
   `defer deps.db.Close()` и `defer nc.Drain()`. Занятый порт → неотправленные сообщения NATS
   теряются, пул PostgreSQL не закрывается.
2. `initInfra` (main.go:297) создаёт ресурсы последовательно, но при ошибке на шаге N ничего
   из созданного на шагах 1..N-1 не закрывается. Падение `db.New` оставляет открытыми
   NATS-соединение и Redis-клиент.

Цель — вынести сборку и управление жизненным циклом приложения в отдельный пакет, закрыв оба
дефекта структурно, а не точечными заплатками.

## Область

**Входит:** `cmd/server`, новые `internal/app` и `cmd/migrate`, дополнение `internal/httpapi`,
экспорт типа хендлера в `internal/auth`, вынос захардкоженных таймаутов в `internal/config`.

**Не входит:**

- Переезд `internal/*` на горизонтальные слои (`domain`/`usecase`/`adapters`). Пакеты уже
  разложены вертикальными слайсами handler → service → repository с интерфейсами, объявленными
  у потребителя (`packRepository` в `pack/service.go:17`). Это соответствует
  `docs/architecture-decisions.md` и не является источником текущих проблем.
- Декомпозиция `profile/service.go` (735 строк) и других разросшихся файлов.
- Схлопывание `/health` (main.go:235) и `/livez` (main.go:415). Оба остаются как есть, с текущими
  путями и текущей формой ответа. В репозитории их никто не вызывает — попадания в `compose.dev.yaml`
  и `docker-compose.yml` относятся к healthcheck'ам сторонних контейнеров (NATS `:8222/healthz`,
  Grafana, MailHog). Значит потребители живут вне репозитория (k8s-пробы, балансировщик), и убрать
  эндпоинт вслепую — это сломать чужую проверку живости ради экономии девяти строк. Переносятся
  в `internal/app/server.go` без изменения поведения.
- Выделение модулей в отдельные сервисы.

## Решение

Ручной DI без библиотек: `uber-go/fx` резолвит граф в рантайме через reflection (ошибки сборки
всплывают при старте, а не при компиляции), `google/wire` требует кодогенерации в CI и всё равно
оставляет управление жизненным циклом на разработчике. Обе библиотеки — новая зависимость ради
графа из ~40 конструкторов в одном бинаре.

### Раскладка файлов

```
cmd/server/main.go          ~25 строк: app.Bootstrap → app.Run → код возврата
cmd/migrate/main.go         ~30 строк: отдельный бинарь миграций
internal/app/
  app.go                    App, Bootstrap(), Run(ctx)
  closer.go                 LIFO-стек cleanup
  infra.go                  db / redis / nats / minio / smtp / crypto
  modules.go                сборка доменных модулей → httpapi.Handlers
  server.go                 два http.Server + errgroup
internal/httpapi/
  p1.go, picturebank.go     существуют, не меняются
  auth.go, profile.go       новые: забирают инлайн-роуты из run()
  ratelimits.go             новый: все rate-limit'ы в одном конструкторе
```

Ни один доменный пакет, кроме `auth`, не меняется.

### Closer

```go
type Closer struct {
    fns   []func(context.Context) error
    names []string
}

func (c *Closer) Add(name string, fn func(context.Context) error)
func (c *Closer) Close(ctx context.Context) error
```

`Close` идёт в порядке LIFO, логирует ошибку каждого шага и **не прерывается** на первой —
иначе один сбойный ресурс блокирует освобождение остальных. Возвращает первую встреченную ошибку.
Общий таймаут закрытия берётся из переданного контекста.

Правило: ресурс регистрирует свой `Close` немедленно после успешного создания, до следующего
`return err`.

```go
func (a *App) initInfra(cfg *config.Config) error {
    nc, pub, err := initNATS(cfg.NATS)
    if err != nil {
        return fmt.Errorf("nats: %w", err)
    }
    a.closer.Add("nats", func(context.Context) error { return nc.Drain() })

    redisClient, err := cache.NewClient(...)
    if err != nil {
        return fmt.Errorf("redis: %w", err)   // nats уже в стеке — будет закрыт
    }
    a.closer.Add("redis", func(context.Context) error { return redisClient.Close() })
    // ...
}
```

`Bootstrap` при ошибке `initInfra` вызывает `closer.Close(ctx)` перед возвратом.

`storage.Client` (MinIO) и `mailer.SMTPSender` метода закрытия не имеют — в стек не попадают.
Если он появится, регистрация добавляется там же.

### Жизненный цикл

```go
func (a *App) Run(ctx context.Context) error {
    ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    g, gctx := errgroup.WithContext(ctx)
    g.Go(func() error { return serveHTTP(a.apiSrv) })
    g.Go(func() error { return serveHTTP(a.metricsSrv) })
    g.Go(func() error {
        <-gctx.Done()
        return a.shutdown()
    })
    return g.Wait()
}
```

`serveHTTP` — единственное место, где проверяется `errors.Is(err, http.ErrServerClosed)` → `nil`.
Это убирает расхождение между `errors.Is` (main.go:253) и `!=` (main.go:260).

Порядок остановки фиксирован: сперва `Shutdown` обоих HTTP-серверов (перестаём принимать запросы),
затем `closer.Close` в порядке LIFO — db, redis, nats.Drain. Сейчас Redis закрывается инлайн
после shutdown, а db и NATS в `defer`, то есть фактический порядок обратный требуемому.

Поведение при отказе: ошибка `ListenAndServe` (например, занятый порт) отменяет `gctx`, запускается
штатный shutdown, `Run` возвращает ошибку, `main` завершает процесс с кодом 1. Ресурсы освобождены.

`golang.org/x/sync v0.21.0` уже прямая зависимость — новых модулей не требуется.

### Регистрация роутов

Оба новых файла повторяют контракт `RegisterP1Routes`: структура хендлеров + функция регистрации,
принимающая готовые middleware.

```go
// internal/httpapi/auth.go
type AuthHandlers struct{ Auth *auth.Handler }

func RegisterAuthRoutes(mux *http.ServeMux, authMW *middleware.AuthMW, rl RateLimits, h AuthHandlers)
```

Покрывает `login`, `refresh`, `password/forgot`, `password/reset`, `verify-email`,
`verify-email/resend` (последний сохраняет комбинацию IP-лимита и `middleware.RateLimitByUser`,
как в `auth/handler.go:174`).

`internal/httpapi/profile.go` — `RegisterProfileRoutes` для шести роутов профиля, аватара,
смены email и смены пароля.

```go
// internal/httpapi/ratelimits.go
type RateLimits struct {
    Packs, Pictures, Login, Refresh, Forgot, Reset,
    VerifyEmail, VerifyResend, ProfileEmailChange, ProfileEmailConfirm Middleware
    ResendPolicy middleware.RateLimitPolicy
}

func NewRateLimits(c *cache.Client, cfg *config.Config) RateLimits
```

Все лимиты строятся одной функцией. Сейчас восемь собираются в `run()` (main.go:128-135), а ещё два
внутри `auth.RegisterRoutes`, куда для этого передаются `*cache.Client` и весь `*config.Config`.
После переноса пакет `auth` перестаёт импортировать `config` и `cache`.

Требуемая правка в `internal/auth`: `NewAuthHandler` возвращает неэкспортируемый `*authHandlers`
(`handler.go:33`), поэтому назвать тип из `httpapi` нельзя. Переименование `authHandlers` → `Handler`,
`NewAuthHandler` → `NewHandler`, метод `RegisterRoutes` удаляется. Правка механическая, затрагивает
тесты пакета.

### Миграции

`--migrate` уезжает в `cmd/migrate`. Из сервера уходят `flag.Parse()` внутри `initInfra`
(он парсит флаги в глубине инициализации и конфликтует с любым другим объявлением флага),
а также `database/sql` и `_ "github.com/lib/pq"` — второй драйвер PostgreSQL в бинаре,
нужный только этому флагу.

`Makefile:87` — единственный вызывающий: `go run ./cmd/server --migrate` → `go run ./cmd/migrate`.
`Dockerfile.server` флаг не использует, прод миграции этим путём не гоняет.

Порядок инициализации в `Bootstrap` становится корректным: `config.Load` → `logger.Init` →
`metrics.Initialize` → инфраструктура. Сейчас `runMigrationsIfNeeded` логирует до `logger.Init`,
и эти записи уходят в дефолтный обработчик slog.

### Конфигурация

Порт метрик `:9090`, таймауты HTTP-сервера (10s read / 30s write / 60s idle), таймауты
metrics-сервера (5s/5s) и таймаут shutdown (30s) переезжают в `config.ServerConfig`. Текущие
значения становятся значениями по умолчанию — поведение не меняется.

## Критерии приёмки

- `go build ./...`, `go vet ./...`, `golangci-lint run ./...` — чисто.
- `go test ./... -race -count=1` — зелёные.
- `make test-e2e` — зелёные **без правок тестов**. Контракт эндпоинтов не меняется; это основная
  страховка от потери роута при переносе.
- `app/closer_test.go`: порядок LIFO; выполнение продолжается после ошибки одного шага;
  соблюдение таймаута контекста.
- `app/infra_test.go`: при отказе на шаге N закрываются ресурсы шагов 1..N-1 (на фейках,
  без testcontainers).
- Ручная проверка Blocker 1: занять порт 8080, запустить сервер, убедиться в логах, что выполнен
  drain NATS и закрытие пула БД, процесс завершился с кодом 1.
- Ручная проверка миграций: `make migrate-embed` применяет миграции через новый бинарь.
- `/health`, `/livez`, `/readyz` на `:9090` отвечают тем же статусом и тем же телом, что до
  рефакторинга (сверить curl'ом до и после).
- `run()` больше не существует; ни один файл в `internal/app` не превышает ~80 строк.

## Открытые вопросы

- Нужен ли `cmd/migrate` в `Dockerfile.server` (или отдельным образом) для прод-миграций, или
  это осознанно ручная операция. Не блокирует: до ответа образ собирается как сейчас, только
  с сервером.
