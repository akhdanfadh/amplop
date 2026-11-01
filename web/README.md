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

## Source layout

Keep code for one feature together, including its form schemas and tests.
Put shared code in the folder for its responsibility:

- `src/features/setup`: first-admin form, validation, and installation gate.
- `src/features/auth`: sign-in form, validation, session messages, and account
  cache cleanup.
- `src/features/probe`: the probe demo form.
- `src/features/totp`: authenticator enrollment, confirmation, backup-code
  management, and turning TOTP off.
- `src/features/sessions`: the signed-in browser list and ending sessions.
- `src/features/system-email`: the system email settings form, its schema,
  and the test message form and its result wording.
- `src/components`: shared controls, such as the password input. `ui/` holds
  the shadcn components we own.
- `src/api`: generated client and schemas, transport, shared validation and
  error messages, and retry handling.
- `src/routes`: route registration and page composition.

Keep feature folders flat until they need more structure. File names can omit
what the folder already says, e.g. `features/setup/form.tsx` exports
`SetupForm`. Keep names that identify a particular operation, such as
`features/auth/sign-in-form.tsx`. Tests stay beside the code they test.

## Routes

Each file in `src/routes` is one route. `pnpm dev` and `pnpm build`
regenerate `src/routeTree.gen.ts` from them. That file is committed so
type checking works on a fresh clone; never edit it by hand.

The application checks setup status once per fresh page load and shares the
result during navigation. Before setup is complete, every page redirects to
`/setup`, including direct sign-in, demo, and unknown URLs. The check attempts
the API even when the browser reports offline. Initial status failures block
the page with an error and retry action; a failed recheck keeps entered setup
values and shows the error beside the form.

