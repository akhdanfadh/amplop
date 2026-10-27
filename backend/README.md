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
A body that breaks the contract gets a 422 listing rejected inputs, e.g.
`{"location": "body.amount", "message": "Enter a valid value."}`. Past a
total of 1000 it answers 409 with type `/api/v0/problems/counter-full`
and detail `The counter is full.`; to empty it, run
`go tool -modfile=tools.mod goose reset`, then `migrate` again.

## Errors

Error responses use `application/problem+json`. They require `type`,
`status`, `title`, and `detail`. The body status equals the HTTP status;
the type identifies the condition, e.g. `/api/v0/problems/counter-full`.
These versioned paths work on each installation's own origin. `instance`
is initially omitted; tracing uses the `X-Request-ID` header.

Handlers map known service errors with `newProblem`, choosing safe public
text and a stable type. This includes expected 5xx failures: an explicitly
mapped 503 can explain maintenance. Unclassified errors are returned as is,
become a fixed 500 `internal-error`, and log their technical cause with the
request ID. Huma status helpers alone do not declare text safe for display.

Optional `errors` is a nonempty list of rejected inputs. Each entry has a
`location` and safe `message`, without `value`. Missing required fields have
precise locations, e.g. `body.items[3].name`, and "This field is required."
Numeric bounds, whole numbers, and string lengths receive correction text,
e.g. "Enter a whole number from 1 to 10." Simple fields with both limits get
a combined range; other fields keep the individual correction. Other Huma
input errors remain generic, because its original messages can include
rejected values. Explicit business mappings can also identify rejected inputs,
including on a 409. Public messages contain no raw technical causes.

Huma still validates request rules. `configureErrors` installs its error
model once before API construction, so runtime responses and the exported
OpenAPI schema describe the same public fields.

Huma reports a missing property's parent location. `configureValidation` uses
its formatter to carry the schema's field name in a private message marker.
`publicInputError` consumes that marker to build the precise location; the
marker never appears in public responses or OpenAPI. This relies on Huma's
required-property formatter arguments, covered by HTTP tests when upgrading.

For numeric and length limits, the formatter records safe text alongside
Huma's cached message. It uses schema limits, never submitted values. Responses
look up that text without parsing English messages; unknown messages keep the
generic fallback.

For known numeric and length failures, the response formatter follows the
operation's body schema to the rejected field. Ordinary object and array paths
can combine both limits, e.g. "Use 1 to 40 characters." Unsupported paths,
combined schemas, and extra numeric rules retain the individual correction.
Identical location/message pairs appear once, even when several rules fail.

Create all APIs and finish registering operations before serving requests.
Schema preparation populates the message lookup at startup; request handling
only reads it. Tests follow this order while sending requests concurrently.

Input failures retain their meanings: malformed requests use 400, body-read
timeouts 408, body limits 413, unsupported content types 415, and schema
violations 422. A 406 uses `not-acceptable`, title `Response format not
supported`, and detail `Ask for a JSON response.` The current configuration
falls back to JSON for unsupported `Accept` values; it does not enable strict
response-format rejection.

Unknown routes (404) and unsupported methods (405, with an `Allow` header)
return the same Problem Details, never Gin's plain-text bodies. A trailing
slash is a different route: `/api/v0/health/` is a 404, not a redirect.

## Timeouts and shutdown

Each request receives a context with a 10-second deadline. Database queries
and other calls using that context can stop when it expires. The deadline does
not forcibly stop handlers or undo completed writes. Returning the context's
deadline error from a handler produces a safe 500 Problem Details response and
logs `context deadline exceeded`.

Stop the server with Ctrl+C or SIGTERM. It stops accepting connections and gives
in-flight requests up to 5 seconds to finish.

## Configuration

`serve` reads these environment variables:

| Variable | Default | Meaning |
|---|---|---|
| `INBOK_HTTP_ADDR` | `127.0.0.1:8080` | Address and port to listen on, e.g. `0.0.0.0:8080`. |
| `INBOK_DATABASE_URL` | none, required | PostgreSQL connection URL; see Migrations below. |
| `INBOK_SETUP_TOKEN` | empty, setup unavailable | Operator-generated first-admin setup token. If nonempty, at least 32 Unicode code points. |
| `GIN_MODE` | `debug` | Read by Gin itself. Use `release` for a deployed server; `debug` prints routes at startup. |

`export` reads none of them, so the committed document is the same on every
machine.

## First administrator setup

After applying migrations, create the first administrator through the web app:

1. Generate a random token with `openssl rand -base64 32`.
2. Set `INBOK_SETUP_TOKEN` in `.env` to that output, then start or restart
   the backend. The Make commands load `.env`; direct Go commands need the
   shell-loading step shown in Run the server.
3. Open `http://localhost:5173/setup` with the web app running. Enter the
   token, an email address, and a password of 15 to 128 Unicode code points.
   Confirm the password in the form.
4. After setup succeeds, remove the token from `.env` and restart the backend.

An unset or empty token leaves setup unavailable while no administrator
exists. A nonempty token shorter than 32 Unicode code points stops `serve`
before it connects to PostgreSQL or opens the HTTP port. Token comparison
preserves the configured contents, including whitespace. The server never
logs the token or returns it in an error.

`GET /api/v0/setup` reports `unconfigured`, `open`, or `done`. An existing
administrator always means `done`, even after the token is removed.
`POST /api/v0/setup` creates one administrator atomically; concurrent claims
cannot create a second one. Setup creates no workspace, membership, session,
or cookie. If the token remains configured after setup, `serve` logs a
startup warning asking the operator to remove it.

Sign-in and TOTP enrollment are planned for later branches. This branch
creates the account but does not sign the administrator in.

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
go tool -modfile=tools.mod testifylint ./...
```

[testifylint](https://github.com/Antonboom/testifylint) checks tests
against testify's usage rules, e.g. expected value first; `--fix`
corrects most of them.

Database tests start their own throwaway PostgreSQL in Docker
([Testcontainers](https://golang.testcontainers.org)), so Docker must be
running; without it they fail rather than skip. They never touch the
Compose database.

## Writing tests

Tests check results with testify's `require` and `assert`
([docs](https://pkg.go.dev/github.com/stretchr/testify)); nothing else
from testify is used. Both report a failure; they differ in what
happens next:

- `require` stops the test. Use it for errors and for any value a later
  line depends on, e.g. a decoded body read by the next check.
- `assert` lets the test go on. Use it for checks that stand alone, so
  one run reports every mismatch.

Put the expected value first, with the same type as the actual one:
`assert.Equal(t, int64(42), total)`; an untyped `42` is an `int` and
never equals an `int64`. `require` must not run in a goroutine the test
started; it can only stop the test's own goroutine.

A JSON number decoded into `any` is a `float64`. Check it with
`assert.InDelta(t, 404, body["status"], 0)`, which is exact. Not
`assert.EqualValues`: it converts `500.5` to the int `500` and passes.

A test reads in three blocks split by blank lines: setup, action, and
checks. An error check that guards the action sits right under it:

```go
cfg, err := Load()
require.NoError(t, err)

assert.Equal(t, "127.0.0.1:8080", cfg.HTTPAddr)
```

When a block is hard to see from its code, e.g. a fake built over
several lines or an action that takes several steps, start each block
with a sentence saying what it does:

```go
// Given storage that loses its connection after the update runs.
storage := &fakeStorage{...}

// When an entry is added.
_, err := service.Add(ctx, entry)

// Then the same error reaches the caller.
require.ErrorIs(t, err, lost)
```

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
