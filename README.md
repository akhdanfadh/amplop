# inbok - Unified Email Client

Requires Go 1.27.1 or newer.

From the repository root, start the development server:

```sh
cd backend
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
Air's dependencies.

In another terminal, check process health:

```sh
curl --fail-with-body http://127.0.0.1:8080/api/v0/health
```

Expect HTTP 200 with:

```json
{"data": {"status": "ok"}}
```

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

The probe will demonstrate Huma's Problem Details errors for field
validation, application failures, and browser error handling. Map service
errors in the HTTP layer.

Error responses never echo input values (`errors[].value` is removed), and
a 5xx response keeps its `detail` but drops `errors[]`: a handler's raw Go
error is logged as `msg="unexpected error"` with the request ID instead of
being sent to the client.

Unknown routes (404) and unsupported methods (405, with an `Allow` header)
return the same Problem Details, never Gin's plain-text bodies. A trailing
slash is a different route: `/api/v0/health/` is a 404, not a redirect.

Stop the server with Ctrl+C or SIGTERM. It stops accepting connections and
gives in-flight requests up to 5 seconds to finish.

`serve` reads these environment variables:

| Variable | Default | Meaning |
|---|---|---|
| `INBOK_HTTP_ADDR` | `127.0.0.1:8080` | Address and port to listen on, e.g. `0.0.0.0:8080`. |
| `GIN_MODE` | `debug` | Read by Gin itself. Use `release` for a deployed server; `debug` prints routes at startup. |

`export` reads none of them, so the committed document is the same on every
machine.

`serve` writes text logs to stderr, one line per request with its method,
path, status, and duration. The query string is never logged because it may
carry secrets. Server errors (5xx) log at `ERROR` level. A handler panic
returns a 500 Problem Details response and logs `msg=panic` with its stack
trace; the panic value never reaches the client.

Each response has an `X-Request-ID` header, and that request's log lines
carry the same value as `request_id`. Quote it when reporting a failed
request. An incoming `X-Request-ID` that parses as a UUID, e.g. nginx's
`$request_id`, is reused unchanged; any other value is replaced.

From `backend`, check the code:

```sh
go test ./...
go vet ./...
```

The browser app in `web` requires pnpm. `package.json` pins the pnpm and
Node.js versions: another installed pnpm downloads and runs the pinned
one, and `pnpm install` downloads the pinned Node.js, which the `pnpm`
scripts then use. From `web`, install dependencies and start the
development server:

```sh
pnpm install
pnpm dev
```

Then open `http://localhost:5173`. The app is a single-page application
(SPA): the server sends the same HTML page for every path, and the browser
renders the matching route. There is no server-side rendering.

The development server forwards every `/api` request to the Go server, so
the browser talks to one address. Start the Go server as well; while it is
down, `/api` requests get a plain-text 502, not Problem Details.

| Variable | Default | Meaning |
|---|---|---|
| `INBOK_PROXY_TARGET` | `http://127.0.0.1:8080` | Go server URL that `/api` requests go to. Change it with `INBOK_HTTP_ADDR`, e.g. `http://127.0.0.1:9090`. |

Build and serve the production files locally:

```sh
pnpm build
pnpm preview
```

The build writes the browser files to `dist/client`, where `_shell.html`
is the page for every path. The rest of `dist` is only used by the build
and `pnpm preview`, which forwards `/api` the same way.

The app uses [Vite+](https://viteplus.dev), whose `vp` command runs
Vite. It is installed with the other dependencies, so the `pnpm` scripts
need no global install. Vite+ replaces the `vite` package with its own
copy; `pnpm-workspace.yaml` pins both, and they must change together.
To upgrade, change the `vite-plus` version there, run `pnpm install`,
then `pnpm exec vp migrate` to move the `vite` copy to the same version.

Each file in `src/routes` is one route. `pnpm dev` and `pnpm build`
regenerate `src/routeTree.gen.ts` from them. That file is committed so
type checking works on a fresh clone; never edit it by hand.

From `web`, check formatting, lint rules, and types in one command, or
fix what can be fixed automatically:

```sh
pnpm check
pnpm fix
```

Formatting uses Oxfmt's defaults. Lint rules and their reasons are in the
`lint` block of `vite.config.ts`; unused code is a lint error, not a type
error. To format on save and see lint errors while typing, install the
Oxc extension in your editor (VS Code: `oxc.oxc-vscode`; others:
<https://viteplus.dev/guide/ide-integration>).

The project uses TypeScript 7. If your VS Code still checks types with
TypeScript 6, install the `TypeScriptTeam.native-preview` extension.

Styling uses [Tailwind CSS](https://tailwindcss.com): utility classes in
`className`, generated from `src/styles.css`. `pnpm fix` sorts them in
Tailwind's recommended order. For class autocomplete, install the
Tailwind CSS IntelliSense extension (VS Code: `bradlc.vscode-tailwindcss`).

UI components come from [shadcn/ui](https://ui.shadcn.com). Its CLI copies
each component's source into `src/components/ui`, where it becomes our
code to edit; there is no component library to upgrade. Settings are in
`components.json`, and the theme (colors, radius, dark mode) is in
`src/styles.css`. From `web`, add a component with the pinned CLI, then
format it:

```sh
pnpm exec shadcn add dialog
pnpm fix
```

Imports starting with `@/` point into `src`, e.g.
`@/components/ui/button` is `src/components/ui/button.tsx`. The `cn` class
helper comes from the `cn` package; an older component that imports it
from `@/lib/utils` needs that import changed.

The browser calls Go through a client generated from the committed OpenAPI
document. After changing `api/generated/openapi.json`, regenerate the
client from `web`, then check it:

```sh
pnpm generate
pnpm check
```

`pnpm generate` rewrites `src/api/generated/inbok.ts`; commit it, never
edit it. It holds one function per operation, named by its operation ID,
plus the types it uses. `getHealth()` returns the success body, e.g.
`{ data: { status: "ok" } }`. If a contract change breaks code that uses
the client, e.g. a renamed field, `pnpm check` reports it there.

A failed call throws `ApiError`. Its `problem` field holds Go's Problem
Details (`title`, `status`, `detail`, `errors`). When the request never
reaches Go, `problem` has only a `title` (and `status` for the proxy's
502). Every generated function sends its request through
`src/api/transport.ts`, the only code that calls `fetch`.
