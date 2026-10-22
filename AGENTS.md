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

- Errors flow outward: storage turns database errors into the feature's
  errors, e.g. `probe.ErrCounterFull`; handlers turn those into a 4xx
  with a fixed message, e.g. `huma.Error409Conflict("counter is full")`;
  anything unexpected is returned as is and becomes a logged, hidden
  500. Error text holds no user input.
- A write that spans features is one `Storage` method shaped as an
  update function, run in one transaction in `repository/postgres`.
  Feature code receives no transaction, transaction provider, or
  transaction in `context.Context`.
- Names: middleware `with<Thing>`, operation registration
  `register<Feature>`, handlers `handle<Operation>`. One noun per
  concept across file, function, and test: `requestlog.go`,
  `withRequestLog`, `TestRequestLog`.
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
- A second 409 meaning on one operation needs its own `type` URI so the
  web can tell them apart.
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
