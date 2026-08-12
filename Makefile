# Makefile -- local dev commands, laptop-only.
#
# Needs on the host: make, docker, docker compose, go (for the api-*
# targets). Nothing here needs bash or psql on the host -- migrate,
# migrate-status, and psql all shell out through the `gitbash` sidecar
# container instead, since avoiding that exact dependency is the whole
# reason that sidecar exists.

SHELL := /bin/sh
COMPOSE := docker compose
API_DIR := api

# Pull in .env if it exists (created via `cp .env.example .env`) so the
# DATABASE_URLs below match whatever docker-compose.yml is actually using,
# including any password/port you've changed locally.
ifneq (,$(wildcard .env))
	include .env
	export
endif

POSTGRES_PASSWORD ?= voltedge_dev
POSTGRES_DB ?= postgres
POSTGRES_PORT ?= 3700
API_PORT ?= 3704

# api-dev runs on the host and talks to postgres over its published port.
# sslmode=disable: this postgres image has no TLS configured, and the
# client defaults to attempting it anyway -- local-dev-only, never carry
# this into a DSN that points at a deployment with real TLS.
LOCAL_DATABASE_URL := postgres://postgres:$(POSTGRES_PASSWORD)@localhost:$(POSTGRES_PORT)/$(POSTGRES_DB)?sslmode=disable
# migrate/psql run inside the compose network, where postgres is reachable
# by service name instead.
CONTAINER_DATABASE_URL := postgres://postgres:$(POSTGRES_PASSWORD)@postgres:5432/$(POSTGRES_DB)?sslmode=disable

.DEFAULT_GOAL := help

## ---------------------------------------------------------------- infra --

.PHONY: infra-up
infra-up: ## Start postgres, martin, rustfs, gitbash (not the api container)
	$(COMPOSE) up -d postgres martin rustfs gitbash

.PHONY: infra-down
infra-down: ## Stop all containers, keep volumes (data survives)
	$(COMPOSE) down

.PHONY: infra-clean
infra-clean: ## Stop all containers AND delete volumes -- wipes the local DB. Destructive.
	@echo "This deletes the postgres_data and rustfs_data volumes. Ctrl-C to abort, Enter to continue."
	@read _ignored
	$(COMPOSE) down -v

.PHONY: infra-ps
infra-ps: ## Show status of the compose stack
	$(COMPOSE) ps

.PHONY: infra-logs
infra-logs: ## Tail logs for every service
	$(COMPOSE) logs -f

.PHONY: up
up: ## Start everything, including the api container, building images as needed
	$(COMPOSE) up -d --build

.PHONY: down
down: infra-down ## Alias for infra-down

## ------------------------------------------------------------------ api --

.PHONY: api-dev
api-dev: ## Run the api locally against the dockerized postgres (needs infra-up first)
	cd $(API_DIR) && DATABASE_URL="$(LOCAL_DATABASE_URL)" PORT=$(API_PORT) go run ./cmd/server

.PHONY: api-build
api-build: ## Build the api binary to api/bin/server
	cd $(API_DIR) && go build -o bin/server ./cmd/server

.PHONY: api-test
api-test: ## Run api tests
	cd $(API_DIR) && go test ./...

.PHONY: tidy
tidy: ## go mod tidy the api module
	cd $(API_DIR) && go mod tidy

.PHONY: vet
vet: ## go vet the api module
	cd $(API_DIR) && go vet ./...

.PHONY: fmt
fmt: ## gofmt the api module
	cd $(API_DIR) && gofmt -l -w .

.PHONY: compile
compile: ## Compile one feature class's metadata into physical schema, e.g. make compile CODE=POLE
	@if [ -z "$(CODE)" ]; then echo "usage: make compile CODE=<FEATURE_CLASS_CODE>"; exit 1; fi
	cd $(API_DIR) && DATABASE_URL="$(LOCAL_DATABASE_URL)" go run ./cmd/compile $(CODE)

.PHONY: seed-pole
seed-pole: ## Load the POLE example metadata (seed/pole_example.sql) via the gitbash sidecar
	$(COMPOSE) exec -T gitbash psql "$(CONTAINER_DATABASE_URL)" -f /workspace/seed/pole_example.sql

.PHONY: seed-connectivity
seed-connectivity: ## Load the MV_CONDUCTOR/DIST_XFMR connectivity example metadata (seed/connectivity_example.sql) via the gitbash sidecar
	$(COMPOSE) exec -T gitbash psql "$(CONTAINER_DATABASE_URL)" -f /workspace/seed/connectivity_example.sql

.PHONY: seed-relationships
seed-relationships: ## Load the POLE/MV_CONDUCTOR relationship_classes example metadata (seed/relationship_example.sql) via the gitbash sidecar -- requires seed-pole and seed-connectivity first
	$(COMPOSE) exec -T gitbash psql "$(CONTAINER_DATABASE_URL)" -f /workspace/seed/relationship_example.sql

## ------------------------------------------------------------- frontend --

.PHONY: frontend-dev
frontend-dev: ## Run the React + Vite frontend dev server
	@if [ -f web/package.json ]; then \
		cd web && npm run dev; \
	else \
		echo "web/ isn't scaffolded yet -- the frontend build starts Phase 3."; \
	fi

## -------------------------------------------------------------- database --

.PHONY: migrate
migrate: ## Apply pending SQL migrations via the gitbash sidecar (needs infra-up first)
	$(COMPOSE) exec -T -e DATABASE_URL="$(CONTAINER_DATABASE_URL)" gitbash bash /workspace/migrations/apply.sh

.PHONY: migrate-status
migrate-status: ## Show what's recorded in ops.migration_log
	$(COMPOSE) exec -T gitbash psql "$(CONTAINER_DATABASE_URL)" -c "SELECT filename, status, applied_at FROM ops.migration_log ORDER BY id;"

.PHONY: psql
psql: ## Open an interactive psql shell against the dockerized postgres
	$(COMPOSE) exec gitbash psql "$(CONTAINER_DATABASE_URL)"

## ------------------------------------------------------------------ misc --

.PHONY: clean
clean: ## Remove local build artifacts (does not touch docker volumes -- see infra-clean)
	rm -rf $(API_DIR)/bin

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'
