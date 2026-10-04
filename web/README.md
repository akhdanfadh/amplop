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

A failed call throws `ApiError`. Its `kind` says why: `unreachable` (no
response arrived), `status` (an error status), or `unexpected` (a body
that breaks the contract). Its `problem` field holds Go's Problem Details
(`title`, `status`, `detail`, `errors`). When Go sent none, `problem` has
only a `title` (and `status` for the proxy's
502). Every generated function sends its request through
`src/api/transport.ts`, the only code that calls `fetch`. It checks each
response body against its schema. A body that does not match, e.g. after
Go renamed a field without `pnpm generate`, throws `ApiError` with the
title `Unexpected response from the server` and logs the mismatched
fields to the browser console. Extra fields are dropped, not rejected.

Never show Go's text to users: `title`, `detail`, and `errors[].message`
are written for developers. Show `userMessage(err)` from
`src/api/problem.ts`, or a screen's own words for a status it expects,
e.g. a 409. Zod's messages are developer text too: `src/router.tsx`
replaces them app-wide with `src/api/validation.ts`, which states the
field's rule, e.g. "Enter a whole number from 1 to 10.".

## Reading data

Components call the API through generated
[TanStack Query](https://tanstack.com/query) hooks, e.g. `useGetHealth()`.
Query sends the request, caches the result, tracks loading and errors, and
refetches when the browser tab regains focus. A failed read retries only
on 502, 503, or 504, at most twice, since Go may be restarting; any other
error shows at once. Writes never retry. The rule is in `src/router.tsx`.

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
`pnpm generate`, `fieldErrors` from `src/api/problem.ts` marks that field
"This value was not accepted." until it is edited.
