# inbok - Unified Email Client

Requires Go 1.27.1 or newer.

From the repository root, start the development server:

```sh
cd backend
go run ./cmd/inbok serve
```

In another terminal, check process health:

```sh
curl --fail-with-body http://127.0.0.1:8080/api/v0/health
```

Expect HTTP 200 with:

```json
{
  "$schema": "http://127.0.0.1:8080/api/v0/schemas/HealthResponse.json",
  "data": {"status": "ok"}
}
```

JSON success responses put their payload in `data`. Huma adds `$schema`
metadata and a `Link` header so tools can discover the response schema.
Operation IDs use camelCase. A shared typed response helper will be added
when the probe introduces another payload.

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

Huma v2.39.1 omits the API prefix in nested JSON Schema references. A
temporary `/schemas/...` redirect sends those requests to
`/api/v0/schemas/...`. Remove it when Huma fixes grouped schema references.

The probe will demonstrate Huma's Problem Details errors for field
validation, application failures, and browser error handling. Map service
errors in the HTTP layer.

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

From `backend`, check the code:

```sh
go test ./...
go vet ./...
```
