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

.PHONY: seed-tower
seed-tower: ## Load the TOWER example metadata (seed/tower_example.sql) via the gitbash sidecar -- transmission support structure, leaner sibling of POLE; compile CODE=TOWER after
	$(COMPOSE) exec -T gitbash psql "$(CONTAINER_DATABASE_URL)" -f /workspace/seed/tower_example.sql

.PHONY: seed-tower-relationship
seed-tower-relationship: ## Load the TOWER supports MV_CONDUCTOR relationship class (seed/tower_relationship_example.sql) -- enables double-circuit towers (Phase 35); requires seed-tower + seed-connectivity first, no compile
	$(COMPOSE) exec -T gitbash psql "$(CONTAINER_DATABASE_URL)" -f /workspace/seed/tower_relationship_example.sql

.PHONY: seed-connectivity
seed-connectivity: ## Load the MV_CONDUCTOR/DIST_XFMR connectivity example metadata (seed/connectivity_example.sql) via the gitbash sidecar
	$(COMPOSE) exec -T gitbash psql "$(CONTAINER_DATABASE_URL)" -f /workspace/seed/connectivity_example.sql

.PHONY: seed-service-point
seed-service-point: ## Load the SERVICE_POINT class + DIST_XFMR T2 -> MV_CONDUCTOR rule (seed/service_point_example.sql) via the gitbash sidecar -- requires seed-connectivity first; compile CODE=SERVICE_POINT after
	$(COMPOSE) exec -T gitbash psql "$(CONTAINER_DATABASE_URL)" -f /workspace/seed/service_point_example.sql

.PHONY: seed-relationships
seed-relationships: ## Load the POLE/MV_CONDUCTOR relationship_classes example metadata (seed/relationship_example.sql) via the gitbash sidecar -- requires seed-pole and seed-connectivity first
	$(COMPOSE) exec -T gitbash psql "$(CONTAINER_DATABASE_URL)" -f /workspace/seed/relationship_example.sql

.PHONY: seed-mv-metering
seed-mv-metering: ## Load the MV metering point classes MV_METERING_POINT + MV_METER (Phase 43 Slice 1) via the gitbash sidecar -- requires migration 0080 + seed/mv_measuring_example.sql (MV_CT/MV_VT) + seed/connectivity_example.sql; compile CODE=MV_METERING_POINT and CODE=MV_METER after
	$(COMPOSE) exec -T gitbash psql "$(CONTAINER_DATABASE_URL)" -f /workspace/seed/mv_metering_example.sql

.PHONY: seed-mv-metering-cubicle
seed-mv-metering-cubicle: ## Load the MV_METERING_CUBICLE container class (Phase 43 Slice 2; contains MV_METER + MV_CT + MV_VT, owns a METERING_SCHEMATIC diagram) via the gitbash sidecar -- requires seed-mv-metering + seed-mv-measuring first; compile CODE=MV_METERING_CUBICLE after
	$(COMPOSE) exec -T gitbash psql "$(CONTAINER_DATABASE_URL)" -f /workspace/seed/mv_metering_cubicle_example.sql

.PHONY: seed-mv-metering-symbology
seed-mv-metering-symbology: ## Load map + diagram symbology for the Phase 43 metering classes MV_METERING_POINT/MV_METER/MV_METERING_CUBICLE (Slice 3) via the gitbash sidecar -- requires seed-mv-metering + seed-mv-metering-cubicle first; MV_CT/MV_VT already symbolized by seed-mv-symbology; no compile (symbology is client-side)
	$(COMPOSE) exec -T gitbash psql "$(CONTAINER_DATABASE_URL)" -f /workspace/seed/mv_metering_symbology.sql

.PHONY: seed-test-network
seed-test-network: ## Load the small radial test feeder -- the FIRST seed of real gis.* feature INSTANCES + runtime.feature_connectivity (SUB-SOURCE/SW-101/TX-101 -> LV-1 -> MTR-A/B -> SP-A/B + 3 poles) via the gitbash sidecar -- requires seed-connectivity/seed-load-flow-fields/seed-transformer-fields/seed-zero-seq-fields/seed-mv-conductor-specs/seed-mv-lv-switch/seed-lv-conductors/seed-lv-metering/seed-service-point/seed-pole applied AND the 7 classes compiled first; fails loudly if a prerequisite is missing
	$(COMPOSE) exec -T gitbash psql "$(CONTAINER_DATABASE_URL)" -f /workspace/seed/test_network_example.sql

