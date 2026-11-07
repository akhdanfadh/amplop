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

PostgreSQL, Mailpit, Meilisearch, SeaweedFS, and a Google emulator run in
Docker Compose (`compose.yaml`). From the repository root, create your
local settings once, then start them in the background:

```sh
cp .env.example .env
docker compose up -d --wait --build
```

`--wait` returns once PostgreSQL accepts connections on `127.0.0.1:5432`
(`INBOK_POSTGRES_PORT` in `.env`) and the others are ready. `--build`
builds the emulator's image the first time, which downloads emulate from
npm, and again after `google-emulator/` changes. PostgreSQL's,
Meilisearch's, and SeaweedFS's data live in Docker volumes and survive
`docker compose down`. To delete them and start empty, e.g. after
changing the user or password in `.env`, run `docker compose down -v`. A
`.env` from before Meilisearch lacks `INBOK_MEILISEARCH_KEY` and
`INBOK_MEILISEARCH_URL`: Compose then stops with an error naming the key,
and the worker with one naming the URL. Copy both lines from
`.env.example`.

SeaweedFS holds message content and answers on `127.0.0.1:8333`
(`INBOK_SEAWEEDFS_PORT`). A `.env` from before it lacks the `INBOK_S3_*`
lines: Compose then stops with an error naming a key. Copy them, and
`INBOK_SEAWEEDFS_PORT`, from `.env.example`.

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

To try real Gmail instead, create a Google client as
[Connecting Gmail](deploy/README.md#connecting-gmail) says, with
`http://localhost:5173/api/v0/connected-accounts/google/callback` as its
redirect URI; Google allows plain http on localhost. Use a client of its
own, never a deployment's, as
[What Google limits](deploy/README.md#what-google-limits) explains.
Leaving the app in Testing is enough for development, with yourself as a
test user, but Google then ends its refresh tokens after seven days. In
`.env`, set `INBOK_GOOGLE_CLIENT_ID` and `INBOK_GOOGLE_CLIENT_SECRET` to
the client's and empty `INBOK_GOOGLE_BASE_URL`. Google sends the browser back to
`localhost:5173`, so on a remote development machine, browse through an
SSH tunnel from your own computer:
`ssh -N -L 5173:localhost:5173 <user>@<machine>`.

Meilisearch holds the search index on `127.0.0.1:7700`
(`INBOK_MEILISEARCH_PORT`). Its web dashboard at `http://localhost:7700`
asks for `INBOK_MEILISEARCH_KEY` and shows what the index holds, one
Meilisearch index per member, e.g. `messages_1`. The
index is rebuilt from PostgreSQL rather than kept, so losing its volume
costs nothing but the rebuild; the command that rebuilds it comes with
search itself. Each Meilisearch version gets a new volume,
since a version refuses to open another's data, so after
`compose.yaml` moves to a new version the old volume stays behind;
`docker volume ls` lists it, e.g. `inbok_meilisearch-1.56.1`, and
`docker volume rm` deletes it.

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
