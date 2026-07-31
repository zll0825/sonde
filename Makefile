.PHONY: help install-tools proto migrate-up migrate-down migrate-status dev lint test build clean

# ---- Variables ----
DB_URL ?= postgres://capital:capital_dev@localhost:5432/capital_observatory?sslmode=disable
MIGRATIONS_DIR ?= ./migrations

# ---- Help ----
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

# ---- Tooling ----
install-tools: ## Install buf, golang-migrate, and air
	@echo ">> Installing buf..."
	go install github.com/bufbuild/buf/cmd/buf@v1.34.0
	@echo ">> Installing golang-migrate..."
	go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
	@echo ">> Installing air (hot-reload)..."
	go install github.com/air-verse/air@latest
	@echo ">> Installing golangci-lint..."
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	@echo ">> Done. Ensure $(HOME)/go/bin is in your PATH."

# ---- Protobuf ----
proto: ## Generate Go code from proto files
	buf generate proto
	@echo ">> Generated pkg/proto/"

# ---- Database ----
migrate-up: ## Apply all pending migrations
	migrate -path $(MIGRATIONS_DIR) -database "$(DB_URL)" up

migrate-down: ## Rollback one migration
	migrate -path $(MIGRATIONS_DIR) -database "$(DB_URL)" down 1

migrate-status: ## Show migration status
	migrate -path $(MIGRATIONS_DIR) -database "$(DB_URL)" version

migrate-force: ## Force migration version (usage: make migrate-force VERSION=1)
	migrate -path $(MIGRATIONS_DIR) -database "$(DB_URL)" force $(VERSION)

# ---- Development ----
dev-db: ## Start local database via docker compose
	docker compose -f deployments/docker-compose.yml up -d

dev: ## Start core with hot-reload (M1+)
	air -c .air.toml

# ---- CI ----
lint: ## Run gofmt + go vet + golangci-lint + buf lint
	gofmt -l .
	go vet ./...
	buf lint proto

test: ## Run all go tests
	go test -v ./...

build: ## Build all binaries
	go build ./...
	go build -o bin/core ./cmd/core
	go build -o bin/api ./cmd/api

# ---- Cleanup ----
clean: ## Remove build artifacts
	rm -rf bin/
	rm -rf pkg/proto/plugin/