.PHONY: seed-substation-network
seed-substation-network: ## Load the larger 33/11/0.4 kV SUBSTATION feeder -- a second seed of real gis.* feature INSTANCES + runtime.feature_connectivity built around a POWER_XFMR + MV_BUSBAR (GRID-SRC -> PTX-1 -> BUS-A -> two feeders w/ recloser/switches/3 DIST_XFMRs -> 4 LV runs + LV switch + 6 meters/service points + 4 poles) via the gitbash sidecar -- requires ALL seed-test-network prerequisites PLUS seed-power-transformer/seed-mv-busbar/seed-mv-recloser/seed-mv-lv-switch applied AND the 11 classes compiled first (DIST_XFMR/POWER_XFMR/MV_BUSBAR/MV_RECLOSER/MV_SWITCH/LV_SWITCH/MV_CONDUCTOR/LV_CONDUCTOR/METER/SERVICE_POINT/POLE); fails loudly if a prerequisite is missing; coexists with seed-test-network
	$(COMPOSE) exec -T gitbash psql "$(CONTAINER_DATABASE_URL)" -f /workspace/seed/substation_network_example.sql

.PHONY: seed-protection-network
seed-protection-network: ## Load the PROTECTION-COORDINATION feeder -- a third seed of real gis.* feature INSTANCES + runtime.feature_connectivity + runtime.feature_relationships built to exercise POST /analysis/coordination (SUB-SOURCE -> BRK-1[graded by RLY-1 relay via a protection_trip relationship] -> FUSE-1 -> TX-1 -> LV-1 -> MTR-1 -> SP-1, with MV_CT/MV_VT sensing the relay) via the gitbash sidecar -- requires migrations 0075+0076 (task migrate) PLUS all seed-test-network prerequisites PLUS seed-mv-switching/seed-mv-protection/seed-mv-measuring/seed-mv-relay/seed-protection-curves applied AND the classes compiled first (DIST_XFMR/MV_CONDUCTOR/LV_CONDUCTOR/METER/SERVICE_POINT/MV_BREAKER/MV_FUSE/MV_CT/MV_VT/PROTECTIVE_RELAY); fails loudly if a prerequisite is missing; coexists with the other network seeds
	$(COMPOSE) exec -T gitbash psql "$(CONTAINER_DATABASE_URL)" -f /workspace/seed/protection_network_example.sql

.PHONY: seed-functional-roles
seed-functional-roles: ## Stamp metadata.feature_classes.functional_role (migration 0091) onto today's electrical classes -- the class->role assignments for the electrical-function taxonomy (consumer/passthrough/transformer/controller/node/sensor/ground; no class maps to 'source', the network source is a slack DIST_XFMR instance). Read by the supply-path trace to identify serving transformers/switching devices/source by role instead of hardcoded class lists -- via the gitbash sidecar; requires `make migrate` for 0091 first; no gis.* table, no recompile; idempotent and tolerant of absent classes
	$(COMPOSE) exec -T gitbash psql "$(CONTAINER_DATABASE_URL)" -f /workspace/seed/functional_role_backfill.sql

.PHONY: seed-coupler-network
seed-coupler-network: ## Load the BUS-COUPLER tie network -- a minimal seed of real gis.* feature INSTANCES + runtime.feature_connectivity that isolates the coupler (BUS-A =[CPLR-1 MV_BREAKER, switch_function=BUS_COUPLER, device_state=CLOSED]= BUS-B -> LOAD-FEED) so a downstream trace from BUS-A reaches BUS-B/LOAD-FEED ONLY across the closed tie (set CPLR-1 device_state=OPEN and re-trace to watch them drop out) via the gitbash sidecar -- requires seed-connectivity/seed-mv-switching/seed-mv-busbar/seed-bus-coupler-fields applied AND the classes compiled first (MV_CONDUCTOR/MV_BREAKER[after bus_coupler_fields]/MV_BUSBAR); fails loudly if a prerequisite is missing; coexists with the other network seeds; hand-built target state the POST /couplers endpoint produces
	$(COMPOSE) exec -T gitbash psql "$(CONTAINER_DATABASE_URL)" -f /workspace/seed/coupler_network_example.sql

