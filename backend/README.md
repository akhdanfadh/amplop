# inbok backend

The Go server: the `inbok` command (`serve`, `migrate`, `export`), its
HTTP API, and its database code. Running `make` in the repository root
lists a shortcut for most commands here.

## Run the server

`serve` needs PostgreSQL with its migrations applied; see Local database
in `CONTRIBUTING.md` and Migrations below. From the repository root, load
`.env` and start the
development server:

```sh
cd backend
set -a; . ../.env; set +a
go run ./cmd/inbok serve
```

Or, to rebuild and restart the server whenever a Go file changes, use
[Air](https://github.com/air-verse/air). Its version is pinned in
`backend/tools.mod`, separate from the server's own dependencies in
`go.mod`:

```sh
cd backend
go tool -modfile=tools.mod air
```

Edits to `_test.go` files do not trigger a restart. The first run downloads
Air's dependencies. Air passes its shell's environment to `serve`, so load
`.env` there too.

In another terminal, check process health:

```sh
curl --fail-with-body http://127.0.0.1:8080/api/v0/health
```

Expect HTTP 200 with:

```json
{"data": {"status": "ok"}}
```

## API contract

JSON success responses put their payload in `data`. Response schemas live
only in the OpenAPI document: responses carry no `$schema` field or `Link`
header. Operation IDs use camelCase. Handlers build success bodies with
`respondData`. Schema names come from Go type names, e.g.
`ResponseHealthStatus`, and become the generated client's type names, so
renaming a payload type is a breaking change.

Huma's operation metadata and Go response types define the API contract.
The API base path is `/api/v0`; operation paths such as `/health` are
relative to it. The OpenAPI document advertises this relative base, so it
works on whatever host serves it. Locally, that is `http://127.0.0.1:8080`.
View its documentation at `http://127.0.0.1:8080/api/v0/docs` and its OpenAPI
document at `http://127.0.0.1:8080/api/v0/openapi.json`.

The committed copy of that document is `api/generated/openapi.json`. After
changing an operation, regenerate it from `backend` without starting the
server:

```sh
go run ./cmd/inbok export ../api/generated/openapi.json
```

## The probe

The probe is an example database write, removed before the first release.
It adds `amount` to a shared counter and records the entry, both in one
transaction:

```sh
curl --fail-with-body -H 'Content-Type: application/json' \
  -d '{"note": "hello", "amount": 3, "highlighted": false}' \
  http://127.0.0.1:8080/api/v0/probe/events
```

Expect HTTP 201 with the saved event and the new total, e.g.
`{"data": {"event": {"id": 1, "note": "hello", ..., "tag": null}, "total": 3}}`.
A body that breaks the contract gets a 422 listing each bad field, e.g.
`{"location": "body.amount", "message": "expected number >= 1"}`. Past a
total of 1000 it answers 409 `counter is full`; to empty it, run
`go tool -modfile=tools.mod goose reset`, then `migrate` again.

## Errors

Handlers map service errors to fixed messages, e.g. `probe.ErrCounterFull`
to `huma.Error409Conflict("counter is full")`, and never pass a raw error
to a 4xx helper: only 5xx details are hidden. Any other error is returned
as is and becomes a 500.

Error responses never echo input values (`errors[].value` is removed), and
a 5xx response keeps its `detail` but drops `errors[]`: a handler's raw Go
error is logged as `msg="unexpected error"` with the request ID instead of
being sent to the client.

Unknown routes (404) and unsupported methods (405, with an `Allow` header)
return the same Problem Details, never Gin's plain-text bodies. A trailing
slash is a different route: `/api/v0/health/` is a 404, not a redirect.

## Timeouts and shutdown

Each request's work, e.g. a database query, is cancelled after 10
seconds; a request cut off this way answers 500 and logs
`context deadline exceeded`. Stop the server with Ctrl+C or SIGTERM. It
stops accepting connections and gives in-flight requests up to 5 seconds
to finish.

## Configuration

`serve` reads these environment variables:

| Variable | Default | Meaning |
|---|---|---|
| `INBOK_HTTP_ADDR` | `127.0.0.1:8080` | Address and port to listen on, e.g. `0.0.0.0:8080`. |
| `INBOK_DATABASE_URL` | none, required | PostgreSQL connection URL; see Migrations below. |
| `GIN_MODE` | `debug` | Read by Gin itself. Use `release` for a deployed server; `debug` prints routes at startup. |

`export` reads none of them, so the committed document is the same on every
machine.

## Logs and request IDs

`serve` writes text logs to stderr, one line per request with its method,
path, status, and duration. The query string is never logged because it may
carry secrets. Server errors (5xx) log at `ERROR` level. A handler panic
returns a 500 Problem Details response and logs `msg=panic` with its stack
trace; the panic value never reaches the client.

Each response has an `X-Request-ID` header, and that request's log lines
carry the same value as `request_id`. Quote it when reporting a failed
request. An incoming `X-Request-ID` that parses as a UUID, e.g. nginx's
`$request_id`, is reused unchanged; any other value is replaced.

## Checks

From `backend`, check the code:

```sh
go test ./...
go vet ./...
```

Database tests start their own throwaway PostgreSQL in Docker
([Testcontainers](https://golang.testcontainers.org)), so Docker must be
running; without it they fail rather than skip. They never touch the
Compose database.

## Migrations

The database schema changes through numbered SQL files, called
migrations, in `backend/internal/repository/postgres/migrations`. Each
file has an `Up` part that applies the change and a `Down` part that
undoes it. `inbok migrate` applies the files the database has not seen
yet and records them; `serve` never does. Go does not read `.env`, so
load it into your shell first. From `backend`:

```sh
set -a; . ../.env; set +a
go run ./cmd/inbok migrate
```

| Variable | Default | Meaning |
|---|---|---|
| `INBOK_DATABASE_URL` | none, required | PostgreSQL connection URL, e.g. `postgres://inbok:inbok@127.0.0.1:5432/inbok?sslmode=disable`. |

For development, the [Goose](https://pressly.github.io/goose/) command is
pinned in `tools.mod` and reads the `GOOSE_*` settings in `.env`. From
`backend`, after loading `.env`:

```sh
go tool -modfile=tools.mod goose status               # applied and pending files
go tool -modfile=tools.mod goose reset                # run every Down part
go tool -modfile=tools.mod goose create -s add_x sql  # next numbered file
```

Never edit a migration a shared database has already applied; add a new
one. The probe's tables are an exception until the first release.

## Queries

Queries are SQL files in `backend/internal/repository/postgres/queries`.
[SQLC](https://sqlc.dev) reads them together with the migrations and
generates typed Go methods, e.g. `LockProbeCounter`, into
`backend/internal/repository/postgres/sqlc`; there, `models.go` lists
every table's columns. Every feature's queries share that one package, so
a query's name repeats its table's name, e.g. `InsertProbeEvent` for
`probe_events`. Settings are in `backend/sqlc.yaml`. After
changing a query or a migration, regenerate from `backend` and commit the
result; never edit it by hand:

```sh
go tool -modfile=tools.mod sqlc generate
```

SQLC parses SQL with PostgreSQL's own C code, so it needs a C compiler,
e.g. gcc. Its first run builds it, which takes about a minute.

## Package layout

The Go code in `backend/internal` has one package per feature for its
rules, and one package per technology that connects those rules to the
outside world. An arrow means "imports":

```text
httpapi --> probe <-- repository/postgres
```

- `probe`, like every feature package, holds the feature's types, its
  rules, its `Service`, and a `Storage` interface listing what it needs
  saved. It imports no Gin, Huma, pgx, or SQLC, and never imports
  `httpapi` or `repository/postgres`, so its tests need no server and no
  database.
- `httpapi` registers every feature's operations and holds the shared
  middleware and error handling. For each feature it declares an
  interface for what its operations call, e.g. `probeService`, so its
  tests answer with fixed results instead of running the feature's rules.
- `repository/postgres` implements every feature's `Storage` on one `DB`.
  A write that must save several features' data together is one `DB`
  method running one transaction, as `UpdateCounter` does. `DB` method
  names must be unique across features; when two clash, qualify the newer
  one.
- `cmd/inbok` builds each feature's `Service` and hands it to `httpapi`
  in `httpapi.Services`.

ADR021 in the project's decision records explains this layout and the
alternatives it was chosen over.

## Adding a feature

To add a feature, e.g. `account`:

1. `internal/account`: types, rules, `Service`, and `Storage`, tested with
   an in-memory `Storage`.
2. A migration and `queries/account.sql`; regenerate SQLC.
3. `repository/postgres/account.go`: the `Storage` methods, tested against
   PostgreSQL.
4. `httpapi/account.go`: the operations, an `accountService` interface,
   and `registerAccount`, tested with a fake `accountService`. Add an
   `Account` field to `Services` and call `registerAccount` in `New`.
5. `cmd/inbok/main.go`: set `Account` in `serve`'s `Services`.
6. Regenerate `api/generated/openapi.json`.

Each step can be its own commit.
