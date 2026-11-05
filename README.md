# inbok

An open-source, self-hostable, multi-user webmail client: one web
interface for several email accounts, e.g. Gmail, Outlook, or a private
IMAP server.

inbok is in early development and has no email features yet. The code is
a working skeleton: a Go API backed by PostgreSQL, a React browser app,
first-administrator setup, and one example database write, the probe, which
is removed before the first release.

## Requirements

These are for development. Self-hosting needs only Docker and git; see
[Self-hosting](#self-hosting).

- Go 1.27.1 or newer
- pnpm, which downloads the pinned pnpm and Node.js versions itself
- Docker, for PostgreSQL, Mailpit, the Google emulator, and the Go
  database and Gmail tests
- A C compiler, e.g. gcc, for SQLC, which `make generate` and
  `make check` run
- GNU Make

## Quick start

From the repository root:

```sh
make services-start  # PostgreSQL, Mailpit, Google emulator; creates .env
make db-migrate      # create the tables
make dev             # Go on 127.0.0.1:8080, the web app on localhost:5173
```

Open `http://localhost:5173`. Ctrl+C stops both servers;
`make services-stop` stops the Docker services and keeps the database.

To create the first administrator, follow
[First administrator setup](backend/README.md#first-administrator-setup),
then open `http://localhost:5173/setup`. After setup, sign in at
`http://localhost:5173/sign-in`.

## Self-hosting

To run inbok on your own server with HTTPS, follow `deploy/README.md`.
Docker Compose builds inbok from this repository and runs it with
PostgreSQL and Caddy.

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