.PHONY: seed-bus-coupler-fields
seed-bus-coupler-fields: ## Add the bus-coupler role fields to MV_BREAKER (switch_function FEEDER/INCOMER/BUS_COUPLER/BUS_SECTION/TRANSFORMER + normal_state NORMALLY_CLOSED/NORMALLY_OPEN; Bus coupler Slice 1) via the gitbash sidecar -- a coupler is a breaker wired bus<->bus, these fields just label it; nullable field-pack, no migration; requires seed-mv-switching first; compile CODE=MV_BREAKER after
	$(COMPOSE) exec -T gitbash psql "$(CONTAINER_DATABASE_URL)" -f /workspace/seed/bus_coupler_fields.sql

.PHONY: seed-cable-management
seed-cable-management: ## Load the Phase 44 Slice 1 CIVIL cable-management inventory classes CABLE_TRENCH/CABLE_DUCT/CABLE_CHAMBER (network_role='none', never traced -- ducts/trenches/chambers with way_count capacity field) via the gitbash sidecar -- no migration needed; compile CODE=CABLE_TRENCH+CABLE_DUCT+CABLE_CHAMBER after; symbology via seed-cable-management-symbology
	$(COMPOSE) exec -T gitbash psql "$(CONTAINER_DATABASE_URL)" -f /workspace/seed/cable_management_example.sql

.PHONY: seed-cable-management-symbology
seed-cable-management-symbology: ## Load the mandatory 'map' default symbology for the Phase 44 Slice 1 classes CABLE_TRENCH/CABLE_DUCT/CABLE_CHAMBER (publish gate; civil amber/slate/stone tones, no diagram rule) via the gitbash sidecar -- requires seed-cable-management first; no compile (symbology is client-side)
	$(COMPOSE) exec -T gitbash psql "$(CONTAINER_DATABASE_URL)" -f /workspace/seed/cable_management_symbology.sql

.PHONY: seed-cable-joint
seed-cable-joint: ## Load the Phase 44 Slice 2 ELECTRICAL cable-joint junction classes MV_CABLE_JOINT/LV_CABLE_JOINT (point, network_role='junction', zero-Z pass-through splice; subtypes straight/branch[+T3 tee]/transition/termination; feed-through network_rules to MV/LV_CONDUCTOR; CABLE_CHAMBER contains joints via existing containment kind) via the gitbash sidecar -- requires seed-connectivity + seed-lv-conductors + seed-cable-management (CABLE_CHAMBER) first; no migration; compile CODE=MV_CABLE_JOINT+LV_CABLE_JOINT after; symbology via seed-cable-joint-symbology
	$(COMPOSE) exec -T gitbash psql "$(CONTAINER_DATABASE_URL)" -f /workspace/seed/cable_joint_example.sql

.PHONY: seed-cable-joint-symbology
seed-cable-joint-symbology: ## Load the mandatory 'map' default symbology for the Phase 44 Slice 2 joint classes MV_CABLE_JOINT/LV_CABLE_JOINT (publish gate; deep-teal/slate splice dots, no diagram rule) via the gitbash sidecar -- requires seed-cable-joint first; no compile (symbology is client-side)
	$(COMPOSE) exec -T gitbash psql "$(CONTAINER_DATABASE_URL)" -f /workspace/seed/cable_joint_symbology.sql

.PHONY: seed-cable-routing
seed-cable-routing: ## Load the Phase 44 Slice 3 cable_routing relationship classes coupling the civil axis to the cables (CABLE_DUCT carries MV/LV_CONDUCTOR [way-consuming], CABLE_TRENCH carries CABLE_DUCT + MV/LV_CONDUCTOR direct-buried; all many_to_many) via the gitbash sidecar -- requires migration 0082 (task migrate) + seed-cable-management + seed-connectivity + seed-lv-conductors first; no compile (relationship_classes has no physical footprint)
	$(COMPOSE) exec -T gitbash psql "$(CONTAINER_DATABASE_URL)" -f /workspace/seed/cable_routing_example.sql

.PHONY: seed-duct-way
seed-duct-way: ## Load the Phase 44 Slice 4 DUCT_WAY per-way class (geometry_type='none' logical child) + relationship classes (CABLE_DUCT contains DUCT_WAY [containment]; DUCT_WAY carries MV/LV_CONDUCTOR [cable_routing, per-way]) via the gitbash sidecar -- requires seed-cable-management + migration 0082 + seed-connectivity + seed-lv-conductors first; compile CODE=DUCT_WAY after
	$(COMPOSE) exec -T gitbash psql "$(CONTAINER_DATABASE_URL)" -f /workspace/seed/duct_way_example.sql

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
