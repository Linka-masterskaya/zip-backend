# Bootstrap Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Вынести сборку и жизненный цикл приложения из 225-строчного `run()` в `internal/app`, закрыв утечку ресурсов при частичном отказе инициализации и потерю graceful shutdown при ошибке HTTP-сервера.

**Architecture:** Ручной DI без библиотек. `internal/app` содержит LIFO-стек `Closer`, куда каждый ресурс регистрирует своё закрытие сразу после создания, и `Run` на `errgroup`, где отказ любого сервера отменяет общий контекст и запускает штатную остановку. Регистрация HTTP-роутов целиком уезжает в `internal/httpapi` по образцу существующего `RegisterP1Routes`.

**Tech Stack:** Go 1.25.7, `net/http.ServeMux` (паттерны Go 1.22+), `golang.org/x/sync/errgroup`, pgx/v5, go-redis через `internal/cache`, NATS JetStream, MinIO, viper.

**Spec:** `docs/superpowers/specs/2026-07-31-bootstrap-refactor-design.md`

## Global Constraints

- Модуль: `github.com/Linka-masterskaya/zip-backend`. Все внутренние импорты — с этим префиксом.
- Новых зависимостей в `go.mod` не добавлять. `golang.org/x/sync v0.21.0` уже прямая зависимость.
- Доменные пакеты `internal/*` не трогать, кроме `internal/auth` (Task 3) и `internal/config` (Task 7).
- Публичный HTTP-контракт неизменен: ровно 46 паттернов, те же пути, те же методы, те же middleware в том же порядке.
- Комментарии к экспортируемым сущностям — на английском, как в существующем коде (`// Service contains pack business logic.`).
- Каждая задача завершается зелёным `go build ./... && go vet ./... && go test ./... -race -count=1`.
- Коммит после каждой задачи. Ветка — текущая (`feat/help`), новых веток не создавать.

---

## File Structure

**Создаются:**

| Файл | Ответственность |
|---|---|
| `internal/app/closer.go` | LIFO-стек функций закрытия |
| `internal/app/closer_test.go` | Тесты порядка, устойчивости к ошибкам, таймаута |
| `internal/app/infra.go` | Создание db/redis/nats/minio/smtp/crypto с регистрацией в Closer |
| `internal/app/infra_test.go` | Проверка освобождения при отказе на середине |
| `internal/app/modules.go` | Сборка доменных модулей в `httpapi.Handlers` |
| `internal/app/server.go` | Два `http.Server`, health-эндпоинты, errgroup, shutdown |
| `internal/app/app.go` | `App`, `Bootstrap`, `Run` |
| `internal/httpapi/mux.go` | Интерфейс `Mux` + `Middleware` |
| `internal/httpapi/ratelimits.go` | Все rate-limit'ы одним конструктором |
| `internal/httpapi/auth.go` | 6 auth-роутов |
| `internal/httpapi/profile.go` | 6 profile-роутов |
| `internal/httpapi/routes_test.go` | Golden-список всех 46 паттернов |
| `cmd/migrate/main.go` | Бинарь миграций |

**Модифицируются:**

| Файл | Что |
|---|---|
| `internal/auth/handler.go` | Экспорт типа, удаление `RegisterRoutes` |
| `internal/auth/handler_test.go` | 5 вызовов `NewAuthHandler` → `NewHandler` |
| `e2e/refresh_test.go:58` | То же переименование |
| `internal/httpapi/p1.go` | Параметр `mux` → тип `Mux` |
| `internal/httpapi/picturebank.go` | То же |
| `internal/config/config.go` | `ServerConfig` + дефолты |
| `cmd/server/main.go` | 447 строк → ~25 |
| `Makefile:87` | `go run ./cmd/server --migrate` → `go run ./cmd/migrate` |

---

### Task 1: Closer — LIFO-стек освобождения ресурсов

**Files:**
- Create: `internal/app/closer.go`
- Test: `internal/app/closer_test.go`

**Interfaces:**
- Consumes: ничего
- Produces: `type Closer struct{}`, `func (c *Closer) Add(name string, fn func(context.Context) error)`, `func (c *Closer) Close(ctx context.Context) error`, `func (c *Closer) Len() int`

- [ ] **Step 1: Написать падающий тест**

Create `internal/app/closer_test.go`:

```go
package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCloserRunsInReverseOrder(t *testing.T) {
	var order []string
	var c Closer

	c.Add("first", func(context.Context) error { order = append(order, "first"); return nil })
	c.Add("second", func(context.Context) error { order = append(order, "second"); return nil })
	c.Add("third", func(context.Context) error { order = append(order, "third"); return nil })

	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}

	want := []string{"third", "second", "first"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestCloserContinuesAfterError(t *testing.T) {
	boom := errors.New("boom")
	var closed []string
	var c Closer

	c.Add("bottom", func(context.Context) error { closed = append(closed, "bottom"); return nil })
	c.Add("broken", func(context.Context) error { return boom })
	c.Add("top", func(context.Context) error { closed = append(closed, "top"); return nil })

	err := c.Close(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("Close() = %v, want %v", err, boom)
	}
	if len(closed) != 2 || closed[0] != "top" || closed[1] != "bottom" {
		t.Fatalf("closed = %v, want [top bottom]", closed)
	}
}

func TestCloserStopsOnContextCancel(t *testing.T) {
	var closed []string
	var c Closer

	c.Add("never", func(context.Context) error { closed = append(closed, "never"); return nil })
	c.Add("slow", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if err := c.Close(ctx); err == nil {
		t.Fatal("Close() = nil, want context deadline error")
	}
	if len(closed) != 0 {
		t.Fatalf("closed = %v, want empty: cancelled context must stop the stack", closed)
	}
}

func TestCloserEmpty(t *testing.T) {
	var c Closer
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	if c.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", c.Len())
	}
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/app/ -run TestCloser -v`
Expected: FAIL — `undefined: Closer` (пакет `app` ещё не существует).

- [ ] **Step 3: Реализовать Closer**

Create `internal/app/closer.go`:

```go
// Package app assembles the server application and owns its lifecycle.
package app

import (
	"context"
	"log/slog"

	"github.com/Linka-masterskaya/zip-backend/internal/logger"
)

type closeFn struct {
	name string
	fn   func(context.Context) error
}

// Closer holds resource shutdown functions and runs them in reverse order.
type Closer struct {
	fns []closeFn
}

// Add registers a shutdown function. Resources must register immediately
// after they are successfully created, so a later failure still releases them.
func (c *Closer) Add(name string, fn func(context.Context) error) {
	c.fns = append(c.fns, closeFn{name: name, fn: fn})
}

// Len reports how many shutdown functions are registered.
func (c *Closer) Len() int { return len(c.fns) }

// Close runs every registered function in LIFO order. It does not stop at the
// first failure: one broken resource must not block releasing the others.
// It returns the first error encountered, or the context error if the context
// is done before the stack is drained.
func (c *Closer) Close(ctx context.Context) error {
	var firstErr error

	for i := len(c.fns) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			slog.Error("closer aborted", "remaining", i+1, logger.Err(err))
			if firstErr == nil {
				firstErr = err
			}
			return firstErr
		}

		entry := c.fns[i]
		if err := entry.fn(ctx); err != nil {
			slog.Error("resource close failed", "resource", entry.name, logger.Err(err))
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		slog.Debug("resource closed", "resource", entry.name)
	}

	return firstErr
}
```

Замечание к третьему тесту: `slow` вызывается первым (LIFO), блокируется до истечения контекста и возвращает `ctx.Err()`; на следующей итерации проверка `ctx.Err()` прерывает стек, поэтому `never` не выполняется.

- [ ] **Step 4: Убедиться, что тесты проходят**

Run: `go test ./internal/app/ -run TestCloser -race -v`
Expected: PASS, все четыре теста.

- [ ] **Step 5: Коммит**

```bash
git add internal/app/closer.go internal/app/closer_test.go
git commit -m "feat(app): add LIFO closer for resource shutdown"
```

---

### Task 2: Интерфейс Mux и golden-тест на список роутов

