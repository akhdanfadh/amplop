# Development commands. Run from the repository root; `make` alone lists
# them. CONTRIBUTING.md, backend/README.md, and web/README.md explain
# what each command does and needs.
#
# Naming: when `make <verb>` exists, its parts are `<verb>-backend` and
# `<verb>-web`, e.g. `make check` runs `check-backend` and `check-web`.
# A command for one part only is `<part>-<verb>`, e.g. `db-migrate`.

# Settings for the Docker services, Go, and Goose; copied from .env.example
# on first use. Another file runs a second copy beside yours, e.g. on other
# ports:
# make dev ENV_FILE=/tmp/trial.env
ENV_FILE ?= .env

# Committed OpenAPI document: Go writes it, web/orval.config.ts reads it.
OPENAPI := api/generated/openapi.json

# Prefix for a command that needs ENV_FILE. Each recipe line runs in its
# own shell, so the prefix goes on the same line. The shell reads the file,
# as backend/README.md does; Make would cut a value at #, e.g. p#ss
# becomes p.
WITH_ENV = set -a; . "$(abspath $(ENV_FILE))"; set +a;

GOOSE := go -C backend tool -modfile=tools.mod goose

# Checks tests against testify's usage rules, e.g. require for errors.
TESTIFYLINT := go -C backend tool -modfile=tools.mod testifylint

.DEFAULT_GOAL := help

.PHONY: help install image dev dev-backend dev-worker dev-web backend-serve \
	backend-worker backend-build web-build web-preview web-add-ui \
	services-start services-stop services-wipe db-migrate db-status db-rollback \
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

# Container image, built from the Dockerfile on this machine; nothing is
# pushed. Another name keeps a second image beside this one, e.g.
# make image IMAGE=inbok:trial
IMAGE ?= inbok:local

image: ## Build the inbok container image, tagged IMAGE (inbok:local)
	docker build --tag $(IMAGE) .

# Development servers

# -j3 runs all three at once; Ctrl+C stops them all.
dev: ## Run serve and the worker (restarting on change) and Vite together
	@$(MAKE) -f $(firstword $(MAKEFILE_LIST)) --no-print-directory -j3 \
		dev-backend dev-worker dev-web

dev-backend: $(ENV_FILE) ## Run Go, restarting on change (Air)
	$(WITH_ENV) go -C backend tool -modfile=tools.mod air

# The worker answers health checks on its own address beside serve's. A
# .env from before the worker lacks INBOK_WORKER_HTTP_ADDR; the default
# covers it.
WORKER_ENV = INBOK_HTTP_ADDR="$${INBOK_WORKER_HTTP_ADDR:-127.0.0.1:8081}"

dev-worker: $(ENV_FILE) ## Run the worker, restarting on change (Air)
	$(WITH_ENV) $(WORKER_ENV) go -C backend tool -modfile=tools.mod air -c .air.worker.toml

dev-web: $(ENV_FILE) ## Run Vite on http://localhost:5173
	$(WITH_ENV) pnpm -C web dev

backend-serve: $(ENV_FILE) ## Run Go once, without restarts
	$(WITH_ENV) go -C backend run ./cmd/inbok serve

backend-worker: $(ENV_FILE) ## Run the worker once, without restarts
	$(WITH_ENV) $(WORKER_ENV) go -C backend run ./cmd/inbok worker

# The binary embeds what backend/internal/webapp/dist holds when it is
# built, so replace that copy first; a file left from an older build would
# be served too.
backend-build: web-build ## Build backend/bin/inbok with the web app built in
	find backend/internal/webapp/dist -mindepth 1 ! -path backend/internal/webapp/dist/.gitkeep -delete
	cp -R web/dist/client/. backend/internal/webapp/dist/
	go -C backend build -o bin/inbok ./cmd/inbok

web-build: ## Build the browser files into web/dist
	pnpm -C web build

web-preview: web-build ## Serve the built files on http://localhost:4173
	pnpm -C web preview

web-add-ui: ## Add a shadcn component, e.g. NAME=dialog
	$(if $(NAME),,$(error set NAME, e.g. make web-add-ui NAME=dialog))
	pnpm -C web exec shadcn add $(NAME)
	pnpm -C web fix

# Local services: the Docker Compose services in compose.yaml

# --build rebuilds the Google emulator's image when google-emulator/
# changes; otherwise Docker reuses its cached layers.
services-start: $(ENV_FILE) ## Start PostgreSQL, Mailpit, Meilisearch, SeaweedFS, and the Google emulator in Docker
	$(WITH_ENV) docker compose up -d --wait --build

services-stop: $(ENV_FILE) ## Stop the Docker services, keeping their data
	$(WITH_ENV) docker compose down

services-wipe: $(ENV_FILE) ## Stop the Docker services, deleting their data
	$(WITH_ENV) docker compose down -v

# Database

db-migrate: $(ENV_FILE) ## Apply pending migrations
	$(WITH_ENV) go -C backend run ./cmd/inbok migrate

db-status: $(ENV_FILE) ## List applied and pending migrations
	$(WITH_ENV) $(GOOSE) status

db-rollback: $(ENV_FILE) ## Undo the last applied migration
	$(WITH_ENV) $(GOOSE) down

db-reset: $(ENV_FILE) ## Undo inbok's migrations (not dbos), then apply all again
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

generate-web: ## Web client from the OpenAPI document, and cn's tables from the theme
	pnpm -C web generate

# Checks. Go tests need Docker running.

# Fails if regenerating code would change it, e.g. after editing a query
# without `make generate`; never edits files.
FRESH := scripts/check-fresh.sh

check: check-backend check-web ## Everything a commit must pass

# Fresh SQLC code first: stale code makes vet fail with confusing errors.
check-backend: ## Format, generated code, vet, lint, dependencies, and tests
	gofmt -d backend
	$(FRESH) backend/internal/repository/postgres/sqlc $(MAKE) generate-sqlc
	$(FRESH) $(OPENAPI) $(MAKE) generate-api
	go -C backend vet ./...
	$(TESTIFYLINT) ./...
	go -C backend mod tidy -diff
	go -C backend test ./...

# web-build also regenerates the route tree.
check-web: ## Generated code, build, format, lint, types, and tests
	$(FRESH) web/src/api/generated $(MAKE) generate-web
	$(FRESH) web/src/lib/generated/cn-tables.ts $(MAKE) generate-web
	$(FRESH) web/src/routeTree.gen.ts $(MAKE) web-build
	pnpm -C web check
	pnpm -C web test

test: test-backend test-web ## Run all tests

test-backend: ## Go tests
	go -C backend test ./...

test-web: ## Web unit and browser tests (browser tests need Playwright's Chromium)
	pnpm -C web test

fix: fix-backend fix-web ## Fix formatting and what lint can fix

fix-backend: ## Format Go files and fix test lint
	$(TESTIFYLINT) --fix ./...
	gofmt -w backend

fix-web: ## Format and fix lint in web
	pnpm -C web fix
