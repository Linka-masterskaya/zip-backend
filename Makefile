.PHONY: build run test lint mock dev-up dev-down dev-reset migrate migrate-down

# ── Build ────────────────────────────────────────────────────────────────────
build:
	go build -o bin/server ./cmd/server
	go build -o bin/ai-worker ./cmd/ai-worker

# ── Run ──────────────────────────────────────────────────────────────────────
run:
	CONFIG_PATH=config/config.dev.yml go run ./cmd/server

# ── Test ─────────────────────────────────────────────────────────────────────
test:
	go test ./... -race -count=1

test-cover:
	go test ./... -race -count=1 -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html

# ── Lint ─────────────────────────────────────────────────────────────────────
lint:
	golangci-lint run ./...

# ── Mocks (uber/gomock) ──────────────────────────────────────────────────────
mock:
	go generate ./...

# ── Dev infra ────────────────────────────────────────────────────────────────
dev-up:
	docker compose -f docker-compose.yml -f compose.dev.yaml up -d

dev-down:
	docker compose -f compose.dev.yaml down

dev-reset:
	docker compose -f compose.dev.yaml down -v
	docker compose -f compose.dev.yaml up -d

# ── Migrations (goose) ───────────────────────────────────────────────────────
migrate:
	goose -dir migrations postgres "$(DB_URL)" up

migrate-down:
	goose -dir migrations postgres "$(DB_URL)" down
