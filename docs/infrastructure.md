# Linka Editor — Инфраструктура и безопасность

---

## Топология VPS-узлов

```
Internet
    │
    ▼
edge-01  ──  Traefik (TLS-терминация, reverse proxy)
             Fail2ban (блокировка брутфорса)
    │
    ▼
app-01   ──  api-gateway
             pack-service
             media-service
             ai-service
             auth-service
    │
    ▼
data-01  ──  PostgreSQL 15 + pgvector
             Redis
             RabbitMQ
             MinIO
    │
    ▼
mon-01   ──  Prometheus 2.51
             Grafana 11
             Loki 3
```

Все узлы — отдельные VPS, взаимодействие только по внутренней сети (private network).

---

## Сервисы и роли

### edge-01
| Сервис | Роль |
|---|---|
| **Traefik** | Reverse proxy, TLS 1.3 (Let's Encrypt), rate-limit, маршрутизация |
| **Fail2ban** | Блокировка IP при брутфорсе SSH/HTTP |

### app-01
| Сервис | Роль |
|---|---|
| **API-Gateway** | Единая точка входа для клиентов, auth middleware, rate-limit |
| **Pack-Service** | CRUD наборов, черновики, версии, сборка ZIP |
| **Media-Service** | Загрузка файлов, дедупликация, presigned URL к MinIO |
| **AI-Service** | LLM (OpenAI o3), TTS (Yandex SpeechKit), очереди RabbitMQ |
| **Auth-Service** | OAuth2, JWT, RBAC |

### data-01
| Сервис | Роль |
|---|---|
| **PostgreSQL 15** | Основное хранилище метаданных, pgvector для AI-поиска |
| **Redis** | Кеш, WebSocket-сессии, хранение refresh-токенов |
| **RabbitMQ** | Очереди задач (TTS, антивирус ClamAV) |
| **MinIO** | Объектное хранилище медиафайлов (изображения, mp3) |

### mon-01
| Сервис | Роль |
|---|---|
| **Prometheus** | Сбор метрик со всех сервисов |
| **Grafana** | Дашборды, алерты (email/Telegram) |
| **Loki** | Агрегация логов |

---

## CI/CD (GitHub Actions + GHCR)

```
PR открыт
  → lint (golangci-lint / ruff / eslint)
  → unit tests
  → Trivy (scan образов на CVE)
  → code review checks

push main
  → build Docker images
  → push → GHCR

deploy (SSH на edge-01)
  → docker compose pull
  → docker compose up -d
  → health check
```

- Секреты (API ключи, DB pass) — GitHub Actions Secrets, не в репо.
- GHCR — приватный registry, доступ только по токену.

---

## Хранение данных

| Данные | Где | Бэкап |
|---|---|---|
| Метаданные наборов, пользователи | PostgreSQL (data-01) | `pg_dump` каждые 6 ч → S3/rclone |
| Медиафайлы (изображения, mp3) | MinIO (data-01) | `rclone sync` раз в сутки → внешнее хранилище |
| Кеш, сессии | Redis (data-01) | Без бэкапа (восстанавливаемое) |
| Логи | Loki (mon-01) | Retention 30 дней |
| Метрики | Prometheus (mon-01) | Retention 15 дней |

---

## Безопасность

### Сетевой уровень
- TLS 1.3 на всех публичных эндпоинтах (Traefik + Let's Encrypt)
- mTLS между edge-01 ↔ app-01 (взаимная аутентификация сервисов)
- app-01 и data-01 недоступны из интернета напрямую — только через private network
- Fail2ban на edge-01: блокировка после N неудачных попыток

### Аутентификация и авторизация
- JWT с TTL 15 минут + refresh-токен в Redis
- OAuth2 (Google SSO)
- RBAC: 4 роли (`defectologist`, `parent`, `viewer`, `admin`)
- Rate-limit на API-Gateway: защита от DDoS/brute-force

### Защита от OWASP Top 10
| Атака | Защита |
|---|---|
| **SQL Injection** | Параметризованные запросы (SQLAlchemy ORM), никаких raw SQL со входными данными |
| **XSS** | CSP заголовки в Traefik/Nginx, экранирование на фронте (Vue автоматически), `sanitize-html` при рендере пользовательского контента |
| **CSRF** | SameSite=Strict cookies + CSRF-токен для state-changing запросов |
| **SSRF** | Whitelist разрешённых URL при проксировании запросов к внешним ресурсам |
| **Path Traversal** | Валидация путей при работе с ZIP (.linka), запрет `../` в именах файлов |
| **Broken Access Control** | RBAC middleware на каждом сервисе, проверка ownership ресурса |
| **Insecure Upload** | ClamAV (через RabbitMQ), проверка MIME-типа, лимит 50 МиБ на архив |
| **Sensitive Data** | PII шифруется AES-256-GCM в БД |
| **Security Headers** | `X-Frame-Options`, `X-Content-Type-Options`, `Strict-Transport-Security`, `Content-Security-Policy` |

### Линтеры и статический анализ (в CI)

**Go:**
```yaml
- golangci-lint (errcheck, gosec, staticcheck, revive, govet)
- gosec — специально для security issues
```

**Python:**
```yaml
- ruff (fast linter + formatter)
- bandit (security linter)
- mypy (type checking)
```

**Frontend (TypeScript/Vue):**
```yaml
- eslint + @typescript-eslint
- eslint-plugin-vue
- stylelint (Tailwind CSS)
- prettier (formatting)
```

**Docker/IaC:**
```yaml
- Trivy (CVE scan образов и зависимостей)
- hadolint (Dockerfile lint)
```

Все линтеры запускаются в PR-чеках — мерж заблокирован при ошибках.

### Работа с секретами
- Секреты никогда не в репо (`.gitignore` для `.env`)
- Локально: `.env.local` (не коммитится)
- CI/CD: GitHub Actions Secrets
- Prod: переменные окружения через docker-compose secrets или Vault (опционально)
- Ротация JWT-ключей — без даунтайма (поддержка 2 активных ключей одновременно)

---

## Мониторинг и алерты

- Grafana алерты при: p95 > 200 мс, error rate > 1%, диск > 80%, сервис упал
- Уведомления: email + Telegram
- Uptime-check каждые 60 с

---

## Соответствие OWASP ASVS L2

Проверка при приёмке:
- [ ] Все API требуют аутентификации (кроме публичных)
- [ ] Логи аутентификации (success/fail) сохраняются
- [ ] Нет захардкоженных секретов в коде
- [ ] Зависимости проверены на CVE (Trivy)
- [ ] Ввод валидируется на сервере (не только на клиенте)
- [ ] PII шифруется в БД
- [ ] Бэкапы проверены восстановлением (100%)