Регистрации меняют тип параметра с `*http.ServeMux` на узкий интерфейс, чтобы тест мог подставить рекордер и зафиксировать полный список паттернов. Это единственная защита от потери роута при переносе: e2e-тесты собирают свой mux (`e2e/refresh_test.go:60` регистрирует login/refresh вручную) и регрессию в auth/profile не увидят.

**Files:**
- Create: `internal/httpapi/mux.go`
- Create: `internal/httpapi/routes_test.go`
- Modify: `internal/httpapi/p1.go:25` (сигнатура), `internal/httpapi/picturebank.go:11` (сигнатура)

**Interfaces:**
- Consumes: `RegisterP1Routes`, `RegisterPictureBankRoutes` (существующие)
- Produces: `type Mux interface { Handle(pattern string, handler http.Handler) }`, `type Middleware = middleware.Middleware`, тестовый `recordingMux` с полем `patterns []string`

- [ ] **Step 1: Создать интерфейс Mux**

Create `internal/httpapi/mux.go`:

```go
package httpapi

import (
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
)

// Mux is the subset of *http.ServeMux used to register routes. Narrowing the
// dependency lets tests record the full pattern set without a real server.
type Mux interface {
	Handle(pattern string, handler http.Handler)
}

// Middleware is an HTTP middleware, re-exported for route registration signatures.
type Middleware = middleware.Middleware
```

- [ ] **Step 2: Поменять сигнатуры существующих регистраций**

In `internal/httpapi/p1.go` заменить параметр:

```go
func RegisterP1Routes(
	mux Mux,
	authMW *middleware.AuthMW,
	rateLimit Middleware,
	handlers P1Handlers,
) {
```

In `internal/httpapi/picturebank.go`:

```go
func RegisterPictureBankRoutes(
	mux Mux,
	authMW *middleware.AuthMW,
	rateLimit Middleware,
	handler *picturebank.Handler,
) {
```

Тела функций не меняются: `*http.ServeMux` удовлетворяет `Mux`, а `func(http.Handler) http.Handler` — это и есть `middleware.Middleware`.

- [ ] **Step 3: Проверить, что всё собирается**

Run: `go build ./... && go test ./e2e -tags=e2e -run XXX`
Expected: сборка проходит, включая `e2e/p1_test.go:521`, который передаёт настоящий `*http.ServeMux`.

- [ ] **Step 4: Написать golden-тест на текущие 34 роута**

Create `internal/httpapi/routes_test.go`:

```go
package httpapi

import (
	"net/http"
	"sort"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
)

type recordingMux struct {
	patterns []string
}

func (m *recordingMux) Handle(pattern string, _ http.Handler) {
	m.patterns = append(m.patterns, pattern)
}

func passthrough(next http.Handler) http.Handler { return next }

func assertPatterns(t *testing.T, got, want []string) {
	t.Helper()
	sort.Strings(got)
	sorted := append([]string(nil), want...)
	sort.Strings(sorted)

	if len(got) != len(sorted) {
		t.Fatalf("registered %d routes, want %d\ngot:  %v\nwant: %v", len(got), len(sorted), got, sorted)
	}
	for i := range sorted {
		if got[i] != sorted[i] {
			t.Fatalf("route mismatch at %d: got %q, want %q", i, got[i], sorted[i])
		}
	}
}

func TestRegisterP1RoutesPatterns(t *testing.T) {
	m := &recordingMux{}
	RegisterP1Routes(m, middleware.NewAuthMW([]byte("test-secret")), passthrough, P1Handlers{})

	assertPatterns(t, m.patterns, []string{
		"POST /api/v1/packs",
		"GET /api/v1/packs/{id}",
		"GET /api/v1/packs",
		"PATCH /api/v1/packs/{id}",
		"DELETE /api/v1/packs/{id}",
		"POST /api/v1/packs/{id}/move",
		"POST /api/v1/packs/{id}/publication",
		"DELETE /api/v1/packs/{id}/publication",
		"PUT /api/v1/packs/{id}/config",
		"GET /api/v1/packs/{id}/export",
		"POST /api/v1/packs/import",
		"POST /api/v1/packs/{id}/students",
		"DELETE /api/v1/packs/{id}/students/{student_id}",
		"POST /api/v1/packs/{id}/versions",
		"GET /api/v1/packs/{id}/versions",
		"GET /api/v1/packs/{id}/versions/{version}",
		"POST /api/v1/packs/{id}/versions/{version}/restore",
		"POST /api/v1/media",
		"GET /api/v1/media/{id}",
		"DELETE /api/v1/media/{id}",
		"POST /api/v1/folders",
		"GET /api/v1/folders",
		"GET /api/v1/folders/{id}/contents",
		"PATCH /api/v1/folders/{id}",
		"POST /api/v1/folders/{id}/move",
		"DELETE /api/v1/folders/{id}",
		"POST /api/v1/students",
		"GET /api/v1/students",
		"PATCH /api/v1/students/{id}",
		"DELETE /api/v1/students/{id}",
	})
}

func TestRegisterPictureBankRoutesPatterns(t *testing.T) {
	m := &recordingMux{}
	RegisterPictureBankRoutes(m, middleware.NewAuthMW([]byte("test-secret")), passthrough, nil)

	assertPatterns(t, m.patterns, []string{
		"GET /api/v1/pictures/categories",
		"GET /api/v1/pictures/search",
		"GET /api/v1/pictures/{id}/content",
		"POST /api/v1/pictures/{id}/import",
	})
}
```

Тест регистрирует роуты с нулевыми хендлерами — методы-значения на nil-указателе берутся без разыменования, вызова не происходит, поэтому паники нет.

- [ ] **Step 5: Прогнать тест**

Run: `go test ./internal/httpapi/ -race -v`
Expected: PASS, оба теста.

- [ ] **Step 6: Коммит**

```bash
git add internal/httpapi/
git commit -m "test(httpapi): pin registered route patterns behind a Mux interface"
```

---

### Task 3: Экспорт типа auth-хендлера

`NewAuthHandler` возвращает неэкспортируемый `*authHandlers` (`internal/auth/handler.go:33`), поэтому назвать тип из `httpapi` невозможно. Метод `RegisterRoutes` пока остаётся — он уедет в Task 5, чтобы каждая задача оставляла сборку зелёной.

**Files:**
- Modify: `internal/auth/handler.go:27,33` и все методы-получатели
- Modify: `internal/auth/handler_test.go` (5 вызовов: строки 90, 166, 245, 333, 434)
- Modify: `cmd/server/main.go:160`
- Modify: `e2e/refresh_test.go:58`

**Interfaces:**
- Consumes: ничего
- Produces: `type Handler struct{}` в пакете `auth`, `func NewHandler(svc authServiceIface, cfg ...Config) *Handler`

- [ ] **Step 1: Переименовать тип и конструктор**

In `internal/auth/handler.go`:

```go
// Handler serves authentication HTTP endpoints.
type Handler struct {
	svc             authServiceIface
	refreshTokenTTL time.Duration
	cookieSecure    bool
}

// NewHandler creates an auth HTTP handler.
func NewHandler(svc authServiceIface, cfg ...Config) *Handler {
	h := &Handler{
		svc: svc,
	}
	// остальное тело без изменений
```

Заменить получатель во всех методах: `func (h *authHandlers)` → `func (h *Handler)`. Затрагиваются `Login`, `VerifyEmail`, `ResendEmail`, `ForgotPassword`, `ResetPassword`, `RegisterRoutes`, `Refresh`.

Механически: `sed -i '' 's/\*authHandlers/*Handler/g; s/&authHandlers{/\&Handler{/g; s/NewAuthHandler/NewHandler/g' internal/auth/handler.go` — затем прочитать файл и убедиться, что не задет комментарий или строковый литерал.

- [ ] **Step 2: Обновить вызывающих**

```bash
sed -i '' 's/NewAuthHandler/NewHandler/g' internal/auth/handler_test.go e2e/refresh_test.go cmd/server/main.go
```

- [ ] **Step 3: Проверить сборку и тесты**

Run: `go build ./... && go vet ./... && go test ./internal/auth/ -race -count=1`
Expected: PASS. Проверить, что `grep -rn NewAuthHandler --include='*.go' .` не даёт попаданий.

- [ ] **Step 4: Коммит**

