# Contributing

How to set up a development machine and change inbok. The Go server is
described in `backend/README.md`, the browser app in `web/README.md`.

## Make

The `Makefile` runs every command in these files from the repository
root. Run `make` to list them, e.g. `make services-start`,
`make db-migrate`, then `make dev` for the Go server, its background
worker, and the web app together. `make check` runs what a commit must
pass; `make check-backend` and `make check-web` run one half.
It also fails when generated code, e.g. `api/generated/openapi.json`, is
out of date with its source, and names the command that updates it.
Commands that need `.env` copy it from `.env.example` when it is missing.

## Local services

PostgreSQL, Mailpit, and a Google emulator run in Docker Compose
(`compose.yaml`). From the repository root, create your local settings
once, then start them in the background:

```sh
cp .env.example .env
docker compose up -d --wait --build
```

`--wait` returns once PostgreSQL accepts connections on `127.0.0.1:5432`
(`INBOK_POSTGRES_PORT` in `.env`) and the others are ready. `--build`
builds the emulator's image the first time, which downloads emulate from
npm, and again after `google-emulator/` changes. PostgreSQL's data lives
in a Docker volume and survives `docker compose down`. To delete it and
start empty, e.g. after changing the user or password in `.env`, run
`docker compose down -v`.

Mailpit catches mail sent to `127.0.0.1:1025` (`INBOK_MAILPIT_SMTP_PORT`)
and shows it at `http://localhost:8025` (`INBOK_MAILPIT_WEB_PORT`). It deletes
its messages when it stops. To send inbok's test message there, see
[System email](backend/README.md#system-email).

The Google emulator stands in for Google's sign-in and Gmail at
`http://127.0.0.1:4000` (`INBOK_GOOGLE_EMULATOR_PORT`), so connecting a
Gmail account works without a Google project. Its seeded mailboxes and
what it lacks are in [Google emulator](backend/README.md#google-emulator).
A `.env` made before it existed lacks the `INBOK_GOOGLE_*` lines; copy
them from `.env.example`.

## Changing the API

The Go code defines the API. A change to an operation, e.g. a renamed
field, reaches the browser app through generated code: Go writes
`api/generated/openapi.json`, and the web client is generated from it.
Regenerate both and commit them with the Go change:

```sh
make generate   # SQLC code, then the OpenAPI document, then the web client
make check      # web code that still uses the old field fails here
```

Never edit generated code by hand; the next `make generate` overwrites it.
