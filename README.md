# inbok - Unified Email Client

Requires Go 1.27.1 or newer.

From the repository root, start the development server:

```sh
cd backend
go run ./cmd/inbok
```

In another terminal, check process health:

```sh
curl --fail-with-body http://127.0.0.1:8080/api/v0/health
```

Expect HTTP 200 with:

```json
{"data":{"status":"ok"}}
```

JSON success responses put their payload in `data`.

When Huma is added, errors will use its Problem Details format. The probe
will demonstrate field validation, application failures, and browser error
handling. Map service errors in the HTTP layer.

Stop the server with Ctrl+C.

From `backend`, check the code:

```sh
go test ./...
go vet ./...
```