```bash
git add internal/auth/ e2e/refresh_test.go cmd/server/main.go
git commit -m "refactor(auth): export the HTTP handler type"
```

---

### Task 4: RateLimits — все ограничители в одном конструкторе

**Files:**
- Create: `internal/httpapi/ratelimits.go`
- Test: `internal/httpapi/ratelimits_test.go`

**Interfaces:**
- Consumes: `Middleware` (Task 2)
- Produces: `type RateLimits struct` с полями `Packs, Pictures, Login, Refresh, Forgot, Reset, VerifyEmail, VerifyResend, ProfileEmailChange, ProfileEmailConfirm Middleware` и `ResendPolicy middleware.RateLimitPolicy`; `func NewRateLimits(c *cache.Client, cfg *config.Config) RateLimits`

- [ ] **Step 1: Написать падающий тест**

Create `internal/httpapi/ratelimits_test.go`:

```go
package httpapi

import (
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/config"
)

func TestNewRateLimitsPopulatesEveryLimiter(t *testing.T) {
	cfg := &config.Config{}
	cfg.Auth.PackRateLimit = 60
	cfg.Auth.LoginRateLimit = 5
	cfg.Auth.RefreshRateLimit = 30
	cfg.Auth.ForgotRateLimit = 3
	cfg.Auth.ResetRateLimit = 3
	cfg.Auth.EmailConfirmRateLimit = 10
	cfg.Auth.VerifyResendRateLimit = 3
	cfg.Profile.EmailChangeRateLimit = 3
	cfg.Profile.EmailConfirmRateLimit = 10
	cfg.PicturesBank.InboundPerMinute = 120

	rl := NewRateLimits(nil, cfg)

	limiters := map[string]Middleware{
		"Packs":               rl.Packs,
		"Pictures":            rl.Pictures,
		"Login":               rl.Login,
		"Refresh":             rl.Refresh,
		"Forgot":              rl.Forgot,
		"Reset":               rl.Reset,
		"VerifyEmail":         rl.VerifyEmail,
		"VerifyResend":        rl.VerifyResend,
		"ProfileEmailChange":  rl.ProfileEmailChange,
		"ProfileEmailConfirm": rl.ProfileEmailConfirm,
	}
	for name, limiter := range limiters {
		if limiter == nil {
			t.Errorf("RateLimits.%s is nil", name)
		}
	}
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/httpapi/ -run TestNewRateLimits -v`
Expected: FAIL — `undefined: NewRateLimits`.

- [ ] **Step 3: Реализовать**

Create `internal/httpapi/ratelimits.go`:

```go
package httpapi

import (
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
)

// RateLimits holds every rate limiter used by the public API, so limits live
// in one place instead of being spread across bootstrap and handlers.
type RateLimits struct {
	Packs               Middleware
	Pictures            Middleware
	Login               Middleware
	Refresh             Middleware
	Forgot              Middleware
	Reset               Middleware
	VerifyEmail         Middleware
	VerifyResend        Middleware
	ProfileEmailChange  Middleware
	ProfileEmailConfirm Middleware

	// ResendPolicy is applied per user on top of the IP-based VerifyResend limit.
	ResendPolicy middleware.RateLimitPolicy
}

// NewRateLimits builds all API rate limiters from configuration.
func NewRateLimits(c *cache.Client, cfg *config.Config) RateLimits {
	proxies := cfg.App.TrustedProxies
	limit := func(scope string, n int64) Middleware {
		return middleware.RateLimit(c, scope, n, time.Minute, proxies)
	}

	return RateLimits{
		Packs:               limit("packs_api", int64(cfg.Auth.PackRateLimit)),
		Pictures:            limit("pictures_api", cfg.PicturesBank.InboundPerMinute),
		Login:               limit("login", int64(cfg.Auth.LoginRateLimit)),
		Refresh:             limit("refresh", int64(cfg.Auth.RefreshRateLimit)),
		Forgot:              limit("forgot", int64(cfg.Auth.ForgotRateLimit)),
		Reset:               limit("reset", int64(cfg.Auth.ResetRateLimit)),
		VerifyEmail:         limit("email-confirm", int64(cfg.Auth.EmailConfirmRateLimit)),
		VerifyResend:        limit("verify-resend", int64(cfg.Auth.VerifyResendRateLimit)),
		ProfileEmailChange:  limit("profile-email-change", int64(cfg.Profile.EmailChangeRateLimit)),
		ProfileEmailConfirm: limit("profile-email-confirm", int64(cfg.Profile.EmailConfirmRateLimit)),
		ResendPolicy: middleware.RateLimitPolicy{
			Scope:  cfg.RateLimit.Resend.Scope,
			Limit:  cfg.RateLimit.Resend.Limit,
			Window: cfg.RateLimit.Resend.Window,
		},
	}
}
```

Значения scope скопированы дословно из `cmd/server/main.go:128-135` и `internal/auth/handler.go:180-200` — они являются ключами в Redis, изменение сбросит накопленные счётчики.

- [ ] **Step 4: Прогнать тест**

Run: `go test ./internal/httpapi/ -race -count=1 -v`
Expected: PASS.

Если поля `cfg.RateLimit.Resend.*` названы иначе — свериться с `internal/config/config.go` и поправить, значения не выдумывать.

- [ ] **Step 5: Коммит**

```bash
git add internal/httpapi/ratelimits.go internal/httpapi/ratelimits_test.go
git commit -m "feat(httpapi): build all rate limiters in one constructor"
```

---

### Task 5: Auth-роуты в httpapi

**Files:**
- Create: `internal/httpapi/auth.go`
- Modify: `internal/httpapi/routes_test.go` (добавить тест)
- Modify: `internal/auth/handler.go` (удалить `RegisterRoutes`, строки ~174-219)

**Interfaces:**
- Consumes: `Mux`, `Middleware` (Task 2), `RateLimits` (Task 4), `*auth.Handler` (Task 3)
- Produces: `type AuthHandlers struct { Auth *auth.Handler }`, `func RegisterAuthRoutes(mux Mux, authMW *middleware.AuthMW, rl RateLimits, cacheClient *cache.Client, h AuthHandlers)`

- [ ] **Step 1: Добавить падающий тест**

Append to `internal/httpapi/routes_test.go`:

```go
func TestRegisterAuthRoutesPatterns(t *testing.T) {
	m := &recordingMux{}
	rl := RateLimits{
		Login:        passthrough,
		Refresh:      passthrough,
		Forgot:       passthrough,
		Reset:        passthrough,
		VerifyEmail:  passthrough,
		VerifyResend: passthrough,
	}
	RegisterAuthRoutes(m, middleware.NewAuthMW([]byte("test-secret")), rl, nil, AuthHandlers{})

	assertPatterns(t, m.patterns, []string{
		"POST /api/v1/auth/login",
		"POST /api/v1/auth/refresh",
		"POST /api/v1/auth/password/forgot",
		"POST /api/v1/auth/password/reset",
		"POST /api/v1/auth/verify-email",
		"POST /api/v1/auth/verify-email/resend",
	})
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/httpapi/ -run TestRegisterAuthRoutes -v`
Expected: FAIL — `undefined: RegisterAuthRoutes`.

- [ ] **Step 3: Реализовать регистрацию**

Create `internal/httpapi/auth.go`:

```go
package httpapi

import (
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/auth"
	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
)

// AuthHandlers contains the handlers exposed by the auth API.
type AuthHandlers struct {
	Auth *auth.Handler
}

// RegisterAuthRoutes registers login, refresh, password reset and email verification routes.
func RegisterAuthRoutes(
	mux Mux,
	authMW *middleware.AuthMW,
	rl RateLimits,
	cacheClient *cache.Client,
	h AuthHandlers,
) {
	public := func(limit Middleware, next middleware.AppHandler) http.Handler {
		return limit(middleware.ErrorMiddleware(next))
	}

	mux.Handle("POST /api/v1/auth/login", public(rl.Login, h.Auth.Login))
	mux.Handle("POST /api/v1/auth/refresh", public(rl.Refresh, h.Auth.Refresh))
	mux.Handle("POST /api/v1/auth/password/forgot", public(rl.Forgot, h.Auth.ForgotPassword))
	mux.Handle("POST /api/v1/auth/password/reset", public(rl.Reset, h.Auth.ResetPassword))
	mux.Handle("POST /api/v1/auth/verify-email", public(rl.VerifyEmail, h.Auth.VerifyEmail))

	// Resend is limited twice: by IP before authentication, then per user.
	mux.Handle(
		"POST /api/v1/auth/verify-email/resend",
		rl.VerifyResend(
			middleware.ErrorMiddleware(
				authMW.AuthMiddleware(
					middleware.RateLimitByUser(cacheClient, rl.ResendPolicy)(h.Auth.ResendEmail),
				),
			),
		),
	)
}
```

