.PHONY: build run test lint mock dev-up dev-down dev-reset migrate migrate-down

# ── Environment ──────────────────────────────────────────────────────────────
# Load variables from .env file (if exists) and export them for subprocesses
ifneq (,$(wildcard .env))
    include .env
    export
endif

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
	docker compose up -d

dev-down:
	docker compose down

dev-reset:
	docker compose down -v
	docker compose up -d

# ── Migrations (goose) ───────────────────────────────────────────────────────
migrate:
	goose -dir migrations postgres "$(DB_URL)" up

migrate-down:
	goose -dir migrations postgres "$(DB_URL)" down
