# inbok web app

The browser app: React with TanStack Start, served as a single-page
application, calling the Go server through a generated client. Running
`make` in the repository root lists a shortcut for most commands here.

## Run the app

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

| Variable             | Default                 | Meaning                                                                                                   |
| -------------------- | ----------------------- | --------------------------------------------------------------------------------------------------------- |
| `INBOK_PROXY_TARGET` | `http://127.0.0.1:8080` | Go server URL that `/api` requests go to. Change it with `INBOK_HTTP_ADDR`, e.g. `http://127.0.0.1:9090`. |

## Production build

Build and serve the production files locally:

```sh
pnpm build
pnpm preview
```

The build writes the browser files to `dist/client`, where `_shell.html`
is the page for every path. The rest of `dist` is only used by the build
and `pnpm preview`, which forwards `/api` the same way.

## Vite+

The app uses [Vite+](https://viteplus.dev), whose `vp` command runs
Vite. It is installed with the other dependencies, so the `pnpm` scripts
need no global install. Vite+ replaces the `vite` package with its own
copy and includes Vitest; `pnpm-workspace.yaml` pins all three, and they
must change together. To upgrade, change the `vite-plus` version there,
run `pnpm install`, then `pnpm exec vp migrate` to move `vite` and
`vitest` to the versions it includes.

## Routes

Each file in `src/routes` is one route. `pnpm dev` and `pnpm build`
regenerate `src/routeTree.gen.ts` from them. That file is committed so
type checking works on a fresh clone; never edit it by hand.

## Checks and tests

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

Run the unit tests, files named `*.test.ts` next to the code they test:

```sh
pnpm test
```

Tests use [Vitest](https://vitest.dev), included in Vite+. Import its
functions from `vite-plus/test`, not `vitest`; `pnpm check` flags the
latter.

The project uses TypeScript 7. If your VS Code still checks types with
TypeScript 6, install the `TypeScriptTeam.native-preview` extension.

## Styling and components

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

## API client

The browser calls Go through a client generated from the committed OpenAPI
document. After changing `api/generated/openapi.json`, regenerate the
client from `web`, then check it:

```sh
pnpm generate
pnpm check
```

`pnpm generate` rewrites `src/api/generated`; commit it, never edit it.
`inbok.ts` holds one function per operation, named by its operation ID.
`model` holds each type as a [Zod](https://zod.dev) schema, which checks
a value while the app runs. `getHealth()` returns the success body, e.g.
`{ data: { status: "ok" } }`. If a contract change breaks code that uses
the client, e.g. a renamed field, `pnpm check` reports it there.

## Errors

A failed call throws `ApiError`. Its `kind` is `network` for communication
or body-read failures, `http` for an error response, or `response` for a
complete successful reply that breaks the expected contract. Deliberate
cancellation keeps its original error; a timeout remains a visible failure.

`status` holds the actual HTTP status whenever received. Optional `problem`
holds received Problem Details; HTML, unreadable bodies, and local failures
never produce a stand-in problem. Optional `requestId` holds the received
`X-Request-ID`. Optional `retryAfter` holds the received `Retry-After` header,
even if reading the body fails. A problem's own status can differ from the actual
HTTP status; request decisions use `ApiError.status`.
`method` holds the request method so screens can distinguish a failed write
from a failed read even when no response arrived.

Every generated function uses `src/api/transport.ts`, which checks success
bodies against generated schemas and logs contract mismatches to the browser
console. Error bodies with media type `application/problem+json` use
`src/api/received-problem.ts`: members can be omitted, invalid members are
ignored, and URI references can be relative or absolute. Extra fields are
dropped, not rejected.

The API's `detail` and input `message` are safe user text. `userMessage(err)`
from `src/api/problem.ts` shows the detail, then the title if no detail exists,
and local fallback text if neither is usable. Unfamiliar problem types still
show their safe text. Different business failures can share a status, so use
their type when adding different recovery actions. The probe form displays a
received request reference for reporting failures.

For writes, a missing response or HTTP 5xx cannot confirm whether the change
took effect. The message keeps any safe server explanation and asks the user
to check before submitting again. An unreadable success response instead says
the server reported success but its result could not be loaded. A received
4xx rejection keeps its normal explanation. Failed submissions keep the form's
values; the request reference remains available when received.

Zod's messages are developer text: `src/router.tsx`
replaces them app-wide with `src/api/validation.ts`, which states the
field's rule, e.g. "Enter a whole number from 1 to 10.".

## Reading data

Components call the API through generated
[TanStack Query](https://tanstack.com/query) hooks, e.g. `useGetHealth()`.
Query sends the request, caches the result, tracks loading and errors, and
refetches when the browser tab regains focus. `src/api/retry.ts` supplies Query's
retry defaults through `src/router.tsx`; transport sends each attempt once.
`src/api/retry-after.ts` handles header parsing separately from retry policy.

Reads retry connection failures, interrupted successful bodies, and actual HTTP
408, 429, 502, 503, or 504. They stop after two retries. A received rejection such
as 422 still prevents retry if reading its body fails. HTTP 500, complete replies
that break the success contract, and deliberately canceled reads do not retry.
The default applies to reads; writes have automatic retry explicitly disabled.
An operation must guarantee safe repetition before opting into write retries.

Writes use `networkMode: "always"` with `retry: false`. Query attempts them even
when its browser connectivity indicator says offline. That indicator can be
wrong about whether a self-hosted API is reachable. A failed write reports its
communication failure instead of waiting for reconnection. Reconnecting never
submits it again; another attempt requires the user to submit. The form keeps
entered values after a failed attempt.

Normal retry waits are 1 and 2 seconds. On 429 and 503, a usable `Retry-After`
can extend that wait, using integer seconds or an HTTP date. Invalid or absent
headers use normal backoff. Each wait may take at most 5 seconds; a longer
server delay stops retries instead of being shortened. Scheduled waiting totals
at most 10 seconds. Request execution and offline pauses are outside that
waiting limit; request deadlines are not configured yet. Canceling an
obsolete query also prevents its pending retries.

Under `pnpm dev`, a page sends each read twice and cancels the first:
React's development mode mounts every component twice to expose bugs.
The `pnpm build` output sends it once.

## Forms

Forms use [TanStack Form](https://tanstack.com/form), which holds each
field's value, with shadcn's `Field` components for labels and messages.
`src/components/probe-form.tsx` is the example: it sends a write with its
generated mutation hook, `useCreateProbeEvent()`, and locks its inputs
and button while the request runs. A failed request's message, e.g. "The
counter is full.", stays below the form until the next edit or submit.
Before sending, the form checks each edit against the generated schema,
`NewProbeEvent`, and sends nothing while a value breaks it. A field shows
its error once the user leaves it or clicks submit, then updates live.
If Go still rejects a value, e.g. after a rule changed in Go before
`pnpm generate`, `formErrors` from `src/api/problem.ts` attaches the server's
safe messages only to fields that the form renders. Editing a field clears its
server error. Rejected inputs outside those fields stay visible in the request
summary. Request failures without input errors do not enter form validation
state, so valid input can be submitted again without an edit.