Порядок обёрток в `verify-email/resend` скопирован из `internal/auth/handler.go:209-217` буквально: внешний IP-лимит → `ErrorMiddleware` → `AuthMiddleware` → пользовательский лимит → хендлер. Менять порядок нельзя: `RateLimitByUser` требует уже проставленного в контексте пользователя.

Методы `VerifyEmail` и `ResendEmail` экспортированы у `*auth.Handler` (`handler.go:119,137`), а вот сервисные методы `verifyEmail`/`resendEmail` — нет; трогать их не нужно.

- [ ] **Step 4: Прогнать тест**

Run: `go test ./internal/httpapi/ -race -count=1 -v`
Expected: PASS, три теста на паттерны.

- [ ] **Step 5: Удалить старую регистрацию из пакета auth**

Удалить метод `RegisterRoutes` целиком (`internal/auth/handler.go`, начиная с `func (h *Handler) RegisterRoutes(` и до закрывающей скобки). После удаления убрать из импортов `internal/cache` и `internal/config`, если они больше не используются в файле.

Удалить вызов `authHandler.RegisterRoutes(mainMux, authMW, deps.redis, deps.cfg)` из `cmd/server/main.go:174` и добавить временно, чтобы не потерять роуты до Task 11:

```go
	httpapi.RegisterAuthRoutes(mainMux, authMW, rateLimits, deps.redis, httpapi.AuthHandlers{Auth: authHandler})
```

где `rateLimits := httpapi.NewRateLimits(deps.redis, deps.cfg)` объявляется рядом с остальными лимитами, а восемь строк `middleware.RateLimit(...)` (main.go:128-135) заменяются на использование полей `rateLimits`. Четыре инлайн-регистрации auth-роутов (main.go:147-171) удаляются как дублирующие.

- [ ] **Step 6: Проверить сборку и весь тестовый набор**

Run: `go build ./... && go vet ./... && go test ./... -race -count=1`
Expected: PASS. `grep -n 'RegisterRoutes' internal/auth/handler.go` — пусто.

- [ ] **Step 7: Коммит**

```bash
git add internal/httpapi/ internal/auth/handler.go cmd/server/main.go
git commit -m "refactor(httpapi): move auth route registration out of the auth package"
```

---

### Task 6: Profile-роуты в httpapi

**Files:**
- Create: `internal/httpapi/profile.go`
- Modify: `internal/httpapi/routes_test.go`
- Modify: `cmd/server/main.go` (удалить 6 инлайн-регистраций, строки ~183-214)

**Interfaces:**
- Consumes: `Mux`, `Middleware`, `RateLimits`
- Produces: `type ProfileHandlers struct { Profile *profile.Handler; ChangePassword *profile.ChangePasswordHandler }`, `func RegisterProfileRoutes(mux Mux, authMW *middleware.AuthMW, rl RateLimits, h ProfileHandlers)`

- [ ] **Step 1: Добавить падающий тест**

Append to `internal/httpapi/routes_test.go`:

```go
func TestRegisterProfileRoutesPatterns(t *testing.T) {
	m := &recordingMux{}
	rl := RateLimits{
		ProfileEmailChange:  passthrough,
		ProfileEmailConfirm: passthrough,
	}
	RegisterProfileRoutes(m, middleware.NewAuthMW([]byte("test-secret")), rl, ProfileHandlers{})

	assertPatterns(t, m.patterns, []string{
		"GET /api/v1/profile/me",
		"PUT /api/v1/profile/me/avatar",
		"DELETE /api/v1/profile/me/avatar",
		"POST /api/v1/profile/me/email",
		"POST /api/v1/profile/me/email/confirm",
		"POST /api/v1/profile/me/password",
	})
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/httpapi/ -run TestRegisterProfileRoutes -v`
Expected: FAIL — `undefined: RegisterProfileRoutes`.

- [ ] **Step 3: Реализовать**

Create `internal/httpapi/profile.go`:

```go
package httpapi

import (
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/Linka-masterskaya/zip-backend/internal/profile"
)

// ProfileHandlers contains the handlers exposed by the profile API.
type ProfileHandlers struct {
	Profile        *profile.Handler
	ChangePassword *profile.ChangePasswordHandler
}

// RegisterProfileRoutes registers profile, avatar, email change and password change routes.
func RegisterProfileRoutes(mux Mux, authMW *middleware.AuthMW, rl RateLimits, h ProfileHandlers) {
	protected := func(next middleware.AppHandler) http.Handler {
		return middleware.ErrorMiddleware(authMW.AuthMiddleware(next))
	}
	limited := func(limit Middleware, next middleware.AppHandler) http.Handler {
		return limit(middleware.ErrorMiddleware(authMW.AuthMiddleware(next)))
	}

	mux.Handle("GET /api/v1/profile/me", protected(h.Profile.GetProfile))
	mux.Handle("PUT /api/v1/profile/me/avatar", protected(h.Profile.UploadAvatar))
	mux.Handle("DELETE /api/v1/profile/me/avatar", protected(h.Profile.DeleteAvatar))
	mux.Handle("POST /api/v1/profile/me/email", limited(rl.ProfileEmailChange, h.Profile.RequestEmailChange))
	mux.Handle("POST /api/v1/profile/me/password", protected(h.ChangePassword.ChangePassword))

	// Confirmation arrives from an email link, so it is rate limited but not authenticated.
	mux.Handle(
		"POST /api/v1/profile/me/email/confirm",
		rl.ProfileEmailConfirm(middleware.ErrorMiddleware(h.Profile.ConfirmEmailChange)),
	)
}
```

Существенная деталь: `ConfirmEmailChange` в `cmd/server/main.go:201-206` идёт **без** `AuthMiddleware` — переход по ссылке из письма происходит без access-токена. Обернуть его в `protected` значит сломать подтверждение почты.

- [ ] **Step 4: Прогнать тест**

Run: `go test ./internal/httpapi/ -race -count=1 -v`
Expected: PASS, четыре теста на паттерны.

- [ ] **Step 5: Переключить main.go на новую регистрацию**

Заменить шесть блоков `mainMux.Handle(...)` для профиля (main.go:183-214) одной строкой:

```go
	httpapi.RegisterProfileRoutes(mainMux, authMW, rateLimits, httpapi.ProfileHandlers{
		Profile:        profileHandler,
		ChangePassword: changePasswordHandler,
	})
```

- [ ] **Step 6: Проверить**

Run: `go build ./... && go vet ./... && go test ./... -race -count=1`
Expected: PASS.

- [ ] **Step 7: Коммит**

```bash
git add internal/httpapi/ cmd/server/main.go
git commit -m "refactor(httpapi): move profile route registration out of bootstrap"
```

---

### Task 7: ServerConfig — таймауты и порт метрик из конфигурации

**Files:**
- Modify: `internal/config/config.go` (структура `Config`, новый тип, дефолты рядом со строкой 262)
- Test: `internal/config/config_test.go` (создать, если отсутствует)

**Interfaces:**
- Consumes: ничего
- Produces: `config.ServerConfig` с полями `MetricsPort string`, `ReadTimeout, WriteTimeout, IdleTimeout, MetricsReadTimeout, MetricsWriteTimeout, ShutdownTimeout time.Duration`; поле `Server ServerConfig` в `config.Config`

- [ ] **Step 1: Написать падающий тест**

Create or append `internal/config/config_test.go`:

