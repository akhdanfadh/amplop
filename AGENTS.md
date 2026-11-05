# AGENTS.md

inbok is a Go API (`backend/`) and a React single-page app (`web/`). The
contract between them is `api/generated/openapi.json`. Read the doc for
the area you change before editing it:

- Go code: `backend/README.md`, especially Package layout and Adding a
  feature.
- Web code: `web/README.md`.
- Setup, Make commands, or an API change: `CONTRIBUTING.md`.

## Done

- `make check` passes. It needs Docker for the Go database tests.
- Generated code comes from `make generate`, committed with the change
  that caused it. Its generators own these paths:
  `backend/internal/repository/postgres/sqlc`, `api/generated`,
  `web/src/api/generated`, `web/src/routeTree.gen.ts`.

## Running things

- Trial runs use their own stack: copy `.env.example` to a file outside
  the repo, set other ports and a `COMPOSE_PROJECT_NAME`, and pass it as
  `ENV_FILE`, e.g. `make db-start dev ENV_FILE=/tmp/trial.env`. The
  default `.env` stack holds the developer's own data.
- New migrations come from `make db-migration NAME=add_x`, which puts
  the file in the migrations folder.

## Go

- Errors flow outward: feature handlers map known errors with `newProblem`,
  choosing status, type, and safe text. An explicitly mapped 5xx keeps that
  text. Unexpected errors are returned as is and become a logged, fixed 500.
  Public error text and input messages contain no rejected values or causes.
- A throttle's refusal is an `*attemptlimit.Error` carrying its wait; map
  it with `retryAfterProblem`, which adds `Retry-After`.
- A write with public access gets `withRateLimit`'s per-client limit and
  its documented 429 automatically; add nothing by hand.
- Services take a `*slog.Logger` from `cmd/inbok`, built by `logging.New`.
  Log with the request's context, e.g. `logger.WarnContext(ctx, ...)`, so the
  line carries its `request_id`.
- A write that spans features is one `Storage` method shaped as an
  update function, run in one transaction in `repository/postgres`.
  Feature code receives no transaction, transaction provider, or
  transaction in `context.Context`.
- Names: middleware `with<Thing>`, feature registration `register<Feature>`,
  operation registration `register<Operation>`, handlers `handle<Operation>`.
  One noun per concept across file, function, and test: `requestlog.go`,
  `withRequestLog`, `TestRequestLog`.
- One verb per kind of test: `Validate` holds input to a rule and returns
  an error; `Parse` does that and returns the normalized or typed value;
  `Verify` compares a secret with its stored form; `check` asks whether
  saved state is still usable at a time. `Match` finds which of several
  stored forms a secret matches.
- `backend/tools.mod` changes only through
  `go get -modfile=tools.mod -tool`; `go mod tidy` there copies the
  app's dependencies in. Goose's version stays equal in `go.mod` and
  `tools.mod`.

## API contract

The web app reads these from the contract; keep them stable:

- Not-blank text is `pattern:"\\S"`, which the web words as "Can't be
  blank.". Any other not-blank pattern reads "Enter a valid value.".
- Field limits (`minimum`, `maximum`, `minLength`, `maxLength`) are the
  web's error messages, so changing one is a contract change.
- A 422's `errors[].location` is `body.<field>`, e.g.
  `body.items[3].tags`; only that marks the field in a form.
- Received Problem Details can omit members and use relative URI references.
  Parse them with `web/src/api/received-problem.ts`.
- Inbok problems require `type`, `status`, `title`, and `detail`. Types use
  versioned URI references, e.g. `/api/v0/problems/validation-error`.
  `instance` is initially omitted. Tracing uses `X-Request-ID`.
- A second 409 meaning on one operation needs its own `type` URI so the
  web can tell them apart.
- The contract's `ProblemType` lists every type an operation shows as a
  response example, plus the shared ones. Give each type the web compares a
  response example, and type the web's constant with `ProblemType`.
- Every 429 sends `Retry-After` in whole seconds; the web words the wait.
- A 204 response needs transport work first: `web/src/api/transport.ts`
  requires a response schema.

## Web

- Writes go through generated mutation hooks, e.g.
  `useCreateProbeEvent()`; `useQuery` is for GET operations.
- A request failure lives in the mutation (`create.error`); field
  problems live in the form's errors. A request failure in the form's
  error map blocks a retry until the next edit.
- `ApiError.status` is the actual HTTP status. Its optional `problem` is
  received data; use `status` for request decisions and preserve missing
  problems as missing.
- `src/components/ui` is our code: edit a component to pass lint. Add
  one with `make web-add-ui NAME=dialog`.
- Before upgrading `vite-plus` or adding a Vite plugin, read the NOTE
  comments in `web/pnpm-workspace.yaml`.

## Comments

- A doc comment says what the code does, its inputs, and what it returns.
  A reason sits inline above the line it explains. A reason that belongs
  to no single line is a `DESIGN:` paragraph after the doc sentence.
- `NOTE:` marks only a surprise a reader must know: a tool's unexpected
  behaviour, a workaround, a dependency on undocumented behaviour.
