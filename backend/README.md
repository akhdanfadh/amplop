# inbok backend

The Go server: the `inbok` command (`serve`, `migrate`, `export`,
`recover-admin`), its
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

GET, HEAD, and OPTIONS must not perform user actions, such as changing mail.
Session checks may refresh activity timestamps or delete expired session rows.
Cross-site protection always allows these methods; use write methods for actions.

Access checks use Huma middleware because it can read the matched operation's
`Metadata["access"]`. Declare `session.AccessPublic` for public operations,
`session.AccessMember` for any valid session, or `session.AccessAdmin` for
administrators with confirmed TOTP. Missing or invalid metadata requires admin
access. This metadata is shared endpoint configuration. Each request's context
holds the checked session reference and user's identity. Handlers pass the
reference to session operations without reading its hash.

Protected responses use `Cache-Control: no-store` so browser and proxy HTTP
caches do not retain account data or reuse stale authentication responses.
This does not prevent storing the session cookie or clear the web app's
TanStack Query cache.

Write requests must include `Sec-Fetch-Site` or `Origin`. Modern browsers
send these headers. For manual requests to the local API, send
`Origin: http://127.0.0.1:8080`. Proxies must preserve these headers and
the original `Host`. Missing source headers receive a 403.

The server uses Go's [CrossOriginProtection](https://pkg.go.dev/net/http#CrossOriginProtection)
for Fetch Metadata and Origin checks, including setup and sign-in. It adds
the missing-header rejection above; Go alone permits requests without both
headers. There is no CSRF token or configured cross-origin exception. Keep the
web app and API on the same origin, including the scheme, host, and port.

JSON success responses put their payload in `data`. Response schemas live
only in the OpenAPI document: responses carry no `$schema` field or `Link`
header. Operation IDs use camelCase. Handlers build success bodies with
`respondData`. Schema names come from Go type names, e.g.
`ResponseHealthStatus`, and become the generated client's type names, so
renaming a payload type is a breaking change.

Huma's operation metadata and Go response types define the API contract.
Each operation's response helper calls `problemResponses(api, statuses...)`,
then sets its failure descriptions and examples. Pass that map as `Responses`
when registering the operation. The shared helper includes a `default`
Problem response for shared errors. Avoid `Operation.Errors`: Huma adds
separate 422 and 500 entries to that list.
The API overview explains shared statuses and shows a field-error example.

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
  -H 'Origin: http://127.0.0.1:8080' \
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
| `INBOK_TRUSTED_PROXIES` | empty, trust none | Comma-separated proxy network ranges in CIDR notation. Invalid entries stop startup. |
| `INBOK_SETUP_TOKEN` | empty, setup unavailable | Operator-generated first-admin setup token. If nonempty, at least 32 Unicode code points. |
| `INBOK_ENCRYPTION_KEYS` | none, required | Keys that encrypt stored TOTP secrets; see Encryption keys below. Invalid entries stop startup. |
| `GIN_MODE` | `debug` | Read by Gin itself. Use `release` for a deployed server; `debug` prints routes at startup. |

`export` reads none of them, so the committed document is the same on every
machine.

### Encryption keys

`INBOK_ENCRYPTION_KEYS` is a comma-separated list of `<id>:<key>` entries,
e.g. `2:<base64 key>,1:<base64 key>`. Each ID is a positive whole number
without leading zeros; each key is 32 random bytes in standard base64, from
`openssl rand -base64 32`. Startup fails, without printing key material, if
the list is empty or an entry is malformed or repeated.

The first entry encrypts new TOTP secrets with AES-256-GCM. Every entry can
decrypt. Each stored secret records its key ID and is bound to its user, so
a ciphertext copied to another user does not decrypt. Backup codes and
passwords use Argon2id hashes and do not depend on these keys.

To rotate, generate a key, put it first under an unused ID, keep the old
entries, and restart every API instance with the same list. Existing secrets
are not re-encrypted; each keeps its original key until that factor is
removed and enrolled again. Administrators cannot turn TOTP off, so their
factors can need an old key indefinitely. Remove an old key only when no
`user_totp` row uses its ID:

```sql
SELECT key_id, count(*) FROM user_totp GROUP BY key_id;
```

Back up the key list separately from database backups, so one stolen backup
does not contain both the encrypted secrets and their keys, and so the keys
survive losing the database host. A database restore needs every key its
rows use. If a key is lost, authenticator codes for its users fail with
HTTP 500. Their unused backup codes still sign in, and members can use one to
turn TOTP off and enroll again. Administrators cannot remove the factor
themselves; use host-operator recovery below.

## Reverse proxies

`INBOK_TRUSTED_PROXIES` controls which peers may supply a forwarded client
address for sign-in and setup throttling and for the address saved with each
new session. Empty ignores forwarded addresses and uses the connection's
address; behind a proxy, every session then shows the proxy's address. Trust
only the proxy's address ranges; trusting all addresses lets a caller forge
their throttling address and the sign-in address shown in the session list.

For one nginx proxy on the same host as Go, keep Go on `127.0.0.1:8080` and
set `INBOK_TRUSTED_PROXIES=127.0.0.1/32`. Build the web app first. Adapt the
domain and file paths in this HTTPS server block:

```nginx
server {
    listen 443 ssl;
    server_name inbok.example.com;
    ssl_certificate /path/to/fullchain.pem;
    ssl_certificate_key /path/to/privkey.pem;
    root /path/to/inbok/web/dist/client;

    location /api/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $http_host;
        proxy_set_header X-Forwarded-For $remote_addr;
    }

    location / {
        try_files $uri /_shell.html;
    }
}
```

The example overwrites `X-Forwarded-For` with nginx's client address and
preserves the browser's Host, including its port. nginx forwards `Origin`
and `Sec-Fetch-Site` unchanged. Its [proxy header documentation](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_set_header)
explains the Host override. Keep the Go listener inaccessible to remote
clients. A container proxy has a different connection address; trust its
actual range rather than copying the loopback range.

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

Setup does not sign the administrator in. The web app opens sign-in after
completion and shows the token-removal instructions there. Before setup is
complete, every web page leads to setup. After completion, `/setup` returns
to the normal entry page. The API's database claim enforces setup completion
independently of these browser redirects.

## Sign-in and browser sessions

`POST /api/v0/sessions` accepts email and password. Without confirmed TOTP,
it creates a fresh session and returns HTTP 201 with
`{"data":{"step":"complete"}}`. With confirmed TOTP, it returns
`{"data":{"step":"totp"}}` and a temporary `__Host-inbok-challenge` cookie;
no session is created. The factor check and the chosen write share one user
lock, so enrollment confirmation cannot race a password-only session into
existence afterward. Unknown email and wrong password return the same HTTP
401; unknown users still incur an Argon2id password check.

`POST /api/v0/sessions/totp` accepts a string `code` and the challenge cookie.
Six ASCII digits select an authenticator code; other input is checked as a
backup code. Success atomically consumes the challenge and the TOTP step or
backup code, creates a session, and clears the challenge cookie. Only then
does the response issue the session cookie. A failed commit reveals no
session secret and leaves the challenge and code usable.

Challenges expire after five minutes. Wrong codes update their saved failure
count; the fifth deletes the challenge and requires password sign-in again.
Starting a new challenge does not reset the shared per-user code allowance:
5 failed or pending attempts per 15 minutes and 20 per 24 hours. Successful
code sign-in removes only its own attempt in the same transaction.

Expired challenges are pruned for that user during a successful password
sign-in. Saving a new session, after a password or a code, also deletes that
user's expired sessions in the same transaction. There is no scheduled
cleanup; idle users' expired rows remain unusable until removed. Both cookies
use Secure, HttpOnly, SameSite=Lax, and Path=/, without Domain.
Authentication responses use Cache-Control: no-store.

`GET /api/v0/sessions/current` returns the current email, role, and
`totpRequired`. An administrator with confirmed TOTP receives admin access
and `totpRequired: false`. An administrator without confirmed TOTP receives
member access and `totpRequired: true`. Members always receive member access
and `totpRequired: false`, whether or not they enable TOTP.