```go
package config

import (
	"testing"
	"time"
)

func TestServerDefaults(t *testing.T) {
	cfg, err := Load("../../config/config.dev.yml")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	cases := []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"read", cfg.Server.ReadTimeout, 10 * time.Second},
		{"write", cfg.Server.WriteTimeout, 30 * time.Second},
		{"idle", cfg.Server.IdleTimeout, 60 * time.Second},
		{"metrics read", cfg.Server.MetricsReadTimeout, 5 * time.Second},
		{"metrics write", cfg.Server.MetricsWriteTimeout, 5 * time.Second},
		{"shutdown", cfg.Server.ShutdownTimeout, 30 * time.Second},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s timeout = %v, want %v", c.name, c.got, c.want)
		}
	}
	if cfg.Server.MetricsPort != "9090" {
		t.Errorf("MetricsPort = %q, want %q", cfg.Server.MetricsPort, "9090")
	}
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/config/ -run TestServerDefaults -v`
Expected: FAIL — `cfg.Server undefined`.

- [ ] **Step 3: Добавить структуру и дефолты**

In `internal/config/config.go`, добавить поле в `Config` (рядом со строкой 15):

```go
	Server       ServerConfig       `mapstructure:"server"`
```

И новый тип:

```go
// ServerConfig contains HTTP server ports and timeouts.
type ServerConfig struct {
	MetricsPort         string        `mapstructure:"metrics_port"`
	ReadTimeout         time.Duration `mapstructure:"read_timeout"`
	WriteTimeout        time.Duration `mapstructure:"write_timeout"`
	IdleTimeout         time.Duration `mapstructure:"idle_timeout"`
	MetricsReadTimeout  time.Duration `mapstructure:"metrics_read_timeout"`
	MetricsWriteTimeout time.Duration `mapstructure:"metrics_write_timeout"`
	ShutdownTimeout     time.Duration `mapstructure:"shutdown_timeout"`
}
```

Дефолты рядом с остальными (после блока `app.*`, ~строка 267):

```go
	// Server
	v.SetDefault("server.metrics_port", "9090")
	v.SetDefault("server.read_timeout", "10s")
	v.SetDefault("server.write_timeout", "30s")
	v.SetDefault("server.idle_timeout", "60s")
	v.SetDefault("server.metrics_read_timeout", "5s")
	v.SetDefault("server.metrics_write_timeout", "5s")
	v.SetDefault("server.shutdown_timeout", "30s")
```

Значения равны текущим захардкоженным в `cmd/server/main.go:228-243,270` — поведение не меняется. В `config/config.dev.yml` секцию `server:` не добавлять: дефолтов достаточно, а лишняя секция создаст два источника правды.

- [ ] **Step 4: Прогнать тест**

Run: `go test ./internal/config/ -race -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Коммит**

```bash
git add internal/config/
git commit -m "feat(config): move server ports and timeouts into configuration"
```

---

### Task 8: infra.go — инфраструктура с регистрацией в Closer

**Files:**
- Create: `internal/app/infra.go`
- Test: `internal/app/infra_test.go`

**Interfaces:**
- Consumes: `Closer` (Task 1)
- Produces: `type infra struct` с полями `cfg *config.Config`, `db *pgxpool.Pool`, `redis *cache.Client`, `nc *nats.Conn`, `pub *broker.Publisher`, `crypto *cryptox.Cryptox`, `mailer *mailer.SMTPSender`, `storage *storage.Client`; `func initInfra(cfg *config.Config, closer *Closer) (*infra, error)`; `func initNATS(cfg config.NATSConfig) (*nats.Conn, *broker.Publisher, error)`

- [ ] **Step 1: Написать тест на освобождение при отказе**

Create `internal/app/infra_test.go`:

```go
package app

import (
	"context"
	"errors"
	"testing"
)

// Ресурсы инфраструктуры требуют живых Postgres/Redis/NATS/MinIO, поэтому
// проверяется сам контракт: последовательность шагов, где каждый успешный шаг
// регистрирует освобождение до того, как выполнится следующий.
func TestInitStepsReleaseOnFailure(t *testing.T) {
	var closed []string
	var c Closer

	steps := []struct {
		name string
		fail bool
	}{
		{name: "storage"},
		{name: "nats"},
		{name: "redis"},
		{name: "postgres", fail: true},
		{name: "crypto"},
	}

	boom := errors.New("postgres unavailable")
	var initErr error
	for _, s := range steps {
		if s.fail {
			initErr = boom
			break
		}
		name := s.name
		c.Add(name, func(context.Context) error { closed = append(closed, name); return nil })
	}

	if initErr == nil {
		t.Fatal("expected init failure")
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}

	want := []string{"redis", "nats", "storage"}
	if len(closed) != len(want) {
		t.Fatalf("closed = %v, want %v", closed, want)
	}
	for i := range want {
		if closed[i] != want[i] {
			t.Fatalf("closed = %v, want %v", closed, want)
		}
	}
}

func TestInitInfraFailsOnUnreachableDependencies(t *testing.T) {
	cfg := minimalUnreachableConfig()
	var c Closer

	_, err := initInfra(cfg, &c)
	if err == nil {
		t.Fatal("initInfra() = nil error, want failure on unreachable dependencies")
	}
	if closeErr := c.Close(context.Background()); closeErr != nil {
		t.Errorf("Close() after failed init = %v, want nil", closeErr)
	}
}
```

Хелпер `minimalUnreachableConfig` добавить в тот же файл:

```go
func minimalUnreachableConfig() *config.Config {
	cfg := &config.Config{}
	cfg.MinIO.Endpoint = "127.0.0.1:1"
	cfg.NATS.Connection.URL = "nats://127.0.0.1:1"
	cfg.Redis.URL = "redis://127.0.0.1:1/0"
	cfg.DB.URL = "postgres://127.0.0.1:1/none"
	return cfg
}
```

Импортировать `"github.com/Linka-masterskaya/zip-backend/internal/config"`.

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/app/ -run TestInit -v`
Expected: FAIL — `undefined: initInfra`.

- [ ] **Step 3: Реализовать**

Create `internal/app/infra.go`:

```go
package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/Linka-masterskaya/zip-backend/internal/broker"
	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/cryptox"
	"github.com/Linka-masterskaya/zip-backend/internal/db"
	"github.com/Linka-masterskaya/zip-backend/internal/mailer"
	"github.com/Linka-masterskaya/zip-backend/internal/storage"
)

type infra struct {
	cfg     *config.Config
	db      *pgxpool.Pool
	redis   *cache.Client
	nc      *nats.Conn
	pub     *broker.Publisher
	crypto  *cryptox.Cryptox
	mailer  *mailer.SMTPSender
	storage *storage.Client
}

// initInfra creates every external dependency. Each resource registers its
// shutdown with the closer immediately after it is created, so a failure at any
// step still releases everything created before it.
func initInfra(cfg *config.Config, closer *Closer) (*infra, error) {
	storageClient, err := storage.New(cfg.MinIO)
	if err != nil {
		return nil, fmt.Errorf("minio connect: %w", err)
	}

	nc, pub, err := initNATS(cfg.NATS)
	if err != nil {
		return nil, fmt.Errorf("nats init: %w", err)
	}
	closer.Add("nats", func(context.Context) error { return nc.Drain() })

	redisClient, err := cache.NewClient(cache.Config{
		URL:        cfg.Redis.URL,
		ClientName: cfg.Redis.ClientName,
		PoolSize:   cfg.Redis.PoolSize,
	})
	if err != nil {
		return nil, fmt.Errorf("redis init: %w", err)
	}
	closer.Add("redis", func(context.Context) error { return redisClient.Close() })

	dbPool, err := db.New(cfg.DB)
	if err != nil {
		return nil, fmt.Errorf("postgres init: %w", err)
	}
	closer.Add("postgres", func(context.Context) error { dbPool.Close(); return nil })

	cryptoClient, err := cryptox.New(cfg.Crypto.AESKey, cfg.Crypto.HMACKey)
	if err != nil {
		return nil, fmt.Errorf("cryptox init: %w", err)
	}

	smtpSender, err := mailer.NewSMTPSender(cfg.SMTP, cfg.App.FrontendURL)
	if err != nil {
		return nil, fmt.Errorf("smtp init: %w", err)
	}

	return &infra{
		cfg:     cfg,
		db:      dbPool,
		redis:   redisClient,
		nc:      nc,
		pub:     pub,
		crypto:  cryptoClient,
		mailer:  smtpSender,
		storage: storageClient,
	}, nil
}

func initNATS(cfg config.NATSConfig) (*nats.Conn, *broker.Publisher, error) {
	nc, err := broker.New(cfg.Connection)
	if err != nil {
		return nil, nil, fmt.Errorf("initNATS: %w", err)
	}
	slog.Info("nats connected", "url", cfg.Connection.URL)

	js, err := jetstream.New(nc)
	if err != nil {
		return nil, nil, fmt.Errorf("initNATS: jetstream: %w", err)
	}

	if err := broker.InitStreams(cfg.Stream, js); err != nil {
		return nil, nil, fmt.Errorf("initNATS: streams: %w", err)
	}
	slog.Info("jetstream stream ready", "stream", cfg.Stream.Name)

	return nc, broker.NewPublisher(js), nil
}
```

