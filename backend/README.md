# inbok backend

The Go server: the `inbok` command (`serve`, `worker`, `migrate`,
`health`, `export`, `recover-admin`), its HTTP API, the built web app it
serves, its background work, and its database code. Running `make` in the repository root lists a
shortcut for most commands here.

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

`inbok health` makes the same request to the address `serve` listens on
(`INBOK_HTTP_ADDR`; an all-interfaces address such as `0.0.0.0:8080`
reaches this machine), with a 3-second limit and no proxy. It exits 0 if the
answer is HTTP 200 and 1 otherwise, printing the reason. It reads no other
setting, so it needs no secrets. The container image runs it as its health
check, since the image has no `curl`.

## Background work

Work that outlasts a request, or must happen even if a process stops
halfway, runs in a separate process, `inbok worker`, on
[DBOS](https://docs.dbos.dev). DBOS saves each piece of work as a
**workflow** in PostgreSQL, in the `dbos` schema, and records each
finished **step**, so a stopped workflow resumes where it left off.
`serve` does not use DBOS. ADR004 in the project's decision records
explains the choice.

`make dev` runs the worker beside `serve`, restarting it when a Go file
changes; `make dev-worker` runs only the worker, and `make backend-worker`
runs it once without restarts. The worker needs the workflow tables that
`migrate` creates; on a database that lacks them, or has them at an older
version than its DBOS needs, it stops with an error saying the system
database is at a lower schema version, and `migrate` fixes it.

The worker reads these environment variables:

| Variable | Default | Meaning |
|---|---|---|
| `INBOK_DATABASE_URL` | none, required | PostgreSQL connection URL, as for `serve`. |
| `INBOK_HTTP_ADDR` | `127.0.0.1:8081`; the image sets `0.0.0.0:8080` | Address the worker answers health checks on: port 8081 for the worker, 8080 for `serve`, so both run on one machine. `.env` sets `serve`'s, so the Make commands pass the worker `INBOK_WORKER_HTTP_ADDR` instead; `deploy/compose.yaml` sets `127.0.0.1:8081`. |
| `INBOK_WORKER_ID` | `worker` | The name the worker resumes unfinished work under: 1 to 64 letters, digits, `.`, `-`, or `_`. |

DBOS's own `DBOS__APPVERSION` and `DBOS__VMID` would replace the version
and the ID that decide which work a worker resumes (see below), so the
worker refuses to start while either is set.

The worker serves HTTP only for its health check. Once DBOS has started
and resumed the worker's unfinished workflows, it answers every request on
`INBOK_HTTP_ADDR` with HTTP 200 and no body, including the
`/api/v0/health` that `inbok health` asks for. That says the worker started, not that it is
still making progress: DBOS exposes no state of its queue loop to check.
`inbok health`, run with the worker's `INBOK_HTTP_ADDR`, checks it like
the server:

```sh
INBOK_HTTP_ADDR=127.0.0.1:8081 go run ./cmd/inbok health
```

Ctrl+C or SIGTERM closes the health listener, then shuts DBOS down: it
cancels the running workflows and waits up to 3 seconds for each stage of
its shutdown, and the worker exits after 7 seconds at most. A workflow
cancelled this way is not marked failed: it stays pending. So does one
whose worker was killed or lost power. Only DBOS's own shutdown leaves a
workflow pending; DBOS marks one cancelled any other way as `CANCELLED`,
which no worker resumes, so the worker never hands DBOS a context that a
signal cancels.

### Which worker resumes what

Work waiting in a queue belongs to no worker: every worker polls the same
queue tables, and each workflow goes to exactly one of them. Scheduled
runs are shared the same way, since each run's workflow ID comes from the
schedule's name and time. Once a worker starts a workflow, though, the
workflow belongs to that worker's `INBOK_WORKER_ID` until it finishes.

When a worker starts, it puts every pending workflow of its own ID back
on the queue, and they resume from the first step that had not finished.
So a step can run twice when a worker stops during it. DBOS does not check
whether another worker with that ID is still running it, and nothing hands
a workflow to a worker with another ID. Therefore:

- Keep the ID when restarting or replacing a worker.
- Never run two workers with one ID: each start would rerun the other's
  running workflows, side effects and all, e.g. a second email.
- A worker removed for good leaves its started workflows pending. Start a
  worker once with its ID, e.g. `INBOK_WORKER_ID=worker-2`, to finish
  them; DBOS's `ResumeWorkflow` also queues one for any worker, but only
  once the old worker is surely gone.

### Scaling

Run one worker and raise how many workflows it runs at once. A worker runs
each workflow in its own goroutine, so more worker processes on the same
machine do no more than more concurrency in one. A second worker is worth
it for another machine, or to keep one kind of work apart from another,
and it needs its own stable ID. Concurrency is a DBOS queue setting, kept
in the database and re-read by workers about every second, so `serve`
could change it at runtime, e.g. from an admin setting. No queue needs
that yet; the first job that does adds the setting.

DBOS records a version on every workflow and resumes only workflows of its
own version. inbok fixes that version at `1` instead of DBOS's default, a
hash of the binary, so a new release resumes what the previous one left
unfinished. A workflow whose steps change must therefore still finish the
runs an older release started, e.g. with
[`dbos.Patch`](https://docs.dbos.dev/golang/tutorials/upgrading-workflows).

## Container image

From the repository root, `make image` builds the `inbok:local` image
from the `Dockerfile`; `IMAGE=name:tag` picks another name. Nothing is
pushed to a registry. It holds the binary, with the web app built in, on
distroless's `static` base: no shell, no package manager, and the
`nonroot` user (65532). It sets `GIN_MODE=release` and
`INBOK_HTTP_ADDR=0.0.0.0:8080`, since a container's loopback is
unreachable from other containers. Containers on the same Docker network
can then reach it, and the host's network only if the port is published.
To publish it for a proxy on the same host, bind loopback, e.g.
`-p 127.0.0.1:8080:8080`: a bare `-p 8080:8080` listens on every host
interface, and Docker's rules bypass host firewalls such as ufw. Its entry point is `inbok`, and its default command is
`serve`, so `docker run inbok:local migrate` runs a migration and
`docker run inbok:local worker` the worker.

The base images are pinned by version and digest; the `Dockerfile` says
how to update one. Builds cache downloaded Go modules and pnpm packages
between runs, and a change under `backend/` reuses the cached web build.
`deploy/README.md` runs the image with Docker Compose on a server.

## The web app

`serve` also serves the built web app on every path outside `/api`, so the
page and the API share one origin and one version. During development
Vite serves the page instead (`make dev`), and a `go build` or `go run`
from a fresh checkout includes no web app: `serve` logs
`this build has no web app; paths outside /api answer 404` and does
exactly that. To build one binary with the web app inside, from the
repository root:

```sh
make backend-build
```

It builds the web app, copies `web/dist/client` into
`backend/internal/webapp/dist`, and writes `backend/bin/inbok`. Go embeds
that folder when it compiles, so rebuild after any web change. Only the
folder's `.gitkeep` is committed. The copy stays after the build, so later
`go build`, `go run`, and Air also include it until you empty the folder:

```sh
find backend/internal/webapp/dist -mindepth 1 ! -path backend/internal/webapp/dist/.gitkeep -delete
```

For a GET or HEAD outside `/api`:

- A built file's exact path sends it, e.g. `/assets/index-BrBO-kCw.js`.
  Files under `/assets/` have hashed names and get
  `Cache-Control: public, max-age=31536000, immutable`; other files, and
  the shell, get `no-cache` and an `ETag` so the browser rechecks them.
  A `Range` header is ignored.
- Any other path gets `_shell.html` with 200, e.g. `/security`, and the
  browser renders the route.
- A path under `/assets/`, a path with a file extension, e.g.
  `/favicon.ico`, and a path with a segment starting with `.` get the
  API's Problem Details 404 (see Errors) instead: a browser that asks for
  a script never receives the page.

Other methods outside `/api` get a 405 with `Allow: GET, HEAD`, after the
same cross-origin check as the API: a write without `Origin` or
`Sec-Fetch-Site` gets its 403 first. `/api` and every path under `/api/`
belong to the API, so `/api/v1/x` is an API 404, not the shell. There are
no directory listings, and only the files in the build can be sent.

### Security headers

Every response from inbok's handlers, the API's and the web app's,
carries `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`,
and `X-Frame-Options: DENY`. The web app's files and shell also carry this
`Content-Security-Policy`:

```text
default-src 'self'; script-src 'self' <shell hashes>; object-src 'none';
base-uri 'none'; form-action 'self'; frame-ancestors 'none'
```

Pages load only inbok's own files and call only its own API. No `<style>`
element or `style` attribute in the HTML applies; styles set from script,
e.g. React's `style` props, still do. The build's shell has inline
scripts, and their text changes with every build, so `serve` reads the
embedded shell at startup and allows each one by its SHA-256 hash; no
other inline script runs. Nothing in the policy names a part of the web
app's code: whatever the build's shell holds is allowed, and the web app
keeps to the rest. API responses get no Content-Security-Policy; Huma's
docs page at `/api/v0/docs` sets its own, since it loads scripts from
unpkg.com. HTTPS-only (HSTS) is the proxy's header, because Go itself
serves plain HTTP. `web/README.md` lists what the policy means for web
code.

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

The contract lists problem types in the `ProblemType` schema:
`registerProblemTypes` collects every type an operation shows in a response
example, plus the shared types the API overview's table covers. A type a
handler maps but no operation documents is missing from the list, so
document each mapped problem with an example. `Problem.type` is any of the
listed types or any other URI reference, so clients still accept a type the
list lacks.

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
return the same Problem Details, never Gin's plain-text bodies. This
includes the web app's missing files and its other methods; see The web
app. A trailing slash is a different route: `/api/v0/health/` is a 404,
not a redirect.

## Timeouts and shutdown

Each request receives a context with a 10-second deadline. Database queries
and other calls using that context can stop when it expires. The deadline does
not forcibly stop handlers or undo completed writes. Returning the context's
deadline error from a handler produces a safe 500 Problem Details response and
logs `context deadline exceeded`.

Stop the server with Ctrl+C or SIGTERM. It stops accepting connections and gives
in-flight requests up to 5 seconds to finish. What is left of those 5 seconds
goes to password reset emails still being sent, which their requests did not
wait for (see [Password reset](#password-reset)); any still sending then are
lost, and a warning says so.

## Configuration

`serve` reads these environment variables:

| Variable | Default | Meaning |
|---|---|---|
| `INBOK_HTTP_ADDR` | `127.0.0.1:8080`; the image sets `0.0.0.0:8080` | Address and port to listen on, e.g. `0.0.0.0:8080`. |
| `INBOK_DATABASE_URL` | none, required | PostgreSQL connection URL; see Migrations below. |
| `INBOK_PUBLIC_URL` | none, required | The address users open inbok at, e.g. `https://mail.example.com`; see Public URL below. Invalid values stop startup. |
| `INBOK_TRUSTED_PROXIES` | empty, trust none | Comma-separated proxy network ranges in CIDR notation. Invalid entries stop startup. |
| `INBOK_SETUP_TOKEN` | empty, setup unavailable | Operator-generated first-admin setup token. If nonempty, at least 32 Unicode code points. |
| `INBOK_ENCRYPTION_KEYS` | none, required | Keys that encrypt stored TOTP secrets and the system email password; see Encryption keys below. Invalid entries stop startup. |
| `GIN_MODE` | `debug`; the image sets `release` | Read by Gin itself. Use `release` for a deployed server; `debug` prints routes at startup. |

`export` reads none of them, so the committed document is the same on every
machine.

### Public URL

`INBOK_PUBLIC_URL` is the origin users open inbok at: `https`, a host name
or IP address, and an optional port, e.g. `https://mail.example.com`. Plain
`http` is accepted only for `localhost` and loopback addresses, e.g.
`http://localhost:5173` for the Vite dev server. A path other than `/`, a
query, a fragment, or user info stops startup. Host names use letters,
digits, and hyphens; inbok lowercases them and converts an internationalized
domain to its `xn--` form. The example value in `.env.example` is for
development; a deployed server needs its own `https` origin.

Links in inbok's emails start with this value, never with a request's `Host`
header, which a client chooses, so a client cannot point an emailed link at
another server. When moving to another domain, keep the old one for 24
hours, until links sent before the move expire: whoever holds it receives
their secrets. The host is also the name inbok gives mail servers in SMTP's
`EHLO` greeting; an IP address is sent in brackets, e.g. `[192.0.2.10]`.

### Encryption keys

`INBOK_ENCRYPTION_KEYS` is a comma-separated list of `<id>:<key>` entries,
e.g. `2:<base64 key>,1:<base64 key>`. Each ID is a positive whole number
without leading zeros; each key is 32 random bytes in standard base64, from
`openssl rand -base64 32`. Startup fails, without printing key material, if
the list is empty or an entry is malformed or repeated.

The first entry encrypts new TOTP secrets and a newly entered system email
password with AES-256-GCM. Every entry can decrypt. Each stored secret
records its key ID and is bound to what it belongs to: a TOTP secret to its
user, the system email password to its mail server host, port, and username.
A ciphertext copied elsewhere does not decrypt. Backup codes and account
passwords use Argon2id hashes and do not depend on these keys.

To rotate, generate a key, put it first under an unused ID, keep the old
entries, and restart every API instance with the same list. Existing secrets
are not re-encrypted; each keeps its original key until that factor is
removed and enrolled again, and the system email password keeps its key
until an administrator saves a new password. Administrators cannot turn TOTP
off, so their factors can need an old key indefinitely. Remove an old key
only when neither query lists its ID:

```sql
SELECT key_id, count(*) FROM user_totp GROUP BY key_id;
SELECT password_key_id FROM system_email WHERE password_key_id IS NOT NULL;
```

Back up the key list separately from database backups, so one stolen backup
does not contain both the encrypted secrets and their keys, and so the keys
survive losing the database host. A database restore needs every key its
rows use. If a key is lost, authenticator codes for its users fail with
HTTP 500. Their unused backup codes still sign in, and members can use one to
turn TOTP off and enroll again. Administrators cannot remove the factor
themselves; use host-operator recovery below. A lost key also makes the
system email password unreadable: test messages fail with HTTP 500 and the
settings lose their tested time until an administrator enters the password
again and saves. The web app shows that 500 as a lost test result.

## Reverse proxies

`INBOK_TRUSTED_PROXIES` controls which peers may supply a forwarded client
address for sign-in and setup throttling, for the request limit on public
writes, and for the address saved with each new session. Empty ignores
forwarded addresses and uses the connection's address; behind a proxy, every
session then shows the proxy's address, and all clients share one request
limit, so one busy client can block everyone's sign-in. Trust
only the proxy's address ranges; trusting all addresses lets a caller forge
their throttling address and the sign-in address shown in the session list.

For one nginx proxy on the same host as Go, keep Go on `127.0.0.1:8080` and
set `INBOK_TRUSTED_PROXIES=127.0.0.1/32`. Go serves the web app too (see
The web app), so nginx forwards every path. Adapt the domain and
certificate paths in this HTTPS server block:

```nginx
server {
    listen 443 ssl;
    server_name inbok.example.com;
    ssl_certificate /path/to/fullchain.pem;
    ssl_certificate_key /path/to/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $http_host;
        proxy_set_header X-Forwarded-For $remote_addr;
    }
}
```

The example overwrites `X-Forwarded-For` with nginx's client address and
preserves the browser's Host, including its port. nginx forwards `Origin`
and `Sec-Fetch-Site` unchanged. Its [proxy header documentation](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_set_header)
explains the Host override. Keep the Go listener inaccessible to remote
clients. A container proxy has a different connection address; trust its
actual range rather than copying the loopback range. With the Compose
files in `deploy/`, [Using your own proxy](../deploy/README.md#using-your-own-proxy-instead-of-caddy)
gives the address to trust.

## Email addresses

Every address inbok takes goes through `emailaddress.Parse`: setup, sign-in,
`recover-admin`, registration, the system email From address, and each
message's recipient. It trims surrounding spaces and tabs, accepts one bare address of
at most 254 characters without hidden characters, and lowercases the letters
A to Z, local part included; other letters keep their case. Hidden
characters are controls, invisible formatting such as a zero-width space or
a right-to-left override, and Unicode spaces and separators. Anything else
is `emailaddress.ErrInvalid`, which handlers answer with a 422 on the field;
Huma accepts hidden characters, so for those the service's 422 is the one a
client sees. Saved addresses are compared
exactly, and the `users` table refuses an email with an uppercase ASCII
letter. New code that takes an address calls `Parse` in its service.

ADR025 in the project's decision records explains the choice and its cost: a
mail server that treats the local part's case as meaningful is not
supported. Mail accounts that members connect are outside this rule.

## Password hashes

Passwords and TOTP backup codes are stored as Argon2id hashes
(`passwordhash`), with OWASP's minimum profile: 19 MiB, two passes, one
lane. A check uses the stored hash's own costs, so changing the profile
keeps old passwords usable; it refuses a stored hash that asks for more
than 128 MiB, 10 passes, or 16 lanes.

Hashes and checks in progress share a 128 MiB memory budget across the
process: each waits until its own memory cost fits, in arrival order, so
6 hashes of the current profile run at once. The budget counts what
hashes ask for, not what the process holds: the garbage collector frees a
finished hash's memory later, so under load the process briefly holds
more. Every caller shares it:
sign-in, setup, registration, password reset, TOTP backup codes, and
`inbok recover-admin`.
A wait that outlives the request's context ends it; one that outlives the
10-second request deadline is a 500 (see
[Timeouts and shutdown](#timeouts-and-shutdown)). A TOTP request that ends
while waiting to check its first backup code made no guess, so it gives
its attempt back.

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
401; unknown users still incur an Argon2id password check. A deactivated
account with the right password returns HTTP 403 `account-deactivated`
instead; see [Member management](#member-management).

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
code sign-in removes only its own attempt in the same transaction. A code
request refused before any code is checked, e.g. with an expired challenge,
TOTP off, or as an administrator turning it off, releases its attempt: only
guesses count.

Expired challenges are pruned for that user during a successful password
sign-in. Saving a new session, after a password or a code, also deletes that
user's expired sessions in the same transaction. There is no scheduled
cleanup; idle users' expired rows remain unusable until removed. Both cookies
use Secure, HttpOnly, SameSite=Lax, and Path=/, without Domain.
Authentication responses use Cache-Control: no-store.

`GET /api/v0/sessions/current` returns the current email, role,
`totpRequired`, and `emailVerified`. An administrator with confirmed TOTP receives admin access
and `totpRequired: false`. An administrator without confirmed TOTP receives
member access and `totpRequired: true`. Members always receive member access
and `totpRequired: false`, whether or not they enable TOTP.

Identity reads the current account and factor through the session row in one
query. A session revoked by enrollment confirmation cannot gain admin access
from a previous session check. The web app's `/security` page handles
enrollment, backup codes, and turn-off; sign-in shows the code step.

`DELETE /api/v0/sessions/current` deletes the checked session and clears its
cookie, returning HTTP 204. A missing or expired session returns HTTP 401
and clears the cookie, also when a handler finds the session ended after
the access check; the web app treats that as signed out. Signing
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

An administrator can deactivate a member, which ends all of that member's
sessions; see [Member management](#member-management). Member recovery is
not implemented yet. Host-operator administrator recovery is described
below.

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
attempt returns HTTP 429 with `Retry-After`: the seconds until one more
attempt fits, rounded up. These limits are constants too.

Every throttle (sign-in, setup, TOTP codes, verification links,
registration, and password reset links) counts with `attemptlimit.Wait`,
which says how long until one more attempt fits; a new throttle uses it
too. Each keeps its own key, storage, and lock: under the lock it counts the
earlier attempts, then saves this one only if it fits. A
refusal is an `*attemptlimit.Error` that wraps the feature's `ErrThrottled`
and carries the wait; `retryAfterProblem` in `httpapi` turns it into the
header. Every documented 429 lists the header as required, so a new 429 must
send it.

In front of those throttles, every write that needs no session (`rateLimited`
in `httpapi`) passes a limit kept in memory, per operation and client: up to
20 requests in a row, and each second gives one back, up to 20. A client is
an IPv4 address, or an IPv6 address's /64 block (`clientprefix`). Past the
limit, the request gets HTTP 429 `too-many-requests` with `Retry-After`
before any database work, so a flood cannot hold database connections or
start password hashes. Over
any 15 minutes it allows more requests than any database limit, so people
retrying by hand meet those limits first. Counts are per process and reset on
restart; at most 100,000 clients are tracked at once. A new public write gets
the limit, and its 429 in the contract, with no code of its own.

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

An administrator who only forgot their password can use
[Password reset](#password-reset) instead, once their email is verified.
Setup does not verify the first administrator's email, so one who forgets
their password before verifying it needs this command.

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

In a container from the image, the binary is on the `PATH`. The
container must have `INBOK_DATABASE_URL` set, as one running `serve` does.
Replace the container name with yours:

```sh
docker exec -it inbok inbok recover-admin admin@example.com
```

For piped input, use `docker exec -i` without `-t` so it uses the one-line
input mode. This repository's development Compose file starts PostgreSQL
and Mailpit; it does not run inbok itself.

Recovery checks that the account is an administrator, then replaces the
password, removes TOTP and every backup code, ends all sessions and sign-in
challenges, clears that account's code and sign-in attempt rows, deletes its
password reset links, and records
an `admin-recovered` event in `audit_events`. These writes commit together.
Counts are printed without secrets. Recovery sends no notification email.

The administrator signs in with the new password and has member access until
they enroll TOTP on the Security page. Confirmation restores administrator
access. Enrollment happens in the web app. A sign-in that verified the old
password before recovery cannot create a session afterward, even if recovery
sets the same password again.

Recovery leaves `users.deactivated_at` as it is. The web app cannot
deactivate an administrator, but one deactivated directly in the database
stays unable to sign in after recovery; clear the column in SQL as well. A
deactivation done in SQL also leaves the account's sessions and sign-in
challenges in place, refused only while the column is set, and records no
audit event; delete them too.

If the command reports unconfirmed success after interruption or a database
error, inspect the audit table before retrying. It contains no credentials:

```sql
SELECT id, occurred_at, action, target_user_id
FROM audit_events
WHERE action = 'admin-recovered'
ORDER BY id DESC;
```

## System email

System email is the mail server inbok sends its own messages through,
separate from members' mailboxes. It has no environment variables: an
administrator with confirmed TOTP configures it on the web app's Settings
page, which calls `GET` and `PUT /api/v0/system-email` and
`POST /api/v0/system-email/test-message`. It sends test messages and
[email verification](#email-verification) links.

The settings are one `system_email` row: host, port, security, an optional
username and password, and the From address and name. Security is `tls`
(TLS from the first byte, usually port 465), `starttls` (required: a server
that does not offer it fails the send, usually port 587), or `none`. `none`
sends the whole message unencrypted and unchecked, so use it only for a
server on the same host or a trusted private network. It allows no username, and inbok never signs in
without TLS, so the password never crosses the network in clear text. TLS
checks the server's certificate against the host name with the system's
roots and needs TLS 1.2 or newer. The host, username, and From address are saved
without surrounding spaces, so a username of only spaces means no sign-in.

The password is write-only. Reads return `hasPassword`; the value is
encrypted with `INBOK_ENCRYPTION_KEYS` and bound to the host, port, and
username it was entered for. A save without a password keeps the saved one
only when those three are unchanged, so an administrator cannot move it to a
server they control and read it there. Clearing the username clears the
saved password.

Saving replaces the whole row, clears the tested time, and records a
`system-email-saved` event in `audit_events` with the saving administrator
as `actor_user_id`, in the same transaction. The event holds no setting
values. A test message sends a short fixed message to the given recipient
with the saved settings. A pass sets the tested time. A `failed` result
clears it, even when only the recipient was refused, and so does a password
that no longer decrypts. Tests are not audited. A test that ends after a
newer save leaves that save's tested time alone.

A test answers `accepted` or `failed` with the step that failed: `connect`,
`tls`, `auth`, `sender`, `recipient`, or `timeout`. Accepted means the server
took the message for delivery, not that it arrived. One send, from dialing to
the server's last reply, has at most 8 seconds, inside the 10-second request
limit. A failed send logs a warning with its purpose, e.g. `test message`,
the reason, host, port, security, username, and the first 512 bytes of the
error. inbok never adds the password; the text can include the server's own
reply. The warning carries the `request_id` of the request that sent it:

```text
time=2026-11-01T10:00:00.000+07:00 level=WARN msg="system email send failed" purpose="test message" reason=auth host=smtp.example.com port=587 security=starttls username=inbok@example.com err="..." request_id=0b9d6a56-3f4e-4c1a-9d2b-7e8f0a1b2c3d
```

To list saves, with no setting values:

```sql
SELECT id, occurred_at, actor_user_id
FROM audit_events
WHERE action = 'system-email-saved'
ORDER BY id DESC;
```

### Testing with Mailpit

`make db-start` also starts Mailpit, which accepts any message and shows it
in a web page instead of delivering it. On the Settings page, save:

| Field | Value |
|---|---|
| Mail server | `127.0.0.1` |
| Port | `1025`, or `INBOK_MAILPIT_SMTP_PORT` from `.env` |
| Security | None |
| Username and password | empty |
| From address and name | any, e.g. `inbok@example.com` and `inbok` |

Then send a test message and open `http://localhost:8025`
(`INBOK_MAILPIT_WEB_PORT`) to read it. Verification links arrive there too.
Mailpit deletes its messages when it stops and keeps the newest 500.

## Email verification

A signed-in account proves it controls its email address by following a
link inbok emails to it. Nothing sends a link automatically: the user asks
for one on the web app's Security page. An unverified account signs in and
works as before; `GET /api/v0/sessions/current` reports `emailVerified`.

`POST /api/v0/account/email-verification` sends a link to the session's own
account and returns the address it went to. It answers 409
`email-already-verified`, 409 `system-email-not-configured` before system
email is saved, or 429 `email-verification-throttled` after 3 links in 15
minutes or 10 in 24 hours, with `Retry-After`, like the other limits. The
limit counts the account's saved links, so anything that deletes them, such
as a redeem, starts it over. The link is saved before the send, so a 502
`email-not-sent` still counts toward the limit, and a timed-out message may
arrive and work. A failed send logs the same warning as a test message, with
`purpose="email verification"`, but without the server's reply, which can
quote the link. A test message to the same server shows the reply.

The link is `INBOK_PUBLIC_URL` + `/verify-email#` + a 32-byte random secret
in unpadded base64url. Browsers never send the part after `#` to a server,
so the secret stays out of inbok's and its proxy's request logs and out of
Referer headers. It still sits in the email, in the browser's history, and
with any service that rewrites links in mail. Only the secret's SHA-256 hash
is saved, in `email_verification_links`, with the address it was sent to. A
link expires after 24 hours. Every unexpired link works until one is used.

`POST /api/v0/email-verifications/redeem` takes the secret, needs no session,
and signs no one in. It marks the account verified, records an
`email-verified` audit event with the account as `target_user_id` and no
actor, and deletes every link of that account. A link for an account that is
already verified succeeds without changing its verified time or recording an
event. An unknown, used, or expired link, or one sent to an address the
account no longer has, answers 404 `email-verification-link-invalid`. The
address must match exactly; both are saved lowercased, as
[Email addresses](#email-addresses) describes. Redeem has no attempt limit:
a 256-bit secret cannot be guessed.

A deactivated account cannot ask for a link: the request answers like a
signed-out one, HTTP 401. Deactivation deletes its unused links, so a link
sent before it answers 404. Sending a link deletes the account's expired
links; a refused request or redeem changes nothing. Each accepted
registration request also deletes every account's expired links (see
[Registration](#registration)). There is no scheduled cleanup, so while no
one registers, an account that never asks again keeps its expired rows,
which cannot be used. Deleting an account deletes its links.

To list verifications:

```sql
SELECT id, occurred_at, target_user_id
FROM audit_events
WHERE action = 'email-verified'
ORDER BY id DESC;
```

## Member management

An administrator with admin access lists every account and deactivates or
reactivates members. Administrators themselves, the signed-in one included,
cannot be deactivated or reactivated, so an instance never loses its last
administrator this way. Deactivation keeps the account; nothing deletes a
user.

`GET /api/v0/members` returns every account, oldest first, in
`data.members`. Each has its `id` (the user ID), `email`, `role`,
`joinedAt`, `emailVerified`, `totpEnabled`, and `deactivatedAt`, null while
active. It holds nothing about mail, sessions, or activity. There is no
paging.

`POST /api/v0/members/{id}/deactivate` and `.../reactivate` return HTTP 200
with the member as saved in `data.member`. An unknown ID answers 404
`member-not-found`, an administrator 409 `member-is-administrator`, and an
ID that is not a positive whole number 422 at `path.id`. Both are safe to
repeat: a member already in the asked state is returned unchanged, with no
second audit event.

Deactivation locks the user, the same lock sign-in, TOTP, email
verification, and password reset take, and checks the member's role under it.
In one transaction it sets `users.deactivated_at`, deletes the member's
sessions, pending sign-in challenges, unused email verification links, and
unused password reset links, and records a
`member-deactivated` event with the administrator as `actor_user_id` and
the member as `target_user_id`. It keeps the password, the TOTP factor and
backup codes, and the verified address, so reactivation, which clears the
column and records `member-reactivated`, restores the account as it was.

A deactivated account is kept out at each step:

- Sign-in hashes the password before taking the user lock, then reads the
  flag under it, so a deactivation that commits in between still counts. The
  right password answers HTTP 403 `account-deactivated`, "This account is
  deactivated. Ask your administrator."; a wrong one still answers 401 and
  counts toward the throttle. Only someone who knows the password learns the
  account is deactivated.
- TOTP changes and asking for a verification link answer HTTP 401 if a
  deactivation committed after their session check. Redeeming a link answers
  404.
- The session check requires an active account, and the code step answers
  the same 403 and deletes the challenge. Deactivating through the API deletes
  every session and challenge, so these two matter only when
  `deactivated_at` is set directly in the database.

To list deactivations and reactivations:

```sql
SELECT id, occurred_at, action, actor_user_id, target_user_id
FROM audit_events
WHERE action IN ('member-deactivated', 'member-reactivated')
ORDER BY id DESC;
```

## Registration

While an administrator keeps registration open, anyone who can reach inbok
can create a member account. A visitor gives only an email first. The
password comes after they follow a link emailed to them, so no one can
choose the password of an email they do not control, and no half-made
account ever exists. ADR028 in the project's decision records explains
each choice below and the alternatives it was chosen over.

### Opening and closing

`GET` and `PUT /api/v0/registration/settings` need administrator access.
`PUT` takes `{"open": true}` or `{"open": false}`. Registration starts
closed.

- Opening needs system email settings that passed a test message;
  otherwise it answers 409 `system-email-not-tested`. Closing always
  works.
- Every save records `registration-opened` or `registration-closed` with
  the administrator as `actor_user_id` and no target, even a save that
  changes nothing.
- Saving new system email settings, or a failed test, clears their tested
  time. Registration then reports `paused`: still open, but no visitor can register until a
  test passes. Nothing closes it behind the administrator's back.
- Closing deletes every registration attempt and link, so no visitor's
  email or IP address is kept while it stays closed. The cost: a link sent
  before the close stops working even after registration reopens, and the
  limits below start from zero.

`GET /api/v0/registration` is public and answers `{"open": true}` only when
visitors can register now; paused answers `false`.

### Asking for a link

`POST /api/v0/registration/links` takes `{"email"}` and answers 200
`{"status": "requested"}` whatever happens to that email:

- an email without an account gets a link,
  `INBOK_PUBLIC_URL/register/complete#` + a secret, made and stored like an
  [email verification](#email-verification) link (only its SHA-256 hash is
  saved, in `registration_links`), which expires after 24 hours;
- an email with an account gets a notice instead: someone tried to register
  it, and here is the sign-in page. A deactivated account gets it too, since
  the answer must not depend on the account's state;
- an email that got 3 messages in the last hour, or 5 in 24 hours, gets
  nothing. Links and notices count; requests that sent nothing do not.

The same answer for all three keeps the response from telling whether an
email has an account. Other answers:

- 409 `registration-closed`: registration is closed or paused.
- 429 `registration-throttled` with `Retry-After`: this client IP asked 10
  times in 15 minutes, or everyone together asked 50 times in the last hour.
  Both limits are checked before the email's own, so a 429 never depends on
  the email.
- 502 `email-not-sent`: the send failed. The link stays saved and counts,
  and after a timeout the message may still arrive. The warning logged has
  `purpose="registration link"` or `"registration notice"` and no server
  reply.

The email counts exactly as `Parse` returns it: `kate+news@example.org` and
`kate@example.org` are two emails. Folding them would need each provider's
rules and can hand an account to the wrong person, so it is left out.

Two differences remain, and ADR028 accepts both: a request that sends
nothing answers sooner, and while mail is failing it still answers 200
where the others answer 502. Either shows that the email was asked about
recently, never whether it has an account. Anyone can also use up the 50
per hour and block registration for an hour; `DEBT.md` lists the defenses
for when that happens.

How it runs: every request first reads whether registration is open, so a
closed inbok answers without waiting. It then waits for one PostgreSQL
advisory lock that all registration requests share
(`LockRegistrationAttempts`), because the 50 per hour is one count over
everyone. Under the lock it
deletes attempts older than 24 hours, checks again that registration is
open, counts the limits, and saves the attempt and the link. Each accepted
request also deletes every expired registration link and every expired
email verification link, skipping rows another transaction holds; there is
no background job.

### Creating the account

`POST /api/v0/registration/links/redeem` takes `{"secret", "email",
"password"}` and needs no session. The password follows setup's rule, 15 to
128 characters, checked by `account.CheckPassword`. The email is the one the
visitor types; it must be the link's, after `emailaddress.Parse`. Otherwise
someone could ask for a link to an address they control, send it to a
visitor, and have the visitor create that account, which its owner can
later take back with a password reset.

1. It reads the link without a lock. An unknown or expired link, or one
   sent to another email, is refused here, so a guessed secret costs no
   password hash.
2. It hashes the password with Argon2id, waiting for room in the hash
   budget (see [Password hashes](#password-hashes)), so one live link sent
   many times at once cannot start a hash per request.
3. In one transaction it locks the registration row, the system email row,
   the `users` table (the lock setup takes), and the link, then checks
   everything again.
4. It creates a member with `email_verified_at` set, since following the
   link proved the email, deletes every link to that email, and records
   `member-registered` with the new member as `target_user_id` and no
   actor.

It answers 201 `{"id", "email"}` and sets no cookie; the web app sends the
visitor to sign in. Other answers:

- 422 on `body.password` for a password outside the rule.
- 422 on `body.email` for an invalid email, or "Enter the email address
  this link was sent to." for another one. It names no address.
- 404 `registration-link-invalid`: unknown, used, or expired.
- 409 `registration-closed`: closed or paused. Pausing keeps links, so the
  link may work later; closing deleted it.
- 409 `registration-account-exists`: the email got an account first, e.g.
  through a second submission of the same link that waited for the first.

There is no attempt limit: a 256-bit secret cannot be guessed. Both
registration writes also pass the in-memory request limit every public
write has (see [Sign-in and browser sessions](#sign-in-and-browser-sessions)).

To list registration events:

```sql
SELECT id, occurred_at, action, actor_user_id, target_user_id
FROM audit_events
WHERE action IN ('registration-opened', 'registration-closed', 'member-registered')
ORDER BY id DESC;
```

## Password reset

A user who forgot their password follows a link sent to their verified
email and chooses a new one (`passwordreset`). ADR030 in the project's
decision records explains the choices.

### Links

`password_reset_links` keeps the SHA-256 hash of each link's
`randomsecret` secret, the user, the `email` it was sent to, and when it
was created and expires, one hour later. Asking again keeps earlier links;
completing a reset deletes all of the user's links, and so do deactivating
a member and `inbok recover-admin`. Each accepted request also deletes
every user's expired links, skipping rows a completion or deactivation
holds.

### Asking for a link

`POST /api/v0/password-reset/links` takes `{"email"}`, needs no session,
and answers 202 `{"status": "requested"}` whatever happens to that email.
Other answers:

- 422 on `body.email` for an invalid email.
- 429 `password-reset-throttled` with `Retry-After`: this client asked 10
  times in 15 minutes. It never depends on the email.
- 429 `too-many-requests`: the in-memory request limit every public write
  has (see [Sign-in and browser sessions](#sign-in-and-browser-sessions)).

The operation calls `passwordreset.Requests.Request` with the email and the
client's address, which answers the same for every address. Before it
returns, it only:

1. checks that the email is valid (`emailaddress.Parse`);
2. under a lock on the client, records an attempt in
   `password_reset_attempts`, unless the client already made 10 in the
   last 15 minutes. A client is an IPv4 address or an IPv6 /64
   (`clientprefix`). Over the limit it returns an `*attemptlimit.Error`
   wrapping `passwordreset.ErrThrottled` with the wait.

It reads nothing about the account, so its answer and timing are the
same whether the address has one. The rest is background work that
`Request` starts and does not wait for, run by `passwordreset.Background`
as the notice after a reset is (see [Completing a reset](#completing-a-reset)).
In one transaction it locks the account with the email, if any, and
decides:

- no account, an unverified email, or a deactivated account: nothing is
  sent;
- 3 links sent to the account in the last hour, or 5 in 24 hours: nothing
  is sent. These limits are silent, so the answer cannot depend on them.
- otherwise: it saves a link for the account's stored email and sets the
  attempt's `user_id` and `linked_at`. The email limits count `linked_at`,
  the time the link was saved, which can come later than the request.

Then it mails the link to `<public URL>/reset-password#<secret>`. The secret
exists only in memory and the email; the message is `Private`, so a failed
send's log leaves out the server's reply. A failed send keeps its link and is
only logged. Requested emails are kept only for accounts that got a link,
as `user_id`. The next accepted request deletes attempts whose request and
link times are both over 24 hours old; nothing deletes them on a schedule.

### Completing a reset

`POST /api/v0/password-reset/links/redeem` takes `{"secret", "email",
"password"}` and needs no session. The email is the one the person types; it
must be the link's, after `emailaddress.Parse`, so a link someone sends for
an account they control cannot set its password for a person who types
their own address. It answers 200 `{"email"}`, the account's email, so
the sign-in form can be filled in, and sets no cookie. Other answers:

- 422 on `body.password` for a password outside the rule.
- 422 on `body.email` for an invalid email, or "Enter the email address
  this link was sent to." for another one.
- 404 `password-reset-link-invalid`: the link is unknown, used, or expired,
  or its account can no longer reset. One answer for every reason.
- 429 `too-many-requests`: the in-memory request limit.

There is no attempt limit: a 256-bit secret cannot be guessed.

The operation calls `passwordreset.Completion.Complete` with the link's
secret, the email typed, and the new password, which follows setup's rule,
15 to 128 characters (`account.CheckPassword`).

1. It reads the link and its user without a lock. A missing or expired
   link, a deactivated user, a user whose email is no longer the link's,
   or an email given that is not the link's is refused here, so a guessed
   secret costs no password hash.
2. It hashes the password with Argon2id, waiting for room in the hash
   budget (see [Password hashes](#password-hashes)).
3. In one transaction it locks the user, the lock sign-in, deactivation,
   and recovery take, and checks everything again.
4. It saves the password, deletes the user's sessions, sign-in
   challenges, and reset links, deletes the sign-in attempts counted for
   the user's email, and records `password-reset` with the user as
   `target_user_id` and no actor. TOTP and backup codes stay, so the next
   sign-in still asks for a code.

A password outside the rule is `account.ErrPasswordInvalid`, an invalid
email `emailaddress.ErrInvalid`, and a live link given with another email
`passwordreset.ErrEmailMismatch`. Every other refusal, a malformed, unknown,
used, or expired link, a link whose account changed its email, or a
deactivated user, is the same `passwordreset.ErrLinkInvalid`.
On success it returns the user, so the caller can name the email to sign in
with, and creates no session.

Once the reset commits, `Complete` starts background work that emails the
user that their password changed, naming the time, and returns without
waiting for it. `passwordreset.Background` runs that work in a goroutine: it
keeps the request's context values, so its logs carry the `request_id`, but
not its cancellation, and has 30 seconds of its own. A failed send is only
logged, by the mailer as for any system email, and does not undo the reset.
A panic is logged with its stack, not its value, instead of stopping the
server.

To list password resets, and recent requests:

```sql
SELECT id, occurred_at, target_user_id
FROM audit_events
WHERE action = 'password-reset'
ORDER BY id DESC;

SELECT id, created_at, client_prefix, user_id, linked_at
FROM password_reset_attempts
ORDER BY id DESC;
```

## Logs and request IDs

`serve` writes text logs to stderr, one line per request with its method,
path, status, and duration. The query string is never logged because it may
carry secrets. Server errors (5xx) log at `ERROR` level. A handler panic
returns a 500 Problem Details response and logs `msg=panic` with its stack
trace; the panic value never reaches the client.

Each response has an `X-Request-ID` header, and that request's log lines
carry the same value as `request_id`, including warnings services log while
serving it, e.g. a failed email send. Quote it when reporting a failed
request. An incoming `X-Request-ID` that parses as a UUID, e.g. nginx's
`$request_id`, is reused unchanged; any other value is replaced.

`cmd/inbok` builds one root logger with `logging.New` and passes it to every
service and to `httpapi`. A line gets `request_id` when it is logged with
the request's context, e.g. `logger.WarnContext(ctx, ...)`.

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
yet and records them; `serve` never does. `migrate` then creates or
upgrades the workflow tables [DBOS](https://docs.dbos.dev) keeps
background work in, in the `dbos` schema. Their migrations ship inside the
DBOS Go SDK, not as files here, and have no Down part. One PostgreSQL lock
covers both steps, so a second `migrate` on the same database waits for
the first.

Upgrading the SDK can bring new workflow table migrations. Review them
like inbok's own: the release before must keep working on the migrated
tables. `dbos.MigrationStatements("dbos", n)` in Go lists the SQL from
version `n` on; `SELECT version FROM dbos.dbos_migrations` shows a
database's current version.

The role in `INBOK_DATABASE_URL` needs the right to create a schema in its
database, and `migrate` also connects to the server's `postgres` database
to check that inbok's exists. The owner a Docker PostgreSQL creates from
`POSTGRES_USER`, as in `compose.yaml`, has both.

Go does not read `.env`, so load it into your shell first. From
`backend`:

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

Goose knows only inbok's files: `goose reset` and `make db-reset` leave
the `dbos` schema in place. To empty that too, run `DROP SCHEMA dbos CASCADE;` in `psql`; the
next `migrate` creates it again.

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
httpapi --> systememail <-- repository/postgres
                        <-- smtp
httpapi --> emailverification <-- repository/postgres
            emailverification --> systememail
httpapi --> registration <-- repository/postgres
            registration --> systememail, account
httpapi --> passwordreset <-- repository/postgres
            passwordreset --> systememail, account
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
- `smtp`, like `repository/postgres`, is a technology package: it
  implements the `Sender` interface that `systememail` declares, using the
  emersion SMTP libraries. A technology package is named for its technology
  and imports features, never the other way around.
- A leaf package holds one rule that several features share, such as
  `emailaddress` (the address rule), `attemptlimit` (the window count),
  `randomsecret` (emailed link secrets and their hashes), `clientprefix`
  (which addresses count as one client), or `logging` (how loggers are
  built and tagged with the request). It
  imports no feature or technology package and is named for its rule.
- A feature can use another feature through an interface it declares:
  `emailverification` sends through a `mailer` interface, which `cmd/inbok`
  fills with `systememail`'s `Mailer`. A feature may import another's plain
  rules directly: `registration` uses `account.CheckPassword` and
  `account.User`.
- `webapp` embeds the built web app and serves it as a static site:
  files, the shell for browser routes, caching, and the CSP. It knows the
  build's layout, not the web app's code. `httpapi` declares a `webApp`
  interface and sends it the reads outside `/api`, keeping the API's 404
  and 405 for the rest, so its tests use a fake.
- `dbos` is the technology package for [Background work](#background-work):
  it opens DBOS for the worker, launches it, and shuts it down. It is the
  only package besides `repository/postgres` that imports the DBOS SDK,
  whose package is also named `dbos`.
- `cmd/inbok` builds each feature's `Service` and hands it to `httpapi`
  in `httpapi.Services`, and loads `webapp`'s build for `ServeWebApp`.

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
   `Services` and call `registerAccount` in `New`, before
   `registerProblemTypes`, which lists the problem types registered so far.
5. `cmd/inbok/main.go`: set `Account` in `serve`'s `Services`.
6. Regenerate `api/generated/openapi.json`.

Each step can be its own commit.