The `/setup` page shows token configuration instructions or the first-admin
form. See [First administrator setup](../backend/README.md#first-administrator-setup)
for the operator steps. Completing setup updates the shared status and opens
`/sign-in`, with instructions to remove the setup token and restart the server.
Setup does not sign the administrator in. Later visits to `/setup` redirect to
`/`, where the session check chooses home or sign-in.
The browser remembers a local setup attempt until reload, so a status check
that confirms completion after a lost response also shows the instructions.

The `/sign-in` page submits credentials through the generated mutation hook.
Accounts with TOTP then enter six authenticator digits in two groups of three.
"Use a backup code" switches the same control to ten characters in two groups
of five. Pasting ignores spaces and dashes. Switching clears the previous entry.
Codes stay in memory and are released when the form closes.
A rejected code can be retried; an expired or exhausted challenge requires
starting sign-in again. Reloading also returns to the password step.
An uncertain response offers a session check before another attempt.
The `/` page reads the current account and redirects to sign-in on HTTP 401.
Other failures keep a retry action. Administrators without confirmed TOTP open
`/security` to enroll. Members can reach the same page through the Security link.

Security starts enrollment on request and renders the QR code locally, with a
manual setup key as an alternative. A six-digit authenticator code confirms it,
renews this session, and ends other sessions. Backup codes are shown once with
copy and download actions. Leaving before saving them prompts for confirmation.
The secret and codes stay in memory; closing or reloading loses them and may
leave an inactive pending enrollment. Canceling sends a deletion request and
clears the setup key only after success. A rejected cancellation keeps setup
available; an uncertain outcome requires checking status before continuing.
Starting again replaces a pending setup key.
An uncertain confirmation requires checking status before another attempt. If
confirmation succeeded but its response was lost, backup codes cannot be read
again; the page explains that the authenticator must remain available.

Enabled accounts see how many backup codes remain, with a warning at two or
fewer. Replacing codes requires a fresh authenticator code and invalidates
every old backup code; existing sessions stay signed in. The replacement set
has the same one-time display, copy, download, and save acknowledgment as
enrollment. An uncertain replacement blocks another attempt until status is
checked. A count cannot recover lost codes or prove which set is current;
the page directs the user to generate another set with a fresh authenticator
code.

Members can turn TOTP off with a fresh authenticator code or an unused backup
code; the same control switches between them as at sign-in. Turning off removes
the authenticator and every backup code, and sessions stay signed in.
Administrators see that they must keep TOTP instead. If the account became an
administrator after the page loaded, the HTTP 403 refreshes the session and the
page switches to that notice. An HTTP 409 means TOTP is already off, so the
page offers enrollment again. An uncertain turn-off blocks another attempt until
status is checked.

Security also lists the account's signed-in browsers, newest sign-in first.
Each row shows the browser and operating system, or "Unknown browser", the
sign-in time, and the address. Activity is saved at most hourly, so activity
under an hour old reads "Active in the last hour" instead of a time. The
current row has a "This browser" badge and no end button; "Sign out of this
browser" ends it. Other rows sign out with one click. "Sign out of all other
browsers" asks first. A short line says this does not stop someone who knows
the password. Ending sessions is safe to repeat, so every outcome reloads the
list instead of requiring a check. A session the API reports gone reads
"already signed out"; an uncertain outcome asks the user to check the
reloaded list. Ending sessions and TOTP writes wait for each other, because
confirming TOTP replaces this browser's session and ends the others.

Administrators with confirmed TOTP see a Settings link to `/settings`. Each
page passes its session to `AccountNav`, which decides from it. Anyone else
who opens the page sees that settings are for administrators with two-factor
authentication; an administrator also gets a link to Security. A 401 from a
save or test signs out; a 403 reads access again.

The page configures system email, the server inbok sends its own mail
through. The form applies Go's rules between fields before sending, and its
password description says when an empty password keeps the saved one.
Inputs are read-only, not disabled, while a save runs, so focus stays and the
values remain readable. A save whose response is lost locks the form and the
test until "Check saved settings" reads them again and says whether they
match; a typed password is never read back, so it stays unconfirmed. The
username and password inputs carry password-manager opt-outs, so the inbok
sign-in is neither filled in nor saved there.

The test section sends to the signed-in address by default and words each
failure by the step that failed. A lost result can be sent again without a
check. Editing the recipient or a later save hides an earlier result. A save
and a test never run at the same time.

Signing out deletes the current session, clears account caches, and opens
sign-in. A 401 also completes sign-out because that session is already absent
or expired. Other failures stay visible and can be retried. Signing in also
clears account data and mutation variables when the request settles, even if the
sign-in page has closed, so another account cannot inherit them. The public
setup status survives authentication cache cleanup. Account actions wait for a fresh session
check when cached identity is being refreshed. Manual session checks attempt
immediately despite the browser's offline indicator; editing or leaving the
form invalidates their result. The original health
and probe demo remains available at `/probe`.

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
or body-read failures, `timeout` when a local waiting limit expires, `http`
for an error response, or `response` for a complete successful reply that
breaks the expected contract. Deliberate cancellation keeps its original
error; a timeout remains a visible failure.

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
console. For bodyless operations, Orval omits the schema; transport accepts only
HTTP 204 and returns `undefined`. JSON operations still require their promised
body, so an unexpected 204 is a contract mismatch. Error responses keep the same
`ApiError` handling for both kinds of operation.

Error bodies with media type `application/problem+json` use
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
to check before submitting again; ending signed-in browsers reloads the list
for that check. An unreadable success response instead says the server
reported success but its result could not be loaded. A received 4xx
rejection keeps its normal explanation. Failed submissions keep the form's
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
as 422 still prevents retry if reading its body fails. HTTP 500, local timeouts,
complete replies that break the success contract, and deliberately canceled
reads do not retry. HTTP 408 and 504 responses are separate from a local timeout
and remain eligible for retries.
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
waiting limit. Canceling an obsolete query also prevents its pending retries.

Each HTTP attempt has a 15-second timeout covering sending and response body
reading, leaving room around Go's 10-second work limit. Received status and
headers survive a body-read timeout. The transport clears its timer when the
attempt settles. A timeout is recorded as `kind: "timeout"` and stops the read's
retry sequence, even if headers already supplied an otherwise retryable status.
Repeating the same limit does not help a consistently slow response. Reads with
no response show "The request took too long. Try again."; deliberately canceled
requests keep their original behavior.

Query owns retries for eligible failures; each new attempt has a fresh timeout.
The timeout does not bound the complete read operation: earlier attempts,
retry waits, and offline pauses are separate. Later refetches on focus or
reconnection are separate operations too. Writes are attempted once, so a
timeout ends the submission and leaves the form's values available with the
uncertain-outcome message above.

Under `pnpm dev`, a page sends each read twice and cancels the first:
React's development mode mounts every component twice to expose bugs.
The `pnpm build` output sends it once.

## Forms

Forms use [TanStack Form](https://tanstack.com/form), which holds each
field's value, with shadcn's `Field` components for labels and messages.
`src/features/probe/form.tsx` is the example: it sends a write with its
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