`storage.Client` и `mailer.SMTPSender` метода закрытия не имеют — в стек не регистрируются. `pgxpool.Pool.Close()` ошибку не возвращает, поэтому обёртка отдаёт `nil`.

- [ ] **Step 4: Прогнать тесты**

Run: `go test ./internal/app/ -race -count=1 -v`
Expected: PASS. Второй тест может выполняться до нескольких секунд из-за таймаутов подключения — это нормально.

- [ ] **Step 5: Коммит**

```bash
git add internal/app/infra.go internal/app/infra_test.go
git commit -m "feat(app): init infrastructure with guaranteed release on partial failure"
```

---

### Task 9: modules.go — сборка доменных модулей

**Files:**
- Create: `internal/app/modules.go`

**Interfaces:**
- Consumes: `infra` (Task 8), `httpapi.P1Handlers`, `httpapi.AuthHandlers` (Task 5), `httpapi.ProfileHandlers` (Task 6)
- Produces: `type modules struct { p1 httpapi.P1Handlers; auth httpapi.AuthHandlers; profile httpapi.ProfileHandlers; pictures *picturebank.Handler; checker *health.Checker; authCfg auth.Config }`; `func buildModules(in *infra) (*modules, error)`

- [ ] **Step 1: Реализовать сборку**

Create `internal/app/modules.go`:

```go
package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/Linka-masterskaya/zip-backend/internal/auth"
	"github.com/Linka-masterskaya/zip-backend/internal/folder"
	"github.com/Linka-masterskaya/zip-backend/internal/health"
	"github.com/Linka-masterskaya/zip-backend/internal/httpapi"
	"github.com/Linka-masterskaya/zip-backend/internal/media"
	"github.com/Linka-masterskaya/zip-backend/internal/pack"
	"github.com/Linka-masterskaya/zip-backend/internal/picturebank"
	"github.com/Linka-masterskaya/zip-backend/internal/profile"
	"github.com/Linka-masterskaya/zip-backend/internal/student"
)

type modules struct {
	p1       httpapi.P1Handlers
	auth     httpapi.AuthHandlers
	profile  httpapi.ProfileHandlers
	pictures *picturebank.Handler
	checker  *health.Checker
}

// buildModules wires every domain module on top of the infrastructure.
func buildModules(in *infra) (*modules, error) {
	cfg := in.cfg

	packRepo := pack.NewRepository(in.db)
	packService := pack.NewService(packRepo, in.pub)
	mediaRepo := media.NewRepository(in.db)
	mediaService := media.NewService(mediaRepo, in.storage)

	folderRepo := folder.NewRepository(in.db)
	studentRepo := student.NewRepository(in.db)

	picturesSource, err := picturebank.NewSource(cfg.FeatureFlags.LocalBank, cfg.PicturesBank, in.redis)
	if err != nil {
		return nil, fmt.Errorf("pictures bank source: %w", err)
	}
	picturesService := picturebank.NewService(picturesSource)

	// Export substitutes a placeholder when a source picture is gone, so a
	// deleted picture cannot fail the whole archive.
	contentService := pack.NewContentService(
		packRepo, in.storage, mediaService, packService,
		func(ctx context.Context, id uuid.UUID) ([]byte, string, error) {
			image, loadErr := picturesService.Image(ctx, id.String())
			if errors.Is(loadErr, picturebank.ErrPictureNotFound) {
				image = picturebank.DeletedPicturePlaceholder()
				loadErr = nil
			}
			if loadErr != nil {
				return nil, "", loadErr
			}
			return image.Data, image.ContentType, nil
		},
	)

	authCfg := auth.Config{
		JWTSecret:                cfg.JWT.Secret,
		FrontendURL:              cfg.App.FrontendURL,
		AccessTokenTTL:           cfg.Auth.AccessTokenTTL,
		RefreshTokenTTL:          cfg.Auth.RefreshTokenTTL,
		VerifyEmailTokenTTL:      cfg.Auth.VerifyEmailTokenTTL,
		ResetPasswordTokenTTL:    cfg.Auth.ResetPasswordTokenTTL,
		BcryptCost:               cfg.Auth.BcryptCost,
		RequireEmailVerification: cfg.Auth.RequireEmailVerification,
		CookieSecure:             cfg.Auth.CookieSecure,
	}
	authService := auth.NewAuthService(auth.NewAuthRepo(in.db), in.redis, in.mailer, authCfg, in.crypto)

	profileService := profile.NewService(
		profile.NewRepository(in.db), in.storage, in.mailer, in.crypto, in.redis,
		profile.EmailConfig{
			EmailChangeTTL: cfg.Profile.EmailChangeTTL,
			EmailVerifyTTL: cfg.Profile.EmailVerifyTTL,
		},
	)
	changePasswordService := profile.NewChangePasswordService(profile.NewChangePasswordRepo(in.db), in.redis)

	checker, err := health.NewChecker(in.db, in.redis, in.nc, in.storage, health.PicturesBank{
		Local: cfg.FeatureFlags.LocalBank,
		URL:   cfg.PicturesBank.URL,
	})
	if err != nil {
		return nil, fmt.Errorf("health checker init: %w", err)
	}

	return &modules{
		p1: httpapi.P1Handlers{
			Pack:    pack.NewHandler(packService),
			Content: pack.NewContentHandler(contentService),
			Media:   media.NewHandler(mediaService),
			Folder:  folder.NewHandler(folder.NewService(folderRepo)),
			Student: student.NewHandler(student.NewService(studentRepo, in.crypto)),
		},
		auth: httpapi.AuthHandlers{
			Auth: auth.NewHandler(authService, authCfg),
		},
		profile: httpapi.ProfileHandlers{
			Profile:        profile.NewHandler(profileService),
			ChangePassword: profile.NewChangePasswordHandler(changePasswordService),
		},
		pictures: picturebank.NewHandler(picturesService, cfg.PicturesBank.CacheTTL),
		checker:  checker,
	}, nil
}
```

Все конструкторы и порядок аргументов перенесены из `cmd/server/main.go:72-137` без изменений. Обратить внимание на сигнатуры — они отличаются от «очевидных»: `picturebank.NewService` принимает только источник, `picturebank.NewHandler` вторым аргументом берёт TTL кеша, а `pack.NewContentService` — пятым аргументом функцию загрузки картинки. Свериться с текущим `main.go`, а не додумывать.

- [ ] **Step 2: Проверить компиляцию**

Run: `go build ./... && go vet ./internal/app/`
Expected: чисто. Если сигнатура конструктора не совпала — свериться с исходным `main.go`, а не подгонять аргументы наугад.

- [ ] **Step 3: Коммит**

```bash
git add internal/app/modules.go
git commit -m "feat(app): assemble domain modules in a dedicated file"
```

---

### Task 10: server.go и app.go — HTTP-серверы и жизненный цикл

**Files:**
- Create: `internal/app/server.go`
- Create: `internal/app/app.go`
- Test: `internal/app/server_test.go`

**Interfaces:**
- Consumes: `Closer`, `initInfra`, `buildModules`, `config.ServerConfig` (Task 7)
- Produces: `type App struct`, `func Bootstrap(cfgPath string) (*App, error)`, `func (a *App) Run(ctx context.Context) error`, `func serveHTTP(srv *http.Server) error`, `func newAPIServer(...) *http.Server`, `func newMetricsServer(...) *http.Server`

- [ ] **Step 1: Написать падающий тест на serveHTTP**

