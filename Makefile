.PHONY: help install-tools proto migrate-up migrate-down migrate-status migrate-force dev dev-up dev-down dev-logs lint test build clean

# ---- Variables ----
DB_URL ?= postgres://capital:capital_dev@localhost:5432/capital_observatory?sslmode=disable
MIGRATIONS_DIR ?= ./migrations
COMPOSE_FILE ?= ./deployments/docker-compose.yml
API_PORT ?= 8080
# Workspace modules with Go code. NOTE: `go build ./...` from the repo root
# only matches the ROOT module — plugin modules are invisible to it. Add new
# plugin modules here or they silently escape every gate.
GO_MODULES ?= . ./plugins/etf

# ---- Help ----
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-22s\033[0m %s\n", $$1, $$2}'

# ---- Tooling ----
install-tools: ## Install buf, golang-migrate, air, golangci-lint
	@echo ">> Installing buf..."
	go install github.com/bufbuild/buf/cmd/buf@v1.34.0
	@echo ">> Installing golang-migrate..."
	go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
	@echo ">> Installing air (hot-reload)..."
	go install github.com/air-verse/air@latest
	@echo ">> Installing golangci-lint..."
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	@echo ">> Done. Ensure $$HOME/go/bin is in your PATH."

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

# ---- One-command full stack ----
dev-up: ## Build and run the full stack (DB + Core + API + ETF plugin)
	@echo ">> Bringing up Capital Observatory full stack..."
	docker compose -f $(COMPOSE_FILE) up -d --build
	@echo ">> Waiting for database to be healthy..."
	@sleep 2
	$(MAKE) migrate-up
	@echo ""
	@echo "╔══════════════════════════════════════════════════════════════╗"
	@echo "║  Capital Observatory is up!                                  ║"
	@echo "║                                                              ║"
	@echo "║  Database: postgres://capital:capital_dev@localhost:5432     ║"
	@echo "║  Core gRPC: :50051                                           ║"
	@echo "║  API + UI:  http://localhost:$(API_PORT)/                    ║"
	@echo "╠══════════════════════════════════════════════════════════════╣"
	@echo "║  Useful commands:                                            ║"
	@echo "║    make dev-logs         tail service logs                   ║"
	@echo "║    make dev-down         stop everything                     ║"
	@echo "║    psql $(DB_URL) -c \"SELECT * FROM alerts;\"                 ║"
	@echo "╚══════════════════════════════════════════════════════════════╝"

dev-down: ## Stop full stack
	docker compose -f $(COMPOSE_FILE) down

dev-logs: ## Tail all service logs
	docker compose -f $(COMPOSE_FILE) logs -f --tail=100

dev-restart: dev-down dev-up ## Restart full stack

# ---- Local hot-reload development ----
dev: ## Start core with hot-reload via Air (requires running DB)
	air -c .air.toml

# ---- Direct binary run (without Docker) ----
run-core: build-core ## Run the core binary directly
	CORE_BIND=:50051 DB_URL="$(DB_URL)" ./bin/core

run-api: build-api ## Run the API binary directly
	API_BIND=:8080 DB_URL="$(DB_URL)" WEB_DIR=./web ./bin/api

run-etf: ## Run the ETF plugin directly (connects to core at localhost:50051)
	cd plugins/etf && CORE_ADDR=localhost:50051 PLUGIN_ID=etf go run ./cmd/etf

# ---- CI ----
lint: ## Run gofmt + go vet + buf lint (all workspace modules)
	gofmt -l .
	@for m in $(GO_MODULES); do (cd $$m && go vet ./...) || exit 1; done
	buf lint proto

test: ## Run all go tests (all workspace modules)
	@for m in $(GO_MODULES); do (cd $$m && go test -v ./...) || exit 1; done

test-short: ## Run tests without the integration suite (no DB required)
	@for m in $(GO_MODULES); do (cd $$m && go test -short -v ./...) || exit 1; done

build: build-core build-api ## Build all binaries to ./bin/ and verify all modules compile
	@for m in $(GO_MODULES); do (cd $$m && go build ./...) || exit 1; done

build-core:
	go build -o bin/core ./cmd/core

build-api:
	go build -o bin/api ./cmd/api

# ---- Cleanup ----
clean: ## Remove build artifacts + stop docker stack (volumes preserved)
	rm -rf bin/ tmp/
	docker compose -f $(COMPOSE_FILE) down 2>/dev/null || true

clean-data: ## DESTRUCTIVE: stop stack AND delete database volumes
	docker compose -f $(COMPOSE_FILE) down -v