Identity reads the current account and factor through the session row in one
query. A session revoked by enrollment confirmation cannot gain admin access
from a previous session check. The web app's `/security` page handles
enrollment, backup codes, and turn-off; sign-in shows the code step.

`DELETE /api/v0/sessions/current` deletes the checked session and clears its
cookie, returning HTTP 204. A missing or expired session returns HTTP 401
and also clears the cookie; the web app treats that as signed out. Signing
out clears account data and submitted credentials from the web app's Query
cache while retaining the public setup status.

Each new session saves the request's `User-Agent`, cut to 512 bytes, and the
client address resolved as in [Reverse proxies](#reverse-proxies). They help
the owner recognize a session. No access decision uses them, because the
client chooses its `User-Agent`. Sessions created before these columns
existed have neither. Both are personal data: an expired row keeps them until
its user signs in again, with no time limit for users who never return.
Operators with a retention duty must delete expired rows themselves.

`GET /api/v0/sessions` lists the account's live sessions, newest sign-in
first, in `data.sessions`. Each has an `id`, `browser` and `os` names read
from the saved header, `signedInAt`, `lastActiveAt`, `signedInFrom`, and
`current` for the session that made the request. Unrecognized names and an
unknown address are empty strings. `lastActiveAt` changes at most hourly, as
described below. The `id` is a random UUID that names the session to its
owner; the token hash never leaves the server. Listing deletes nothing.

`DELETE /api/v0/sessions/{id}` ends another session of the account and
returns HTTP 200 with `{"data":{"ended":1}}`. A missing, expired, or other
account's session returns the same HTTP 404 (`session-not-found`); an
expired one is deleted as well. The current session returns HTTP 409
(`session-is-current`); sign-out ends it. `DELETE /api/v0/sessions/others`
ends every other session of the account and returns how many live sessions it
ended, which can be 0. Both are safe to repeat. Ending a session stops its
next request, not one already running, and does not stop someone who knows
the password from signing in again; there is no password change yet.

Account deactivation and member recovery are not implemented yet.
Host-operator administrator recovery is described below.

The cookie is `__Host-inbok-session`, with `Secure`, `HttpOnly`,
`SameSite=Lax`, `Path=/`, and no `Domain`. `Max-Age` is the remaining maximum
session lifetime. Go sets these attributes explicitly; the name alone does
not configure them. See the [cookie attribute reference](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Set-Cookie).
Use HTTPS for deployed instances. For local development, use
`http://localhost:5173`; browsers provide a localhost exception for Secure
cookies. If the browser rejects the cookie on plain HTTP, use local HTTPS
rather than disabling Secure or changing the cookie name.

Session rules are constants, not environment settings:

- Idle expiry is 7 days since the stored last-use time; maximum lifetime is
  30 days from sign-in and is never extended.
- Each authenticated request loads the session and current user from
  PostgreSQL. It updates last-use time only when at least an hour has passed
  since the stored value. This reduces writes, not reads.
- Expiry is checked before that update. An expired row is deleted when used
  or when its user signs in again; there is no cleanup job. Sign-out deletes
  the current row.
- Every sign-in creates a new 32-byte random secret. PostgreSQL stores only
  its SHA-256 hash. Cookie secrets, passwords, and hashes must not be logged.

Throttling uses PostgreSQL across API instances. Within 15 minutes, at most
5 failed sign-ins are allowed for one normalized email and client address,
and 50 failures for one address across all emails. Setup attempts share the
address limit. A pending attempt counts until a successful verification
removes it, so parallel password checks cannot bypass the limits. A blocked
attempt returns HTTP 429. These limits are constants too.

`DELETE /api/v0/totp/enrollment` cancels a signed-in account's pending
enrollment without a code. Success returns HTTP 200 with `data.status:
"off"` and `data.backupCodesLeft: 0`; repeating it when no factor remains
also succeeds. A confirmed authenticator returns HTTP 409 (`totp-already-on`).
Existing sessions and their cookies stay unchanged.

`POST /api/v0/totp/backup-codes` requires a signed-in account and a fresh
six-digit authenticator `code`. It replaces all old backup codes and returns
HTTP 200 with `data.backupCodes`, containing ten replacement codes shown once.
Backup codes cannot authorize this operation. Existing sessions stay signed
in, and failure preserves the old set. The shared code-attempt limits apply.

`POST /api/v0/totp/disable` lets a signed-in member turn off TOTP with a
fresh authenticator code or an unused backup code in `code`. Success returns
HTTP 200 with `data.status: "off"` and `data.backupCodesLeft: 0`. The factor
and every backup code are removed together; existing sessions stay signed
in. Administrators receive HTTP 403 and must keep TOTP enabled.

## Administrator recovery

Only a trusted host operator should run this command for an administrator
who lost their password, authenticator, and backup codes. Before recovering
another administrator, verify their identity through your deployment's
operator process. The command trusts database write access; it does not ask
for an inbok session, old password, TOTP code, or setup token, and it does not
check which machine runs it. Members cannot be recovered this way.

Apply migrations first. The server may keep running. From the repository root:

```sh
set -a; . ./.env; set +a
go -C backend run ./cmd/inbok recover-admin admin@example.com
```

Only `INBOK_DATABASE_URL` is required. Terminal input prompts twice for a
password of 15 to 128 Unicode code points without echo, then asks
`Continue? [y/N]`. Only `y` or `yes` proceeds. With piped input, the command
reads the first line and skips confirmation. Password spaces are preserved.
Use a password manager or another trusted program to supply that line; never
put the password in arguments, environment variables, or shell history.

For a deployment container, replace the container name and binary path with
yours. The container must have `INBOK_DATABASE_URL` configured:

```sh
docker exec -it inbok /app/inbok recover-admin admin@example.com
```

For piped input, use `docker exec -i` without `-t` so it uses the one-line
input mode. This repository's Compose file starts only PostgreSQL; it does
not provide an application container.

Recovery checks that the account is an administrator, then replaces the
password, removes TOTP and every backup code, ends all sessions and sign-in
challenges, clears that account's code and sign-in attempt rows, and records
an `admin-recovered` event in `audit_events`. These writes commit together.
Counts are printed without secrets. Notification email is deferred until
system email exists.

The administrator signs in with the new password and has member access until
they enroll TOTP on the Security page. Confirmation restores administrator
access. Enrollment happens in the web app. A sign-in that verified the old
password before recovery cannot create a session afterward, even if recovery
sets the same password again.

If the command reports unconfirmed success after interruption or a database
error, inspect the audit table before retrying. It contains no credentials:

```sql
SELECT id, occurred_at, action, user_id
FROM audit_events
WHERE action = 'admin-recovered'
ORDER BY id DESC;
```

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

### HTTP feature files

Keep shared paths, service interfaces, handler dependencies, and the
`register<Feature>` entry point at the top. That entry point calls a
`register<Operation>` function for each operation.

Group each operation under a three-line comment banner:

```go
// ========================
// First administrator
// ========================
```

Keep its constants, request and response types, input resolvers, registration,
named handler, response helper, and error mapping together under that banner.
Helpers used by only one operation stay in its section. Handler types and
methods are private; operations without dependencies can use plain functions.

## Adding a feature

To add a feature, e.g. `account`:

1. `internal/account`: types, rules, `Service`, and `Storage`, tested with
   an in-memory `Storage`.
2. A migration and `queries/account.sql`; regenerate SQLC.
3. `repository/postgres/account.go`: the `Storage` methods, tested against
   PostgreSQL.
4. `httpapi/account.go`: follow [HTTP feature files](#http-feature-files).
   Declare an `accountService` interface and a `registerAccount` entry point.
   Test the operations with fake services. Add an `Account` field to
   `Services` and call `registerAccount` in `New`.
5. `cmd/inbok/main.go`: set `Account` in `serve`'s `Services`.
6. Regenerate `api/generated/openapi.json`.

Each step can be its own commit.
