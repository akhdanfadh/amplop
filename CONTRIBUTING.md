# Contributing

How to set up a development machine and change amplop. The Go server is
described in `backend/README.md`, the browser app in `web/README.md`.
`AGENTS.md` lists the repository's rules for code, comments, the API
contract, and docs, for people and coding agents alike.

## Requirements

- Go, at the version `backend/go.mod` names or newer
- pnpm, which downloads the pinned pnpm and Node.js versions itself
- Docker, for PostgreSQL, Mailpit, Meilisearch, SeaweedFS, the Google
  emulator, and the Go tests that start their own copies of them
- A C compiler, e.g. gcc, for SQLC, which `make generate` and
  `make check` run
- GNU Make

## Quick start

From the repository root:

```sh
make services-start  # Docker services, listed below; creates .env
make db-migrate      # create the tables
make dev             # Go on 127.0.0.1:8080, the web app on localhost:5173
```

`make services-start` runs PostgreSQL, Mailpit, Meilisearch, SeaweedFS,
and the Google emulator. Open `http://localhost:5173`. Ctrl+C stops the
servers; `make services-stop` stops the Docker services and keeps their
data.

To create the first administrator, follow
[First administrator setup](backend/README.md#first-administrator-setup),
then open `http://localhost:5173/setup`. After setup, sign in at
`http://localhost:5173/sign-in`.

## The name

Write amplop in lowercase, even at the start of a sentence, in docs,
comments, and the interface alike. Code identifiers, environment
variables, header names, recorded fixtures, and test data that exercises
letter case keep their own spelling.

## Make

The `Makefile` runs every command in these files from the repository
root. Run `make` to list them, e.g. `make services-start`,
`make db-migrate`, then `make dev` for the Go server, its background
worker, and the web app together. `make check` runs what a commit must
pass; `make check-backend` and `make check-web` run one half, and
`make check-deploy` tests `deploy/upgrade.sh`'s order and rollbacks with
[bats](https://github.com/bats-core/bats-core), which `npx` fetches on
a machine's first run.
It also fails when generated code, e.g. `api/generated/openapi.json`, is
out of date with its source, and names the command that updates it.
Commands that need `.env` copy it from `.env.example` when it is missing.
The web app's component tests run in Playwright's Chromium, installed
once per machine with `pnpm -C web exec playwright install chromium`
(`web/README.md`, "Checks and tests").

## Local services

PostgreSQL, Mailpit, Meilisearch, SeaweedFS, and a Google emulator run in
Docker Compose (`compose.yaml`). From the repository root, create your
local settings once, then start them in the background:

```sh
cp .env.example .env
docker compose up -d --wait --build
```

`--wait` returns once PostgreSQL accepts connections on `127.0.0.1:5432`
(`AMPLOP_POSTGRES_PORT` in `.env`) and the others are ready. `--build`
builds the emulator's image the first time, which downloads emulate from
npm, and again after `google-emulator/` changes. PostgreSQL's,
Meilisearch's, and SeaweedFS's data live in Docker volumes and survive
`docker compose down`. To delete them and start empty, e.g. after
changing the user or password in `.env`, run `docker compose down -v`. A
`.env` from before Meilisearch lacks `AMPLOP_MEILISEARCH_KEY` and
`AMPLOP_MEILISEARCH_URL`: Compose then stops with an error naming the key,
and the worker with one naming the URL. Copy both lines from
`.env.example`.

SeaweedFS holds message content and answers on `127.0.0.1:8333`
(`AMPLOP_SEAWEEDFS_PORT`); [Message content
store](backend/README.md#message-content-store) explains its settings. A
`.env` from before it lacks the `AMPLOP_S3_*` settings, which Compose,
serve, and the worker need: Compose stops with an error naming a key.
Copy them, and `AMPLOP_SEAWEEDFS_PORT`, from `.env.example`.

Mailpit catches mail sent to `127.0.0.1:1025` (`AMPLOP_MAILPIT_SMTP_PORT`)
and shows it at `http://localhost:8025` (`AMPLOP_MAILPIT_WEB_PORT`). It deletes
its messages when it stops. To send amplop's test message there, see
[System email](backend/README.md#system-email).

The Google emulator stands in for Google's sign-in and Gmail at
`http://127.0.0.1:4000` (`AMPLOP_GOOGLE_EMULATOR_PORT`), so connecting a
Gmail account works without a Google project. Its seeded mailboxes and
what it lacks are in [Google emulator](backend/README.md#google-emulator).
A `.env` made before it existed lacks the `AMPLOP_GOOGLE_*` lines; copy
them from `.env.example`.

To try real Gmail instead, create a Google client as
[Connecting Gmail](deploy/README.md#connecting-gmail) says, with
`http://localhost:5173/api/v0/connected-accounts/google/callback` as its
redirect URI; Google allows plain http on localhost. Use a client of its
own, never a deployment's, as
[What Google limits](deploy/README.md#what-google-limits) explains.
Leaving the app in Testing is enough for development, with yourself as a
test user, but Google then ends its refresh tokens after seven days. In
`.env`, set `AMPLOP_GOOGLE_CLIENT_ID` and `AMPLOP_GOOGLE_CLIENT_SECRET` to
the client's and empty `AMPLOP_GOOGLE_BASE_URL`. Google sends the browser
back to `localhost:5173`, so on a remote development machine, browse
through an SSH tunnel from your own computer:
`ssh -N -L 5173:localhost:5173 <user>@<machine>`.

Meilisearch holds the search index on `127.0.0.1:7700`
(`AMPLOP_MEILISEARCH_PORT`). Its web dashboard at `http://localhost:7700`
asks for `AMPLOP_MEILISEARCH_KEY` and shows what the index holds, one
Meilisearch index per member, e.g. `messages_1`. The
index is rebuilt from PostgreSQL and stored message text rather than
kept, so losing its volume costs nothing but the rebuild: with the
worker running, `go -C backend run ./cmd/amplop search rebuild` rebuilds
it, and the worker does so itself every night ([Rebuilding the
index](backend/README.md#rebuilding-the-index)). Each Meilisearch version gets a new volume,
since a version refuses to open another's data, so after
`compose.yaml` moves to a new version the old volume stays behind;
`docker volume ls` lists it, e.g. `amplop_meilisearch-1.56.1`, and
`docker volume rm` deletes it.

### Gmail fixtures

Tests read Gmail's answers from fixtures in
`backend/internal/google/testdata`; those in `recorded` are real answers.
Record them only from a Gmail account made for testing, never a personal
mailbox, on a trial stack with that account connected, and fetch only the
messages made for the purpose:

1. Write the message as raw MIME and put it into that account with
   Gmail's `messages.insert`, which the connected account's permission
   allows. It arrives as received mail without being sent anywhere.
2. Fetch it with `messages.get` in `format=full`, and any part it sends by
   `attachmentId` with `messages.attachments.get`.
3. Replace the message and thread IDs, the account's address, the Google
   Cloud project number in the `Received` field, and every
   `attachmentId`, and save the answer under `recorded`.
4. Add a case to `receivedpart_test.go` saying what the answer shows.

[Message parts](backend/README.md#message-parts) lists what the
recordings showed about Gmail.

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