Create `internal/app/server_test.go`:

```go
package app

import (
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServeHTTPTreatsServerClosedAsSuccess(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: http.NewServeMux(), ReadHeaderTimeout: time.Second}

	done := make(chan error, 1)
	go func() { done <- serveHTTPListener(srv, ln) }()

	time.Sleep(50 * time.Millisecond)
	if err := srv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveHTTP after Close = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serveHTTP did not return after Close")
	}
}

func TestServeHTTPReturnsBindError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	srv := &http.Server{Addr: ln.Addr().String(), Handler: http.NewServeMux(), ReadHeaderTimeout: time.Second}
	err = serveHTTP(srv)
	if err == nil {
		t.Fatal("serveHTTP on a busy port = nil, want bind error")
	}
	if errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("serveHTTP = %v, want a bind error, not ErrServerClosed", err)
	}
}
```

- [ ] **Step 2: Убедиться, что тест падает**

Run: `go test ./internal/app/ -run TestServeHTTP -v`
Expected: FAIL — `undefined: serveHTTP`.

- [ ] **Step 3: Реализовать серверы**

Create `internal/app/server.go`:

```go
package app

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/health"
	"github.com/Linka-masterskaya/zip-backend/internal/httpapi"
	"github.com/Linka-masterskaya/zip-backend/internal/logger"
	"github.com/Linka-masterskaya/zip-backend/internal/metrics"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
)

// serveHTTP runs a server and reports a graceful shutdown as success.
func serveHTTP(srv *http.Server) error {
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// serveHTTPListener is serveHTTP on a pre-created listener, used by tests.
func serveHTTPListener(srv *http.Server, ln net.Listener) error {
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func newAPIServer(cfg *config.Config, mods *modules, rl httpapi.RateLimits, redis *cache.Client) *http.Server {
	authMW := middleware.NewAuthMW([]byte(cfg.JWT.Secret))
	mux := http.NewServeMux()

	httpapi.RegisterP1Routes(mux, authMW, rl.Packs, mods.p1)
	httpapi.RegisterPictureBankRoutes(mux, authMW, rl.Pictures, mods.pictures)
	httpapi.RegisterAuthRoutes(mux, authMW, rl, redis, mods.auth)
	httpapi.RegisterProfileRoutes(mux, authMW, rl, mods.profile)

	handler := middleware.Chain(
		mux,
		middleware.RecoveryMiddleware,
		middleware.RequestIDMiddleware,
		middleware.Metrics,
		middleware.CORSMiddleware(cfg.App.FrontendURL),
		middleware.SecurityHeaders,
	)

	return &http.Server{
		Addr:         ":" + cfg.App.Port,
		Handler:      handler,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}
}

func newMetricsServer(cfg *config.Config, checker *health.Checker) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics.NewHandler())
	mux.HandleFunc("GET /health", healthHandler(cfg.App.Env))
	mux.HandleFunc("GET /livez", livezHandler)
	mux.HandleFunc("GET /readyz", readyzHandler(checker))

	return &http.Server{
		Addr:         ":" + cfg.Server.MetricsPort,
		Handler:      mux,
		ReadTimeout:  cfg.Server.MetricsReadTimeout,
		WriteTimeout: cfg.Server.MetricsWriteTimeout,
	}
}

func healthHandler(env string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"status": health.StatusOK, "env": env}); err != nil {
			slog.Error("health response encode failed", logger.Err(err))
		}
	}
}

func livezHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]health.Status{"status": health.StatusAlive}); err != nil {
		slog.Error("failed to encode /livez response", logger.Err(err))
	}
}

func readyzHandler(checker *health.Checker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, body := checker.Run(r.Context())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if err := json.NewEncoder(w).Encode(body); err != nil {
			slog.Error("failed to encode /readyz response", logger.Err(err))
		}
	}
}
```

Три health-эндпоинта перенесены с сохранением путей, статусов и формы тела (`cmd/server/main.go:375-382,413-432`). `/health` и `/livez` дублируют друг друга — это осознанно оставлено, потребители живут вне репозитория.

- [ ] **Step 4: Реализовать App**

Create `internal/app/app.go`:

```go
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"

	"golang.org/x/sync/errgroup"

	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/httpapi"
	"github.com/Linka-masterskaya/zip-backend/internal/logger"
	"github.com/Linka-masterskaya/zip-backend/internal/metrics"
)

var (
	// Version and BuildTime are injected at build time via -ldflags.
	Version   string
	BuildTime string
)

// App owns the assembled server and everything it must release on shutdown.
type App struct {
	cfg        *config.Config
	closer     *Closer
	apiSrv     *http.Server
	metricsSrv *http.Server
}

// Bootstrap loads configuration, creates infrastructure and wires the servers.
// On failure it releases whatever was already created.
func Bootstrap(cfgPath string) (*App, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("config load: %w", err)
	}

	logger.Init(cfg.App.Env)
	metrics.Initialize()

	closer := &Closer{}
	in, err := initInfra(cfg, closer)
	if err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
		defer cancel()
		if closeErr := closer.Close(ctx); closeErr != nil {
			slog.Error("cleanup after failed bootstrap", logger.Err(closeErr))
		}
		return nil, err
	}

	mods, err := buildModules(in)
	if err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
		defer cancel()
		if closeErr := closer.Close(ctx); closeErr != nil {
			slog.Error("cleanup after failed bootstrap", logger.Err(closeErr))
		}
		return nil, err
	}

	rl := httpapi.NewRateLimits(in.redis, cfg)

	return &App{
		cfg:        cfg,
		closer:     closer,
		apiSrv:     newAPIServer(cfg, mods, rl, in.redis),
		metricsSrv: newMetricsServer(cfg, mods.checker),
	}, nil
}

// Run serves until a termination signal arrives or a server fails, then shuts
// everything down in order: HTTP servers first, then infrastructure in LIFO order.
func (a *App) Run(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	slog.Info("starting server",
		"addr", a.apiSrv.Addr,
		"metrics", a.metricsSrv.Addr,
		"env", a.cfg.App.Env,
		"version", Version,
		"buildTime", BuildTime,
	)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return serveHTTP(a.apiSrv) })
	g.Go(func() error { return serveHTTP(a.metricsSrv) })
	g.Go(func() error {
		<-gctx.Done()
		return a.shutdown()
	})

	return g.Wait()
}

func (a *App) shutdown() error {
	slog.Info("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), a.cfg.Server.ShutdownTimeout)
	defer cancel()

	var firstErr error
	if err := a.metricsSrv.Shutdown(ctx); err != nil {
		slog.Error("metrics server shutdown", logger.Err(err))
		firstErr = err
	}
	if err := a.apiSrv.Shutdown(ctx); err != nil {
		slog.Error("api server shutdown", logger.Err(err))
		if firstErr == nil {
			firstErr = err
		}
	}
	if err := a.closer.Close(ctx); err != nil && firstErr == nil {
		firstErr = err
	}

	return firstErr
}
```

Порядок в `shutdown` обязателен: сперва перестаём принимать HTTP-запросы, только потом закрываем БД и Redis — иначе обрабатываемые запросы получат закрытый пул.

- [ ] **Step 5: Прогнать тесты**

Run: `go build ./... && go test ./internal/app/ -race -count=1 -v`
Expected: PASS, все тесты пакета.

- [ ] **Step 6: Коммит**

```bash
git add internal/app/
git commit -m "feat(app): run servers under errgroup with ordered shutdown"
```

---

### Task 11: Сжать cmd/server/main.go

**Files:**
- Modify: `cmd/server/main.go` (447 строк → ~30)

**Interfaces:**
- Consumes: `app.Bootstrap`, `app.Run`, `app.Version`, `app.BuildTime`
- Produces: ничего

- [ ] **Step 1: Заменить содержимое**

Replace `cmd/server/main.go` entirely:

```go
// Command server runs the HTTP API server.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/Linka-masterskaya/zip-backend/internal/app"
)

const defaultConfigPath = "config/config.dev.yml"

func main() {
	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = defaultConfigPath
	}

	application, err := app.Bootstrap(cfgPath)
	if err != nil {
		slog.Error("bootstrap failed", "err", err)
		os.Exit(1)
	}

	if err := application.Run(context.Background()); err != nil {
		slog.Error("application failed", "err", err)
		os.Exit(1)
	}
}
```

