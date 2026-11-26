# inbok

An open-source, self-hostable, multi-user webmail client: one web
interface for several email accounts, e.g. Gmail, Outlook, or a private
IMAP server.

inbok is in early development, and Gmail is its only provider so far.

## Requirements

These are for development. Self-hosting needs only Docker and git; see
[Self-hosting](#self-hosting).

- Go 1.27.3 or newer
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

## Self-hosting

To run inbok on your own server with HTTPS, follow `deploy/README.md`.
Docker Compose builds inbok from this repository and runs it with
PostgreSQL, Meilisearch, SeaweedFS, and Caddy.

## Documentation

- `CONTRIBUTING.md`: Make commands, the local database, and changing the
  API.
- `backend/README.md`: the Go server, the web app it serves, its API
  conventions, configuration, migrations, queries, and package layout.
- `web/README.md`: the browser app, its generated API client, error
  messages, data loading, and forms.
- `deploy/README.md`: running inbok on a server: first run, upgrades,
  backups, administrator recovery, and using your own proxy.

## License

GNU Affero General Public License, version 3; see `LICENSE.txt`.
