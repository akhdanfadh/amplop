# Development commands. Run from the repository root; `make` alone lists
# them. README.md explains what each command does and needs.
#
# Naming: when `make <verb>` exists, its parts are `<verb>-backend` and
# `<verb>-web`, e.g. `make check` runs `check-backend` and `check-web`.
# A command for one part only is `<part>-<verb>`, e.g. `db-start`.

# Settings for PostgreSQL, Go, and Goose; copied from .env.example on first
# use. Another file runs a second copy beside yours, e.g. on other ports:
# make dev ENV_FILE=/tmp/trial.env
ENV_FILE ?= .env

# Committed OpenAPI document: Go writes it, web/orval.config.ts reads it.
OPENAPI := api/generated/openapi.json

# Prefix for a command that needs ENV_FILE. Each recipe line runs in its
# own shell, so the prefix goes on the same line. The shell reads the file,
# as README.md does; Make would cut a value at #, e.g. p#ss becomes p.
WITH_ENV = set -a; . "$(abspath $(ENV_FILE))"; set +a;

GOOSE := go -C backend tool -modfile=tools.mod goose

.DEFAULT_GOAL := help

.PHONY: help install dev dev-backend dev-web backend-serve \
	web-build web-preview web-add-ui \
	db-start db-stop db-wipe db-migrate db-status db-rollback \
	db-reset db-migration \
	generate generate-sqlc generate-api generate-web \
	check check-backend check-web test test-backend test-web \
	fix fix-backend fix-web

# Lists every target whose line ends in `## description`.
help: ## List commands
	@awk -F ':.*## ' '/^[a-z-]+:.*## / { printf "  %-16s %s\n", $$1, $$2 }' \
		$(MAKEFILE_LIST)

$(ENV_FILE):
	cp .env.example $@

install: ## Install web dependencies (Go downloads its own)
	pnpm -C web install

# Development servers

# -j2 runs both at once; Ctrl+C stops both.
dev: ## Run Go (restarts on change) and Vite together
	@$(MAKE) -f $(firstword $(MAKEFILE_LIST)) --no-print-directory -j2 \
		dev-backend dev-web

dev-backend: $(ENV_FILE) ## Run Go, restarting on change (Air)
	$(WITH_ENV) go -C backend tool -modfile=tools.mod air

dev-web: $(ENV_FILE) ## Run Vite on http://localhost:5173
	$(WITH_ENV) pnpm -C web dev

backend-serve: $(ENV_FILE) ## Run Go once, without restarts
	$(WITH_ENV) go -C backend run ./cmd/inbok serve

web-build: ## Build the browser files into web/dist
	pnpm -C web build

web-preview: web-build ## Serve the built files on http://localhost:4173
	pnpm -C web preview

web-add-ui: ## Add a shadcn component, e.g. NAME=dialog
	$(if $(NAME),,$(error set NAME, e.g. make web-add-ui NAME=dialog))
	pnpm -C web exec shadcn add $(NAME)
	pnpm -C web fix

# Database

db-start: $(ENV_FILE) ## Start PostgreSQL in Docker
	$(WITH_ENV) docker compose up -d --wait

db-stop: $(ENV_FILE) ## Stop PostgreSQL, keeping its data
	$(WITH_ENV) docker compose down

db-wipe: $(ENV_FILE) ## Stop PostgreSQL and delete its data
	$(WITH_ENV) docker compose down -v

db-migrate: $(ENV_FILE) ## Apply pending migrations
	$(WITH_ENV) go -C backend run ./cmd/inbok migrate

db-status: $(ENV_FILE) ## List applied and pending migrations
	$(WITH_ENV) $(GOOSE) status

db-rollback: $(ENV_FILE) ## Undo the last applied migration
	$(WITH_ENV) $(GOOSE) down

db-reset: $(ENV_FILE) ## Undo every migration, then apply all again
	$(WITH_ENV) $(GOOSE) reset
	$(WITH_ENV) go -C backend run ./cmd/inbok migrate

db-migration: $(ENV_FILE) ## Add the next migration file, e.g. NAME=add_x
	$(if $(NAME),,$(error set NAME, e.g. make db-migration NAME=add_x))
	$(WITH_ENV) $(GOOSE) create -s $(NAME) sql

# Generated code, in dependency order: SQL queries -> Go methods, Go
# operations -> OpenAPI document -> web client.

generate: generate-sqlc generate-api generate-web ## Regenerate all generated code

generate-sqlc: ## Go methods from SQL queries
	go -C backend tool -modfile=tools.mod sqlc generate

generate-api: ## OpenAPI document from Go
	go -C backend run ./cmd/inbok export ../$(OPENAPI)

generate-web: ## Web client from the OpenAPI document
	pnpm -C web generate

# Checks. Go tests need Docker running.

# Fails if regenerating code would change it, e.g. after editing a query
# without `make generate`; never edits files.
FRESH := scripts/check-fresh.sh

check: check-backend check-web ## Everything a commit must pass

# Fresh SQLC code first: stale code makes vet fail with confusing errors.
check-backend: ## Format, generated code, vet, dependencies, and tests
	gofmt -d backend
	$(FRESH) backend/internal/repository/postgres/sqlc $(MAKE) generate-sqlc
	$(FRESH) $(OPENAPI) $(MAKE) generate-api
	go -C backend vet ./...
	go -C backend mod tidy -diff
	go -C backend test ./...

# web-build also regenerates the route tree.
check-web: ## Generated code, build, format, lint, types, and tests
	$(FRESH) web/src/api/generated $(MAKE) generate-web
	$(FRESH) web/src/routeTree.gen.ts $(MAKE) web-build
	pnpm -C web check
	pnpm -C web test

test: test-backend test-web ## Run all tests

test-backend: ## Go tests
	go -C backend test ./...

test-web: ## Web unit tests
	pnpm -C web test

fix: fix-backend fix-web ## Fix formatting and what lint can fix

fix-backend: ## Format Go files
	gofmt -w backend

fix-web: ## Format and fix lint in web
	pnpm -C web fix