Переменные `version`/`buildTime` переехали в `app.Version`/`app.BuildTime`. Проверить `Makefile` и `Dockerfile.server` на `-ldflags` с `main.version`: если такие есть, поменять путь на `github.com/Linka-masterskaya/zip-backend/internal/app.Version`. Если `-ldflags` не используются — ничего не делать.

- [ ] **Step 2: Проверить, что старый код удалён**

Run:
```bash
grep -c '' cmd/server/main.go
grep -n 'runMigrationsIfNeeded\|flag\.\|database/sql\|lib/pq' cmd/server/main.go
```
Expected: меньше 35 строк; второй grep — пусто.

- [ ] **Step 3: Полная проверка**

Run: `go build ./... && go vet ./... && go test ./... -race -count=1`
Expected: PASS.

- [ ] **Step 4: Проверить сервер вживую**

```bash
docker compose -f compose.dev.yaml up -d postgres minio nats redis
CONFIG_PATH=config/config.dev.yml FEATURE_LOCAL_BANK=false go run ./cmd/server
```
Expected: лог `starting server`. В другом терминале:
```bash
curl -s localhost:9090/health; echo
curl -s localhost:9090/livez; echo
curl -s -o /dev/null -w '%{http_code}\n' localhost:9090/readyz
curl -s -o /dev/null -w '%{http_code}\n' -X POST localhost:8080/api/v1/auth/login
```
Expected: `/health` и `/livez` отдают JSON, `/readyz` — 200, login — 400 (а не 404: роут на месте).
Затем Ctrl+C: в логах `shutting down...` и процесс завершается без паники.

- [ ] **Step 5: Коммит**

```bash
git add cmd/server/main.go
git commit -m "refactor(server): reduce main to bootstrap and run"
```

---

### Task 12: Отдельный бинарь миграций

**Files:**
- Create: `cmd/migrate/main.go`
- Modify: `Makefile` (цель `migrate-embed`, строка 87)

**Interfaces:**
- Consumes: `config.Load`, `migrations.Run`
- Produces: бинарь `./cmd/migrate`

- [ ] **Step 1: Создать бинарь**

Create `cmd/migrate/main.go`:

```go
// Command migrate applies database migrations and exits.
package main

import (
	"database/sql"
	"log/slog"
	"os"

	_ "github.com/lib/pq"

	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/logger"
	"github.com/Linka-masterskaya/zip-backend/migrations"
)

const defaultConfigPath = "config/config.dev.yml"

func main() {
	if err := run(); err != nil {
		slog.Error("migrate failed", "err", err)
		os.Exit(1)
	}
	slog.Info("migrations completed")
}

func run() error {
	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = defaultConfigPath
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("config load: %w", err)
	}
	logger.Init(cfg.App.Env)

	conn, err := sql.Open("postgres", cfg.DB.URL)
	if err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			slog.Error("close db connection failed", logger.Err(err))
		}
	}()

	if err := migrations.Run(conn); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}
```

Добавить `"fmt"` в импорты.

Вся работа вынесена в `run() error` намеренно: `os.Exit` не выполняет отложенные вызовы, поэтому при `os.Exit(1)` прямо в `main` соединение с БД осталось бы незакрытым — та же ошибка, которую этот рефакторинг устраняет в сервере. `os.Exit` вызывается только после возврата из `run`, когда `defer` уже отработал.

- [ ] **Step 2: Поправить Makefile**

В цели `migrate-embed` (`Makefile:86-87`), было:
```make
	go run ./cmd/server --migrate
```
стало:
```make
	go run ./cmd/migrate
```

Цель `run-local` (`Makefile:20-23`) зовёт `migrate-embed` через `$(MAKE)` — правка одной строки покрывает и её.

Добавить сборку бинаря в цель `build` (`Makefile:11-14`):
```make
	go build -o bin/migrate ./cmd/migrate
```

- [ ] **Step 3: Проверить миграции**

Run:
```bash
docker compose -f compose.dev.yaml up -d postgres
make migrate-embed
```
Expected: лог `migrations completed`, код возврата 0. Повторный запуск — тоже успешен (миграции идемпотентны).

- [ ] **Step 4: Убедиться, что lib/pq ушёл из сервера**

Run: `go build ./... && go vet ./...`
Затем: `go list -deps ./cmd/server | grep -c 'lib/pq'`
Expected: `0` — второй драйвер PostgreSQL больше не попадает в бинарь сервера.

- [ ] **Step 5: Коммит**

```bash
git add cmd/migrate/main.go Makefile
git commit -m "refactor(migrate): move migrations into a dedicated binary"
```

---

### Task 13: Финальная проверка

**Files:**
- Modify: ничего (только при обнаружении дефектов)

**Interfaces:**
- Consumes: всё предыдущее
- Produces: ничего

- [ ] **Step 1: Статический анализ**

Run: `go build ./... && go vet ./... && golangci-lint run ./...`
Expected: чисто. Типичное замечание — неиспользованный импорт после переносов; убрать.

- [ ] **Step 2: Полный тестовый прогон**

Run: `go test ./... -race -count=1`
Expected: PASS.

- [ ] **Step 3: E2E**

Run:
```bash
docker compose -f compose.dev.yaml up -d postgres minio nats redis
make test-e2e
```
Expected: PASS без правок тестов.

- [ ] **Step 4: Проверить блокер с занятым портом**

```bash
CONFIG_PATH=config/config.dev.yml go run ./cmd/server &
sleep 3
CONFIG_PATH=config/config.dev.yml go run ./cmd/server; echo "exit=$?"
```
Expected: второй процесс логирует ошибку `bind: address already in use`, затем `shutting down...`, завершается с `exit=1`. В логах должны быть сообщения о закрытии ресурсов — до рефакторинга их не было, потому что `os.Exit(1)` обходил `defer`.

Не забыть остановить первый процесс: `kill %1`.

- [ ] **Step 5: Сверить health-эндпоинты с эталоном**

```bash
curl -s localhost:9090/health
curl -s localhost:9090/livez
curl -s localhost:9090/readyz
```
Expected: те же тела и статусы, что зафиксированы при ручной проверке в Task 11 Step 4.

- [ ] **Step 6: Проверить размер результата**

Run: `wc -l cmd/server/main.go internal/app/*.go internal/httpapi/*.go`
Expected: `cmd/server/main.go` ~30 строк; ни один файл в `internal/app` не превышает ~120 строк.

- [ ] **Step 7: Обновить спеку статусом**

In `docs/superpowers/specs/2026-07-31-bootstrap-refactor-design.md` поменять строку статуса на `Статус: реализовано` и добавить дату.

- [ ] **Step 8: Коммит**

```bash
git add docs/superpowers/specs/2026-07-31-bootstrap-refactor-design.md
git commit -m "docs: mark bootstrap refactor spec as implemented"
```

---

## Проверка плана против спеки

| Требование спеки | Задача |
|---|---|
| Closer с LIFO, без остановки на первой ошибке | Task 1 |
| Регистрация ресурса сразу после создания | Task 8 |
| `Bootstrap` закрывает созданное при ошибке | Task 8, Task 10 |
| errgroup, отказ сервера → отмена контекста | Task 10 |
| Единый `serveHTTP` вместо `errors.Is` / `!=` | Task 10 |
| Порядок остановки: серверы → closer | Task 10 |
| `httpapi/auth.go`, `httpapi/profile.go` | Task 5, Task 6 |
| `httpapi/ratelimits.go`, 10 лимитов | Task 4 |
| Экспорт `auth.Handler`, удаление `RegisterRoutes` | Task 3, Task 5 |
| `auth` не импортирует `config` и `cache` | Task 5 Step 5 |
| `cmd/migrate`, уход `flag.Parse` и `lib/pq` | Task 12 |
| `Makefile:87` | Task 12 |
| Порядок `config.Load` → `logger.Init` → инфра | Task 10 (Bootstrap) |
| `ServerConfig` с текущими значениями по умолчанию | Task 7 |
| `/health`, `/livez`, `/readyz` без изменений | Task 10, Task 13 Step 5 |
| Все критерии приёмки | Task 13 |
