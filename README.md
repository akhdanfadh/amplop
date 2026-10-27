# inbok

An open-source, self-hostable, multi-user webmail client: one web
interface for several email accounts, e.g. Gmail, Outlook, or a private
IMAP server.

inbok is in early development and has no email features yet. The code is
a working skeleton: a Go API backed by PostgreSQL, a React browser app,
first-administrator setup, and one example database write, the probe, which
is removed before the first release.

## Requirements

- Go 1.27.1 or newer
- pnpm, which downloads the pinned pnpm and Node.js versions itself
- Docker, for PostgreSQL and the Go database tests
- A C compiler, e.g. gcc, for SQLC, which `make generate` and
  `make check` run
- GNU Make

## Quick start

From the repository root:

```sh
make db-start    # PostgreSQL in Docker; creates .env from .env.example
make db-migrate  # create the tables
make dev         # Go on 127.0.0.1:8080, the web app on localhost:5173
```

Open `http://localhost:5173`. Ctrl+C stops both servers; `make db-stop`
stops PostgreSQL and keeps its data.

To create the first administrator, follow
[First administrator setup](backend/README.md#first-administrator-setup),
then open `http://localhost:5173/setup`. Sign-in is not implemented yet.

## Documentation

- `CONTRIBUTING.md`: Make commands, the local database, and changing the
  API.
- `backend/README.md`: the Go server, its API conventions, configuration,
  migrations, queries, and package layout.
- `web/README.md`: the browser app, its generated API client, error
  messages, data loading, and forms.

## License

GNU Affero General Public License, version 3; see `LICENSE.txt`.
