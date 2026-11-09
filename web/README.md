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

A deployed server does not use `pnpm preview`: the Go binary embeds
`dist/client` and serves it. From the repository root, `make backend-build`
builds both; [The web app](../backend/README.md#the-web-app) describes
how Go answers each path. Go caches every file under `assets/` for a year,
so only the build's hashed files may go there; a `public/assets` folder
would be cached the same way and its changes not seen.

### Content-Security-Policy

Go sends the built app with a Content-Security-Policy; its value is in
[Security headers](../backend/README.md#security-headers). The
development server sends none. When a change touches any of these, run
`make backend-build`, start `backend/bin/inbok serve`, and check that the
browser console logs no Content Security Policy violation:

- Scripts: only the build's own files and the shell's inline scripts run.
  No `eval`, `new Function`, or inline event attributes such as `onclick`.
  A `script` in the root route's `head` (`src/routes/__root.tsx`) lands
  in the shell and is allowed automatically; one in any other route's
  `head` is added in the browser and blocked.
- Styles: stylesheets and React `style` props work. Any `<style>`
  element, or a `style` attribute in the shell's HTML, is blocked. A
  library that adds its own `<style>` needs its rules in a stylesheet, as
  `src/components/ui/input-otp.tsx` does for input-otp.
- Images, fonts, and requests: only inbok's own origin. No `data:` URLs,
  no CDNs, and no calls to other sites.
- Framing: no site, inbok included, may show a page in a frame.

Zod's compiled checks need `new Function`, so `src/zod-config.ts` turns
them off with `z.config({ jitless: true })`. Zod reads that setting when a
schema is created, and schemas are created as their modules load, so
`src/router.tsx` imports it first. `vite.config.ts` sets Rolldown's
`strictExecutionOrder` so the bundle keeps that order. input-otp's own
`<style>` is kept out by a workaround explained in
`src/components/ui/input-otp.tsx`.

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
- `src/features/auth`: sign-in form, validation, session messages, the code
  step's rule for when a sign-in has ended, and account cache cleanup.
- `src/features/probe`: the probe demo form.
- `src/features/totp`: authenticator enrollment, confirmation, backup-code
  management, and turning TOTP off.
- `src/features/sessions`: the signed-in browser list and ending sessions.
- `src/features/email-verification`: the Security page's Email section, the
  `/verify-email` form, and their wording.
- `src/features/system-email`: the system email settings form, its schema,
  and the test message form and its result wording.
- `src/features/members`: the member list's columns and cells, the table
  with its confirmation dialog, and its wording.
- `src/features/registration`: the Settings page's Registration section,
  the `/register` and `/register/complete` forms, and their wording.
- `src/features/password-reset`: the `/forgot-password` and
  `/reset-password` forms, the reset form's schema, and their wording.
- `src/features/connected-accounts`: the Connected accounts list with its
  connect, reconnect, and disconnect actions, and the page's wording,
  including each way a connection can end.
- `src/components`: shared components, such as the password input and the
  request error under a form. `ui/` holds the shadcn components we own.
- `src/api`: generated client and schemas, transport, shared validation and
  error messages, and retry handling.
- `src/lib`: code that knows no feature and no React, such as keeping the
  secret of an emailed link, or keeping a server's field error through a
  blur.
- `src/hooks`: React hooks shared by features, such as a page reading its
  link's secret.
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
A deactivated account with the right password sees "This account is
deactivated. Ask your administrator." The code step answers a 403 the same
way as an ended challenge: the form stops and offers to start again.
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

Security opens with the account's email address and whether it is verified.
"Send verification link" emails a link that expires in 24 hours. A lost reply
or failed send can be repeated without a check: the server limits how many
links are sent, and every unexpired link works. Sending and TOTP writes wait
for each other, because confirming TOTP replaces this browser's session. An
unverified account also sees a notice on home that links to Security. Both
say a forgotten password can't be reset by email until the address is
verified, since the reset page's answer cannot say why nothing arrived.

The link opens `/verify-email`, which works signed in or out. The secret after
`#` never reaches the server with the page request. `getRouter` calls
`captureLinkSecret` (`lib/link-secret.ts`) with every page an emailed link
opens, before anything renders or fetches: it keeps the secret in memory and
removes it from the address, so the address bar and a copied URL lose it even
if the setup status check fails, and Back or Forward to the page finds no
link. The browser's history list still holds the full link. Reloading after
that asks for the emailed link again. The page reads its secret with
`useLinkSecret` (`hooks/use-link-secret.ts`), which drops the kept copy from
memory and also removes the secret of a link pasted into the tab later. A
page for a new kind of link exports its path from its feature, adds it to
the list in `getRouter`, and calls the hook.

"Verify email" sends the secret. A link that is expired, used, unknown, or
sent to an old address cannot be retried; if the signed-in account is
already verified, the page says so instead. Any other failure offers
another try. Redeeming needs no session, so a link can verify
an account other than the signed-in one; the page then says the signed-in
account is still not verified.

`/register` asks for an email address while registration is open, and says
the inbok isn't accepting new accounts otherwise. A paused registration,
open but with untested email settings, reads as closed.
Every accepted request gets the same "Check your inbox" answer, so the page
never tells whether an email has an account. "Use a different email
address" brings the form back with the address kept. Sign-in shows a
"Create an account" link only while visitors can register.

A sign-up email's link opens `/register/complete`, whose secret is kept the
same way as `/verify-email`'s. The visitor types the email the link was
sent to and chooses a password, 15 to 128 characters, entered twice. The
server refuses another email on the email field ("Enter the email address
this link was sent to."), so a link someone sends for an address they
control cannot create that account for a visitor typing their own. The
email field is marked `autocomplete="username"`, so a password manager
saves it with the new password. Success creates no session: the page opens
sign-in with the email filled in and a notice naming the new account. The
email travels in history state, data the browser keeps with the history
entry, not in the URL. When the server refuses the email, an "Ask for a
new link" link to `/register` follows the error. A spent or expired link, or
an email that already has an account, ends the page with where to go next.
After a try whose result was lost, a spent link means that try probably
created the account, so the page points to sign-in first and says to sign
in with the password sent then, since the fields may have changed since.

Sign-in's "Forgot password?" link opens `/forgot-password`, which asks for
the email the account signs in with. The API answers before it looks the
address up, so every accepted request gets the same text: if the address is
the verified one of an active account, a link is on its way, can take a few
minutes, and expires in 1 hour. A 429 says when to try again, from
`Retry-After`. A lost answer can be repeated: earlier links keep working,
and the server limits how many are sent. "Use a different email address"
brings the form back with the address kept.

The reset email's link opens `/reset-password`, whose secret is kept the
same way as `/verify-email`'s. Like `/register/complete`, the visitor types
the email the link was sent to, marked `autocomplete="username"`, and a new
password of 15 to 128 characters, entered twice; another email is refused
on the email field, followed by "Ask for a new link" to `/forgot-password`.
Success creates no session: the page opens sign-in in its place with the
email filled in and "The password for <email> is changed. Sign in with your
new password." A reset signs the account out everywhere, so success, or a
result that may have been one, clears this browser's account caches, even
if the page has closed. A spent or expired link ends the page with "Ask for
a new link" and "Sign in"; after a lost result, sign-in comes first, as on
`/register/complete`.

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

Administrators with confirmed TOTP see Members and Settings links to
`/members` and `/settings`. Each page passes its session to `AccountNav`,
which decides from it; a page wider than a form, such as `/members`, also
passes `wide`. Anyone else who opens Settings sees that settings are for
administrators with two-factor authentication; an administrator also gets a
link to Security. A 401 from a save or test signs out; a 403 reads access
again.

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

The Registration section opens and closes public registration with a switch,
which shows the status the server saved, not the click. Each change asks
first: opening says anyone who can reach this inbok can create a member
account, and each adds storage and sync work; closing says unused sign-up
links stop working. Until the system email settings in use pass a test,
opening is blocked: the switch stays focusable but read-only, with a note:
save the settings, then send a test that passes. An open registration whose
settings no longer have a passing test reads paused until one passes. The
section reads its status again after every save that keeps access and
whenever the settings' tested time changes, and says so when a lost response
leaves the status unknown.

Members lists every account, oldest first, in a table: the address, with
the signed-in one marked "You", its status, active or deactivated with the
date, then role, joined date, whether the email is verified, and whether
two-factor authentication is on. Status and the action come right after the
address, so a phone shows them first; the table scrolls sideways in a region
the keyboard can focus. Members have a Deactivate or Reactivate button; the
signed-in administrator and other administrators have none. Both actions ask
first in a dialog, and the request starts once the dialog has closed, so
focus can return to the row's button. The changed row updates from the
server's answer at once, and every outcome rereads the list. A failed reread
keeps the list with "Could not reload members, so the list may be out of
date." and Try again. An unknown outcome asks the user to check the list
before trying again; a 404 or 409 shows the server's explanation. Like
Settings, anyone else who opens the page sees that it is for administrators
with two-factor authentication, a 401 signs out, and a 403 reads access
again.

Every signed-in page links `/connected-accounts`, where members connect
Gmail accounts. The page lists the member's accounts, oldest first, with the
date each was connected. Connect Gmail asks the API for Google's consent
page and sends the tab there; Google returns the browser to the API, which
redirects to this page with `?outcome=`. The page validates it against the
generated `ConnectOutcome`, ignores any other value, removes it from the
address, so a reload or Back does not repeat it, and then shows its message.
An account Google stopped accepting is marked "Needs reconnecting", with the
date, and only it has Reconnect, which runs the same consent flow for that
account, where the member must choose the same Google account. Disconnect
asks first and says inbok removes the account with everything it keeps for
it and asks Google to end its access. The row goes as soon as the server
confirms. When the server has no Google client, the page says to ask an
administrator instead of offering Connect Gmail or Reconnect. A 401 from any
read or write opens sign-in. A refused start rereads what it shows has
changed: the providers on a 409, the list on a 404. A lost answer to a start
just asks for another try, since an unused start expires; a lost answer to a
disconnect asks the member to check the list, which rereads after every
outcome. A failed reread keeps the list with "Could not reload connected
accounts, so the list may be out of date." and Try again. A start goes to
Google only while the page is still open. The browser's back-forward cache
can restore the page locked as it left for Google, so a restored page
unlocks its buttons and rereads the list.

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

`AlertDialogAction`, the confirming button of a dialog that asks first,
ignores the later clicks of a double-click: a dialog can open with that
button under the pointer, and the second click would confirm what the member
never read. Enter and Space still confirm.

Data tables use [TanStack Table](https://tanstack.com/table), which supplies
the rows and columns while shadcn's `Table` components render them. It is
used even for a plain list, so every table shares typed column definitions
and cell data, and sorting or paging can be added without a rewrite. A table
used in one place calls `useTable` directly, with its column definitions and
features at module scope in a `columns.tsx` beside it, as
`src/features/members` does. Values that cells need from the component,
such as handlers, go through the table's typed `meta`. Move to
`createTableHook` once several tables share features or cells. The package
ships agent guides for its installed version in
`node_modules/@tanstack/react-table/skills`.

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
`inbok.ts` holds one function per operation, named by its operation ID,
except operations tagged `Redirects`: another site sends the browser
there, e.g. Google's return after its consent page, and a call from the
app would use up what they carry. `orval.config.ts` leaves them out but
keeps every schema, e.g. `ConnectOutcome`, the outcome Google's return
lands on the `/connected-accounts` page with.
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
and local fallback text if neither is usable. A 429 adds when to try again,
from its `Retry-After` and the response's `receivedAt`, as a local clock time
rounded up to the minute: "Try again after 14:32.", or "Try again after 14:32
on Tuesday." on another day. Without a usable header, or for a wait over
six days, it says "Try again later.". Unfamiliar problem types still show their safe text. Different
business failures can share a status, so use their type when adding
different recovery actions. Type the constant you
compare with the generated `ProblemType`, e.g.
`const setupDoneType: ProblemType = "/api/v0/problems/setup-done"`, so a type
the contract no longer lists fails type-checking. The probe form displays a
received request reference for reporting failures, except under a refusal
its fields show in full.

For writes, a missing response or HTTP 5xx cannot confirm whether the change
took effect. The message keeps any safe server explanation and asks the user
to check before submitting again; ending signed-in browsers, changing a
member, and disconnecting an account reload their list for that check.
Elsewhere, an unreadable success response says the server reported
success but its result could not be loaded. A received 4xx
rejection keeps its normal explanation. Failed submissions keep the form's
values; the request reference remains available when received, except
under a refusal the fields show in full (see Forms).

A feature with its own advice for that case uses the helpers in
`src/api/problem.ts` instead of repeating the rules. `uncertainOutcome(err)`
says whether a write's outcome is unknown; a read's never is, so a failed
query never asks for a check. `problemExplanation(err)` returns the
detail-else-title text, and `uncertainMessage(err, advice)` puts that text
before the feature's advice, e.g. "Check the list before trying again."

Zod's messages are developer text: `src/zod-config.ts`
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
Go answers a request that ran out of its time with 503 `service-busy` and a
`Retry-After` from its own timeout; when that wait is longer than a retry may
take, the read shows its error and retry action at once.

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
A screen reader reads a field's error with the field rather than
announcing each change, so give each `FieldError` an `id` and name it in
the field's `aria-describedby`; an error nothing names is never read.
If Go still rejects a value, e.g. after a rule changed in Go before
`pnpm generate`, `formErrors` from `src/api/problem.ts` attaches the server's
safe messages only to fields that the form renders. Editing a field clears its
server error. TanStack Form also drops it when a check on leaving the field
passes, so every field leaves through `blurKeepingSubmitErrors`
(`src/lib/blur-keeping-submit-errors.ts`), which puts it back. Rejected
inputs outside those fields stay visible in the request summary below the
form, which `formSummary` builds and `RequestError`
(`src/components/request-error.tsx`) shows as an alert, announced as it
appears: the request's message, then those inputs.
When every rejected input shows on a field, the summary is empty and no
request reference shows, since the fields tell the whole refusal. Once a
request fails, focus moves to the first invalid field, else to the submit
button, which lost it while disabled: through `useFocusAfterError`
(`src/hooks/use-focus-after-error.ts`), or by a form's own rules where it
has other targets, such as the code forms' restart button or the system
email form's check button. Request failures without input errors do not enter
form validation state, so valid input can be submitted again without an
edit.
