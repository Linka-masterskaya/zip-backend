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

migration-generate:
	@if [ -z "$(NAME)" ]; then \
		echo "Error: specify the migration name"; \
		echo "Ex: make migration-generate NAME=create_users_table"; \
		exit 1; \
	fi
	@mkdir -p migrations
	@TIMESTAMP=$$(date +%Y%m%d%H%M%S); \
	FILENAME="migrations/$${TIMESTAMP}_$(NAME).sql"; \
	echo "-- +goose Up" > $$FILENAME; \
	echo "" >> $$FILENAME; \
	echo "-- +goose Down" >> $$FILENAME; \
	echo "Migration created: $$FILENAME"
	@echo "Now add the SQL queries to the file"

migration-help:
	@echo "Migration management (goose):"
	@echo "*  make migration-generate NAME=<name>   Create a new migration"
	@echo "*  make migrate                          Apply all new migrations"
	@echo "*  make migrate-down                     Roll back the last migration"

migrate-embed:
	go run ./cmd/server --migrate
