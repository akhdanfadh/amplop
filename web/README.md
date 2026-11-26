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
  `src/components/ui/input-otp.tsx` does for input-otp. The policy names
  nothing in the web app's code, so it never changes with a library.
  React Aria's iOS scroll lock adds a `<style>` while an overlay is open,
  which the policy blocks, and an iPhone's console logs that violation;
  the rule's effect, `overscroll-behavior: contain`, comes instead from
  `overscroll-contain` on each overlay's scrolling area (the sidebar's
  content, a dialog), so scrolling past its end never moves the page
  behind.
- Images, fonts, and requests: only inbok's own origin. No `data:` URLs,
  no CDNs, and no calls to other sites.
- Framing: no site, inbok included, may show a page of the app in a
  frame. A page may frame inbok's own URLs; the message view frames a
  message body, whose response allows it (see
  [Message view](#message-view)).

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
copy and includes Vitest; `pnpm-workspace.yaml` pins all three, with
Vitest's Playwright browser provider, `@vitest/browser-playwright`, and
the four must change together. To upgrade, change the `vite-plus` version
there, run `pnpm install`, then `pnpm exec vp migrate` to move `vite` and
`vitest` to the versions it includes, and set
`@vitest/browser-playwright` to the same version as `vitest`.

## Source layout

Keep code for one feature together, including its form schemas and tests.
Put shared code in the folder for its responsibility:

- `src/features/setup`: first-admin form, validation, and installation gate.
- `src/features/auth`: sign-in form, validation, session messages, the code
  step's rule for when a sign-in has ended, account cache cleanup, and the
  one rule for a session that ends on an open page (`watchSessionEnd`,
  `useHeldWork`, `SessionEndedBanner`, `SignInAgain`).
- `src/features/totp`: two-factor setup (`useSetupFlow`), backup-code
  replacement, turning it off, and Security's two-factor section.
- `src/features/sessions`: the signed-in browser list and ending sessions.
- `src/features/email-verification`: the Security page's Email section, the
  `/verify-email` page, and their wording. That page, unlike the other link
  pages, lives here whole: its outcome screens read the session and the
  redeem's result.
- `src/features/system-email`: the system email settings form, its schema,
  the test message form and its result wording, and whether the saved
  settings passed a test (`systemEmailState`), which the badge and the
  setup reminder share.
- `src/features/setup-reminder`: the banners for each setup step the
  signed-in member owes (`SetupReminder`), and their wording.
- `src/features/members`: the member list's columns and cells, the table
  with its confirmation dialog, and its wording.
- `src/features/registration`: the Registration page's section and badge,
  the `/register` and `/register/complete` forms, and their wording.
- `src/features/password-reset`: the `/forgot-password` and
  `/reset-password` forms, the reset form's schema, and their wording.
- `src/features/connected-accounts`: the Connected accounts section with
  its connect, rename, check, reconnect, and disconnect actions, the
  banner every signed-in page shows while syncing has stopped, and the
  page's wording, including each way a connection can end and where each
  sync stands.
- `src/features/messages`: the conversation view (`ConversationView`),
  the reading pane's frame (`ReadingPane`) and an open message
  (`OpenMessage`), which the Outbox shares, a message's content, its HTML
  and plain-text bodies, sizing the body's frame, plain-text links, and
  the header's wording.
- `src/features/outgoing-messages`: the compose window, the store that
  owns it (`compose-store.ts`), its form and schema, the reply editor
  with its defaults and schema, the parts both editors share
  (`editor.tsx`: the card, the recipient rows, the send's key), the
  Outbox page, its rows and reading pane, the one read every status
  comes from (`outbox.ts`), one
  message's status with Send again and Edit and send, their wording, and
  when the Outbox reads again (`pollWhile`).
- `src/features/mail`: the mail frame's sidebar, its places and their
  names, the conversation list and its rows, the reading pane and its
  toolbar, mail's TanStack DB collections (`conversations.ts`), what
  actions guess and which places a conversation is in (`rules.ts`), each
  action's words (`words.ts`), running an action on one conversation or
  many (`perform.ts`) and its Undo bar (`undo.tsx`), picking rows, in a
  place's list and a search's results alike (`picking.ts`, whose
  `usePickingGrid` both lists use), and the bar that acts on them
  (`picking-bar.tsx`), the
  member's labels in the sidebar's order and where a move can leave
  (`labels.ts`), More's Move to and Labels pickers (`label-menu.tsx`), the
  reading pane's label chips (`label-chips.tsx`), the New label and Edit
  label dialog (`label-dialog.tsx`), a sidebar label's menu and its
  delete (`label-actions.tsx`), the page of a label gone
  (`label-gone.tsx`), how a row shows its date and people, and the
  address Back to mail returns to.
- `src/features/settings`: the settings sidebar and the settings page
  layout (`SettingsPage`, `SettingsSection`) every settings page uses.
- `src/components`: shared components, such as the shell around every page
  (`AppShell`), the password input, the request error under a form, the
  status badge, the link styles, the signed-in frame and its member menu,
  the banner and the offline banner, the recipient chips input
  (`RecipientInput`), and the pages for loading, failure,
  and Page not found, and `SignedOutPage`, the card every signed-out page
  puts its content in, with `OutcomeLinks` for the buttons of its outcome
  screens. `ui/` holds the shadcn components we own. `form/` is the form
  kit, kept together because its parts only work as one: `useAppForm` with
  the field kinds and the form parts it is built from, and
  `useFormRequest`, which sends a form's values and whose check plugs into
  it; "Forms" below says how to use it.
- `src/api`: generated client and schemas, transport, shared validation and
  error messages, retry handling, the cached read every loader uses
  (`cachedRead`, `cachedLoader`), and the report of every 401
  (`onUnauthorized`).
- `src/lib`: code that knows no feature and no React, such as keeping the
  secret of an emailed link, keeping a server's field error through a
  blur, splitting typed recipients into addresses (`recipients.ts`), or
  writing dates and times (`datetime.ts`).
- `src/hooks`: React hooks shared by features, such as a page reading its
  link's secret, the layout the window's width allows, or a page focusing
  its heading once a closing drawer lets go (`useFocusOnOpen`).
- `src/routes`: route registration and page composition.
- `src/test`: setup for the tests, the fake server (`fakeServer`),
  `appRouter`, the app's router loaded at an address, `stubRouter`, a
  made-up router around one component, and `testQueryClient`, a test's
  query cache.
- `src/styles.css`: the tokens and base styles. Beside it,
  `react-aria-pressable.css` holds a React Aria rule the
  Content-Security-Policy would otherwise block; the file says why.

Keep feature folders flat until they need more structure. File names can omit
what the folder already says, e.g. `features/setup/form.tsx` exports
`SetupForm`. Keep names that identify a particular operation, such as
`features/auth/sign-in-form.tsx`. Tests stay beside the code they test.

## Routes

Each file in `src/routes` is one route. `pnpm dev` and `pnpm build`
regenerate `src/routeTree.gen.ts` from them. That file is committed so
type checking works on a fresh clone; never edit it by hand.

Pages sit under layout routes, whose names start with `_` and add nothing
to the address. Each guard runs in `beforeLoad`, before anything draws,
and reads the session through `readSession` (`features/auth/guard.ts`),
which uses the cached session when a page has read it:

| Layout route           | Pages                                       | Guard                                                                                                                                                 |
| ---------------------- | ------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| `_app`                 | Everything signed in                        | No session: `/sign-in?redirect=<address>`, which returns there after sign-in; an administrator without two-factor authentication: `/two-factor-setup` |
| `_app/_mail`           | `/mail/<place>`, `/mail/outbox`, `/compose` | None; the mail frame (`/compose` opens the compose window and redirects to the Inbox)                                                                 |
| `_app/settings`        | `/settings/<page>`                          | None; the settings frame                                                                                                                              |
| `_app/settings/_admin` | Members, Registration, System email         | A member gets Page not found                                                                                                                          |
| `_auth`                | Signed-out pages                            | None; the wordmark over one column                                                                                                                    |
| `_auth/_signed-out`    | Sign-in, Forgot password, Register          | A signed-in member goes to `/`; a failed check shows the page                                                                                         |

`/mail/search` is the place route's page with "search" as its place: a
search's results in the list's place, the search in its search
parameters, so the search box, drawn by the place's page, stays the same
box, focused, as typing moves the address from a place to its results.
`/` opens `/mail/inbox`. `/two-factor-setup` sits in `_auth` with its own
guard: no session goes to sign-in, a session with two-factor on to `/`.
Pages emailed links open (`/verify-email`,
`/reset-password`, `/register/complete`) keep their addresses and sit in
`_auth` without a guard, so they work signed in or out. The server's
other entry addresses, `/security` (the recovery notice) and
`/connected-accounts` (where Google's return lands, with `?outcome=`),
are routes that only redirect into settings, so the server never follows
the settings' layout; `src/entry-paths.browser.test.tsx` lists every
entry address and fails when one stops being a page. A session can end
after the guard ran, e.g. signed out from another device, so the
session-end rule below answers a 401 wherever it comes from. The query
client counts a session read in the last 5 s as fresh (`sessionFreshMs`),
so a page that opens
just after a guard read it does not read it again.

Every guard and loader reads through `cachedRead`
(`src/api/cached-read.ts`): the cached answer, unless a write marked it
out of date (`invalidateQueries`), and then a fresh read, joining one
already running. Not TanStack Query's `staleTime: "static"`, which serves
a marked answer too. The router waits for a page's loaders when it runs
them again on a visit back (`defaultStaleReloadMode: "blocking"` in
`src/router.tsx`), and runs them again after a preload instead of reusing
its result (`defaultPreloadStaleTime: 0`), so a visit after a write draws
from the fresh answer: Registration never offers a choice a System email
save just changed. A write only marks what it changes; it need not read it
again itself. A refresh that only polls, e.g. mail's every 30 s, reads
again with `refetchQueries` and marks nothing, or the next visit would
wait for it. Besides a read the cache lacks, a visit waits for one a write
marked, about one request; the loading view stays hidden for its first 300
ms, so on a slow connection it may show then (accepted). A visit made
offline gets a marked answer too, under the offline banner, rather than
wait for the connection. Accepted limits: a connection lost during that
one request waits for its return, then draws the fresh answer; a read that
began before the write and is still running when the member comes back is
joined, and its older answer drawn; and a conversation the list holds
opens from its row at once, its messages refreshing in place (DESIGN.md
"States"). The server refuses any change a stale page offers, with its
reason. A page's test gets the same two router options from `appRouter`,
the app's router.

A page whose data it cannot draw without, e.g. System email, Registration,
or Members, reads it in its route's loader, made by `cachedLoader`
(`src/api/cached-read.ts`), so the router's loading and failure views
below cover it; a page with two reads, e.g. Connected accounts, reads
them side by side with `cachedRead` and throws a 401 before any other
failure; mail's loaders check that the address names a label or account
the member has. The page's own query counts what the loader read in the
last 5 s as fresh (`loadedFreshMs`), so it does not read it again as it
opens, and rereads older data then. A 403 on such a page is an ordinary
failure: no administrator loses the role while signed in today, and a
feature that adds a way to, e.g. a role change, decides what an open page
does then.

**When a session ends on an open page.** One rule, in one place
(`watchSessionEnd`, `src/features/auth/session-end.ts`), which the router
starts (`src/router.tsx`). Every request goes through `transport`
(`src/api/transport.ts`), which tells it of each 401 before the caller
sees the failure (`onUnauthorized`): reads, writes, mail's actions, mail's
collections, and loaders alike. It acts only while the router shows a
page that needs a session (`_app`'s pages and `/two-factor-setup`): on a
signed-out page a 401 is that page's own answer, e.g. a wrong password,
and before the first page draws it is the guard's, for a visitor who
never had a session, whom sign-in tells nothing; a page's own read that
finds the session ended then shows `RouteError`'s signed-out view. A signed-in operation answers 401 only
when no session is left (AGENTS.md, "API contract").

- **Nothing to lose:** it clears the member's data (`clearAccountCache`)
  and opens sign-in with `redirect`, the page's address, and history
  state `sessionEnded`, so sign-in says "Your session ended. Sign in to
  continue." and returns to the page afterwards.
- **Something to lose:** a `LeaveWarning` whose `onSessionEnd` is
  `"stay"` is active, e.g. a typed reply or System email being edited, or
  the compose window holds a message (`useHeldWork`). The page stays,
  with its data, and `SessionEndedBanner` says "You're signed out, so
  changes here can't be saved. Copy what you need, then sign in." with a
  Sign in link back to the page, which the leave warning, or the compose
  window, asks about. The banner sits in the frame, above the others, or
  under the compose window's title bar while the window covers the page,
  so exactly one shows. Once nothing is held, e.g. the reply discarded,
  sign-in opens as above. A loader's 401 then shows `RouteError`'s
  signed-out view, "You're signed out", with Sign in, since Try again would
  fail the same way.
- **Backup codes are not held** (`onSessionEnd="leave"`): they work only
  once a signed-in Finish makes them active, so copying them after the
  session ended would keep codes that never work. Sign-in returns to the
  setup, which shows the same codes again within its 15 minutes; a
  replacement's old codes still work.
- A Sign out the member pressed is not a session end: it asks first while
  the compose window holds a message, and opens sign-in without the
  message or the way back.

Pages no longer handle a 401 themselves. A read's 401 is left out of what
a page shows, e.g. its failure alert, the mail list's refresh and
older-page notices, and the Outbox's "may be out of date" line; a write
the member pressed says why it failed, at the button or in an action's
error toast, in the server's words ("Sign in to continue."), as any
failure does. Accepted: sign-in does not say the
session ended after a reload, or when another tab's sign-out reloads this
one, since only the open page sees it end (every 401 has the same problem
type, so a reloaded page cannot tell an ended session from a first visit);
while a page is kept signed out, pages the tab already read still open
from memory, under the banner: a revocation stops new data,
not what this tab holds, and closing the tab ends it (every API answer
is `no-store`, so nothing stays on disk); a tab that ends with nothing
held clears the data every tab shares, which closes another tab's
compose message, and a tab that keeps its page tells no other tab, which
each finds out from its own next read; the listener runs inside the
failing request, before its caller, which is harmless while sign-in's
navigation supersedes the failed load; typing System email's saved values back counts as no change, so
the page moves to sign-in; a slow request from a member who just signed
out could end a new member's page, within the 15 s timeout, which
signing in again takes longer than; and when the session ends just as
the member moves between pages, the guard and the rule may both open
sign-in, and the message shows only if the rule's is last.

Every page gets the same states from the router (`src/router.tsx`):
`RoutePending`, a spinner with "Loading...", when the page's checks take
over 300 ms, then for at least 500 ms so it does not flash. The first
load, the setup check's included, is one loading period: TanStack shows
its view at once, so CSS hides it for the page's first 300 ms and nothing
holds it, and a quick first load shows nothing. `RouteError` when a read
the page needs fails, with Try again, which runs the checks again; and
`NotFound` for an address with no page, or with search parameters its
route refuses, which Reload would only repeat. A search parameter that
only filters a view should fall back instead, e.g. with Zod's `.catch()`,
so an old link still opens the page; a route that refuses a name in its
path throws `notFound()` from its `beforeLoad` or `loader` (thrown while
the page draws, it is a crash). `CrashScreen` offers Reload and the start
page, the way out when a page fails on every load; a `CatchBoundary` in
the root shows it when the code around the pages fails. Each state names
the tab and moves focus to its heading; after Try again the pending view
takes focus, unless focus is on something still shown.

The root (`src/routes/__root.tsx`) fixes React Aria's locale to `en-US`,
so its own words, e.g. a toast's Close, and its direction never follow
the browser's language; sends React Aria's links through the router; and
holds the one toast region. A React Aria link to a page in the app takes
TanStack Router's options, written with its `linkOptions`, e.g.
`href={linkOptions({ to: "/mail/outbox/$id", params: { id } })}`, which
TypeScript checks against the routes, params included; a bare object
misses a param left out. Its `routerOptions`, e.g. `replace`, reach the
router, and a path written as a string is a type error. Only a link that
leaves the app, a `#fragment`, or a file the server sends (`/api/...`) is
a string.

The signed-in pages sit in a frame (`components/frame.tsx`) that fills
the window at every width ([DESIGN.md](DESIGN.md#panes-and-widths)):
from 1280 px the sidebar sits beside the content, and its open or
collapsed state is kept in `localStorage`; from 1024 px it is a rail,
which expands into the drawer over the content; below 1024 px a top bar
opens the drawer. `useLayout` (`hooks/use-layout.ts`) names the layout
for the width. The frames' routes keep their components in the app's
first download (`codeSplitGroupings`), so a page still loading when the
member moves between pages shows its loading view inside the frame; the
first load is one loading view over the window, as above. In mail, the
list grows with the window from 300 px to 440 px beside the reading pane
(`features/mail/panes.tsx`), with no divider to drag; below 780 px one
pane shows at a time. A state in the reading pane, e.g. a conversation
not found, is a section with an `h2` (`StateRegion`), since the list
holds the page's `main`. In the mail sidebar, an account's labels and a
parent label's children fold behind a chevron (React Aria's
`Disclosure`); what is unfolded is a TanStack DB collection in the tab's
`sessionStorage` (`features/mail/folds.ts`), so each tab keeps its own
through reloads, and a tab without folds of its own starts from the
folds as the member last pressed a chevron in any tab, a plain
`localStorage` key; the sidebar also unfolds the way to the open place,
which leaves that key as it was, and the folds clear at sign-out in the
tab that signs out, and in another tab when it next signs in. A name cut
short is offered in full in a tooltip, measured by `useOverflow`
(`hooks/use-overflow.ts`). The sidebar's scrolling content fades at an
edge with more beyond it
(`scroll-fade-y` in `styles.css`, a scroll-driven animation). Where the
sidebar is a drawer, a place's heading, account, and Check for new mail
sit in the top bar instead of above the list: `Frame` gives its top bar
a box (`useTopBarSlot`), and the place page puts its header there with a
React portal, so the heading keeps its focus and the list's `main` keeps
its name. The mail sidebar's Outbox, among the views after Sent and
outside the account filter (DESIGN.md "Sidebar"), shows how many of its messages are
not sent yet, from the Outbox's one read: those the API marks
`outstanding` (waiting or being sent, or failed or unconfirmed and
neither sent again nor dismissed), so a message sent again or dismissed
stops counting. The client keeps no
copy of that rule; it follows the message's facts, not what its page
offers now, so it holds still while accounts load or while an account
can't send. The member menu at the bottom of the sidebar opens
Settings, the theme choice, and Sign out. Both frames and the signed-out
layout show `OfflineBanner` at the top of the content while the browser
reports no connection, read from TanStack Query's `onlineManager`. Under
it, both frames show `SetupReminder`, a banner for each setup step the
member owes, stacked (web/DESIGN.md "Feedback"): for an administrator,
system email with nothing saved or without a passing test; for anyone, an
unverified address. Each follows its own fact from the server, with no
order between them: the server, not the reminder, refuses a verification
link while nothing is saved. The banners sit in a "Setup reminders"
landmark, since they come before the heading each page focuses. The
reminder reads system email for an administrator only, through the System
email page's own query, so that page's saves and tests move it on; a
failed later read keeps the last answer, as that page does. The `_app`
loader makes the page load's first try before the page draws, so the
banner never pushes a drawn page down; it never waits for the connection,
a failure only leaves system email out, and after that first try, answered
or not, it waits for nothing. Accepted: a first read that hangs holds the
first draw until the request times out (15 s), as a page's own read does.
The reminder's focus reread can be the first to find the session gone,
which the session-end rule answers. Unlike the frame's
other session readers, the reminder rereads the session when the window
regains focus, since an address is often verified from the email in
another tab. A banner shows on every page; on
the page where its step is done it drops its link.

A place in mail, `/mail/<place>`, is a view (`inbox`, `starred`, `sent`,
`all`, `spam`, `trash`) or a label's ID from the API's places, and
`?account=<id>` shows one account's mail. A label the member doesn't
have says the label is gone (its loader throws `notFound` with data the
route's `notFoundComponent` tells from an address that is no place,
which stays Page not found). The open label's page follows the member's
labels as List Places reads them: once its name with its parents' changes,
or it leaves them, its loader runs again (`useFollowLabel`), renaming the
page or saying it is gone. On a view `?account=` only filters, so an
account the member doesn't have shows every account's mail, and one that
is no ID is dropped; a label opens in its own account, so any other
`?account=`, or none, becomes the label's. `/mail/<place>/<conversation
id>` opens a conversation in the reading pane: one the list holds draws
from its row at once while its messages load, and one it doesn't, e.g.
from a link, waits for its read; one that no longer exists is Page not
found either way. Its route splits its loader off
(`codeSplitGroupings`), since it reads mail's TanStack DB collection,
which must stay out of the app's first download. A loader whose read
finds the session ended is the session-end rule's (above). Rows are React Aria links,
which never preload, so resting the pointer or focus on a row for 100 ms
calls `router.preloadRoute`, which reads the conversation ahead;
sweeping over rows reads none. On a phone the reading pane is the page,
with Back at the start of the conversation's toolbar, which goes back in
history as the phone's own back does.

The application checks setup status once per fresh page load and shares
the result during navigation. Once it has seen setup done, it keeps that
for the rest of the page load, since done never goes back: a reset of the
query cache cannot bring back the loading view. Before setup is complete, every page
redirects to `/setup`, including direct sign-in and unknown URLs.
The check attempts the API even when the browser reports offline. While it
runs, the page shows the app's first-load view (above); a failed first
check shows the app's failed-read state, whose Try again shows the loading
view at once, as the router's does, checks again, and runs the page's own
checks too. A failed recheck keeps entered setup values and shows the
error beside the form.

The `/setup` page shows token configuration instructions or the
first-admin form. See [First administrator
setup](../backend/README.md#first-administrator-setup) for the operator
steps. Completing setup signs the new administrator in, with a session
that may only set up two-factor authentication, updates the shared status,
and opens `/`, whose guard sends them on to `/two-factor-setup`. That card
shows the instructions to remove the setup token and restart the server;
if the sign-in failed, or its answer was lost, sign-in shows them instead.
Later visits to `/setup` redirect to `/`. The browser remembers a local
setup attempt until reload, so a status check that confirms completion
after a lost response also shows the instructions.

The `/sign-in` page submits credentials through the generated mutation hook.
`/sign-in?redirect=<path>` returns to that path after sign-in, else to `/`;
anything but a path on this site is dropped (`features/auth/search.ts`), so
a crafted link can't send a member elsewhere.
Accounts with TOTP then enter six authenticator digits in two groups of three.
"Use a backup code" switches the same control to ten characters in two groups
of five. Pasting ignores spaces and dashes. Switching clears the previous entry.
Codes stay in memory and are released when the form closes.
A rejected code can be retried; an expired or exhausted challenge requires
starting sign-in again. Reloading also returns to the password step.
A deactivated account with the right password sees "This account is
deactivated. Ask your administrator." The code step answers a 403 the same
way as an ended challenge: the form gives way to an end screen, "This
sign-in has ended", with the server's reason and "Start sign-in again".
An uncertain response offers a session check before another attempt.
An administrator without confirmed TOTP gets a session the server lets only
set it up, for 15 minutes, so `_app` sends them to `/two-factor-setup`, a
signed-out card with the setup and "Not now? Sign out" under it; `/setup`
signs the first administrator in and leads there too. A 401 there, e.g.
when the 15 minutes run out, opens sign-in through the session-end rule,
which says the session ended and leads back to the card.
Members reach two-factor through Security.

Security (`/settings/security`) is a settings page with three sections:
Email address, Two-factor authentication, and Signed-in browsers. The
two-factor row has two states: on, with the "Two-factor on" badge, the
backup codes left, and "Manage two-factor authentication"; or off, with
"Set up two-factor authentication", which also continues an unfinished
setup. Both open `/settings/security/two-factor`, which has a "‹ Security"
link back. Finishing setup, canceling it, and turning two-factor off return
to Security, whose notice says what changed (history state
`twoFactorChange`). Security shows it once: it clears the state from the
history entry, so Back or a reload does not repeat it, and drops a notice
its own status read contradicts. Once the page has shown two-factor on, any
status read that finds it off returns to Security the same way, e.g. after a
turn-off whose answer was lost, or one from another tab. A 401 from any read
or write on the page is the session-end rule's, which returns here after
sign-in.

Setup (`useSetupFlow`, the same steps on the page and on the card) follows
the server's status, so a reload or another tab lands on the right step;
only what the server shows after the password stays in memory:

1. The password, before any secret shows, with what the step needs as the
   field's description; a hidden username field lets a password manager
   offer the right one. Wrong passwords count against sign-in's limits.
2. The QR code, drawn locally, and the setup key in groups of four, then the
   first code from the app. Checking it leaves two-factor off. On a narrow
   page or a touch pointer, "Add to authenticator app" opens the code's
   `otpauth://` address, since a phone cannot scan its own screen.
3. The ten backup codes, with copy and download, then "I've saved these
   codes", which turns two-factor on, signs out the other browsers, and
   renews this browser's session. What describes the session, the session
   itself, its two-factor status, and the browsers signed in, is reset with
   it (`resetSession`), so none of it stays from the old cookie, even when
   the member left meanwhile; the member's other data, mail's included,
   stays.
   While that request runs, every other request waits for it
   (`transport`), so none carries the cookie it replaces.

A reload asks for the password again, then shows the same QR code or the
same codes, for 15 minutes from the start. After that the page says the
setup expired and to remove the inbok entry from the app. Cancel setup sits
on steps 2 and 3 and asks first in an `AlertDialog`, since the app may
already hold the secret it throws away; "I've saved these codes" waits while
it runs. Only step 3 warns before leaving, with `LeaveWarning` ("Leave
before two-factor authentication is on?"), because someone may think it is
on. A conflict or a lost answer rereads the status, which the page then
follows; a code another tab showed again asks for the password here too.
Each step after the first fades in (DESIGN.md "Motion").

While two-factor is on, the page shows the backup codes left and "Replace
backup codes": a fresh authenticator code makes a new set, shown with the
same step 3, whose "I've saved these codes" swaps it in; the old codes work
until then, and its leave warning says so. The new codes show only in that
answer: the server never shows them again while two-factor is on, since
only the authenticator may hand out a new second factor, so codes left
unsaved, e.g. after a reload, mean making another set. Members turn
two-factor off on the page: the red button opens the code field, with "Use
a backup code" as at sign-in, and the code confirms it, so no dialog asks
again; the answer's status takes the page back to Security. A refused
turn-off (403) rereads the session, whose role the page reads live, so a
member made an administrator since sees that administrators must keep it
on.

Security opens with the account's email address and whether it is
verified. "Send verification link" emails a link that expires in 24 hours.
A lost reply or failed send can be repeated without a check: the server
limits how many links are sent, and every unexpired link works. An
unverified account also sees the setup reminder on every page, linking
to Security from the others. Both say a forgotten password can't
be reset by email until the address is verified, since the reset page's
answer cannot say why nothing arrived. "Send verification link" is pending,
not disabled, while it sends, so focus stays on it.

The link opens `/verify-email`, which works signed in or out. The secret after
`#` never reaches the server with the page request. `getRouter` calls
`captureLinkSecret` (`lib/link-secret.ts`) with every page an emailed link
opens, before anything renders or fetches: it keeps the secret in memory and
removes it from the address, so the address bar and a copied URL lose it even
if the setup status check fails, and Back or Forward to the page finds no
link. The browser's history list still holds the full link. Reloading after
that asks for the emailed link again. Leaving a link page, or reloading it,
loses what was typed and this tab's copy of the link, without a warning:
the email's link still opens the page, within its lifetime, and the page
without it says to open it (web/DESIGN.md "Forms"). The page reads its
secret with `useLinkSecret` (`hooks/use-link-secret.ts`), which drops the
kept copy from memory and also removes the secret of a link pasted into
the tab later. A page for a new kind of link exports its path from its
feature, adds it to the list in `getRouter`, and calls the hook.

"Verify email" sends the secret. A link that is expired, used, unknown, or
sent to an old address cannot be retried; if the signed-in account is
already verified, the page says so instead. Any other failure offers
another try. Redeeming needs no session, so a link can verify
an account other than the signed-in one; the page then says the signed-in
account is still not verified. The outcome links lead home (`/`) and to
Security, which both ask a visitor without a session to sign in first, so
they work whoever opened the page; only a 401 from the session read words
them "Sign in", and a failed read words them as for a signed-in member.

`/register` asks for an email address while registration is open, and says
the inbok isn't accepting new accounts otherwise. A paused registration,
open but with untested email settings, reads as closed.
Every accepted request gets the same "Check your email" answer, with the
address on a line of its own, so the page never tells whether an email has
an account. "Use a different email address" brings the form back with the
address kept and focus in the field. Sign-in shows "No account yet? Create
an account" under its card only while visitors can register.

A sign-up email's link opens `/register/complete`, whose secret is kept the
same way as `/verify-email`'s. The visitor types the email the link was
sent to and chooses a password, 15 to 128 characters, entered twice. The
server refuses another email on the email field ("Enter the email address
this link was sent to."), so a link someone sends for an address they
control cannot create that account for a visitor typing their own. The
email field is marked `autocomplete="username"`, so a password manager
saves it with the new password. Success creates no session: the page opens
sign-in with the email filled in and a notice, the address on a line of its
own, then "Your account is created. Sign in with your new password." The
email travels in history state, data the browser keeps with the history
entry, not in the URL. Under the card, "Link sent to another address? Ask
for a new link" leads to `/register` for every visitor, not only after a
refusal. A spent or expired link, or an email that already has an account,
ends the page with where to go next. After a try whose result was lost, a
spent link means that try probably created the account, so the page points
to sign-in first and says to sign in with the password sent then, since the
fields may have changed since.

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
on the email field, and "Link sent to another address? Ask for a new link"
under the card leads to `/forgot-password`. Success creates no session: the
page opens sign-in in its place with the email filled in and a notice, the
address on a line of its own, then "Your password is changed. Sign in with
your new password." A reset signs the account out everywhere, so success,
or a result that may have been one, clears this browser's account caches, even
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
reloaded list. "Sign out of this browser" waits for a send or an ending to
finish.

Administrators with confirmed TOTP see "This inbok" in the settings
sidebar: Members, Registration, and System email (`/settings/members`,
`/settings/registration`, `/settings/system-email`). An administrator
without it never reaches settings: `_app` sends them to
`/two-factor-setup`. A 401 on these pages is the session-end rule's
(above); a 403 is an ordinary failure.

System email configures the server inbok sends its own mail through. It
shows the saved settings as a summary with "Edit settings"; the form opens
in its place and closes on a save (web/DESIGN.md "Settings and signed-out
pages"). The form applies Go's rules between fields before sending, and its
password description says when an empty password keeps the saved one. A
save whose answer is lost keeps the form open and asks to save again: a
save replaces the settings whole, so repeating it is safe. "Save and send
test" saves, then sends a test. The username and password inputs carry
password-manager opt-outs, so the inbok sign-in is neither filled in nor
saved there. A save or a test also marks Registration's status out of date,
since either can pause or resume it.

Its test section sends to the signed-in address by default and words each
failure by the step that failed. A lost result can be sent again without a
check. Editing the recipient or a later save hides an earlier result. A save
and a test never run at the same time.

The Registration page says whether visitors can create accounts, as a
sentence and a badge beside the title (Open, Closed, Paused), and offers
one button for the change that fits: Open registration or Close
registration. The server lists the changes it allows now
(`allowedActions`), and the page offers only those: until the system email
settings in use pass a test, there is no Open, but a note says why and
"Go to System email" leads there. An open registration whose settings no
longer have a passing test reads Paused, with Close and the same link. Each
change asks first in a `ConfirmDialog`, which holds it until the server
answers: opening says anyone who can reach this inbok can create a member
account, and each adds storage and sync work; closing says unused sign-up
links stop working. Every outcome rereads the status.

Members lists every account, oldest first, in a table that fills the
window and scrolls inside its own box, the "Member list" region the
keyboard can focus while it scrolls, a fade marking columns to its
right, with the header and the Member column held (web/DESIGN.md
"Settings and signed-out pages"). Columns: Member (the address, with badges
for You, Admin, and Not verified), Status (Active in grey, or Deactivated
with the date), Two-factor, Joined, and the row's action. A row offers
Deactivate or Reactivate, whichever changes its status, when the member's
`allowedActions` from the server include it; administrators have none.
Each asks first in a `ConfirmDialog` that holds the request: focus returns
to the row's button, now the other action. The changed row updates from
the server's answer at once, and every outcome rereads the list. A failed
reread keeps the list with "Couldn't refresh the member list." and Try
again. A lost answer says trying again is safe: repeating either action
changes nothing the second time.

Settings' Connected accounts (`/settings/connected-accounts`) is where
members connect Gmail accounts. Its loader reads the accounts and the
offered providers side by side, each through `cachedRead`, and the page
then refreshes both in place. It lists the member's accounts, oldest
first, each by its colour dot and the name the member chose for it, else
its address, wrapping at the address's seams (`Address`), with the
address under a chosen name, and one line on where it stands. Each row
offers what its `allowedActions` from the server include, never the
page's own copy of the rule: Check now (`sync`) for an account inbok can
use whose sync is not already moving, Reconnect for one it can't, and
Disconnect. The pencil beside the name (Rename) opens the name form
(`features/connected-accounts/name-form.tsx`, on the form kit) in its
place, under the account's dot and address, so the row still says whose
name it is, with focus in the field: Save sends Rename Account and puts the
server's answer in the accounts list, so the sidebar and every From list
follow; an empty name goes back to the address; Save, Cancel, or Escape
closes it and gives the pencil focus back. Connect Gmail asks the API for
Google's consent page and sends the tab there; Google returns the browser
to the API, which redirects to this page with `?outcome=`. The page
validates it against the generated `ConnectOutcome`, ignores any other
value, removes it from the address, so a reload or Back does not repeat
it, and then shows it in the page's notice under the lead: green when
connected, grey when nothing changed (cancelled, already connected),
red, as an alert, when the member has something to fix. The notice holds
the last action's result until the next action starts.

An account Google stopped accepting is marked "Needs reconnecting", with
the date, and so is one whose saved access inbok can't open, e.g. after a
server key was lost, which the server checks at each read. Reconnect runs
the same consent flow for that account, where the member must choose the
same Google account. Disconnect asks first in a `ConfirmDialog` ("Disconnect
Work"), which holds the request until the server answers: it says inbok
removes the account with everything it saved for it and asks Google to
end its access; for an account whose saved access inbok can't open, it
says inbok can't ask Google, and that the member can remove inbok in
their Google Account. The dialog leaves out that inbok does not ask when
another member here connected the same Google account, whose access
stays. Once the server confirms, the row goes, the notice says "Work is
disconnected.", and focus moves to the next row's pencil, else the
previous row's, else Connect Gmail; where that one is not on the page,
e.g. the last account goes while Gmail isn't offered, to the section's
heading. An account already gone (the API's 404) closes the dialog with "Work was already disconnected."; a failure
or a lost answer shows inside it, where pressing again retries. While
Gmail isn't offered, i.e. the server has no Google client, a sentence
where Connect Gmail would be says so by who can fix it: a member is told
their administrator hasn't set Gmail up and to ask them, an
administrator to add a Google client as the self-hosting guide's
"Connecting Gmail" explains; the server's `allowedActions` hold neither
Check now nor Reconnect. With no account, the section says "Connect your
first account" with Connect Gmail, or "Gmail can't be connected yet"
with only the next step under it ("Ask your administrator to set up
Gmail on this inbok.", or for an administrator, to add a Google client):
the title says what can't happen, so a member doesn't take it for their
own Gmail. A 401 from any read or write is the session-end rule's. A
refused start rereads what it shows has changed: the providers on a 409,
the list on a 404. A lost answer to a start just asks for another try,
since an unused start expires. A failed refresh keeps the list with
"Couldn't refresh this page, so it may be out of date." and Try again,
which rereads whichever of the two reads failed. A
start goes to Google only while the page is still open. The browser's
back-forward cache can restore the page locked as it left for Google, so
a restored page unlocks its buttons and rereads the list.

Each account inbok can use (`status` `usable`) says where its sync
stands on its one line: `sync.ts` picks the first that applies, and
`text.ts` words it, in the member's terms of getting their mail rather
than "sync": "Getting ready to copy your mail."; "Copying your last 90
days of mail into inbok: N messages so far. New mail shows up in the
meantime."; on a failure, "Couldn't get mail from Gmail at" a time, then
"inbok will try again.", or "within an hour" after a failed full sync, or
"Couldn't get mail at" a time "because of a problem on inbok's side. Ask
your administrator.", and on an account that was complete, "Your mail
here is up to date as of" a time; else "Up to date, checked at" a time. A
time shows its date only when it is not today. Check now, or Try again on
a failed account, starts its sync at once; once the server accepts, the
button keeps turning ("Checking...") until the list shows a check or
failure newer than before, or a full sync, while the list is read every 2
seconds for 30, which also ends the wait (`checkLanded`). When the
outcome lands, a hidden polite region in the row says the account's name
and its line once; the 30 s ending without one says nothing. A 429 says
"Work was checked moments ago. Check again in a few seconds." in grey:
the limit is 15 seconds, so DESIGN.md's clock time rounded up to the
minute would overstate it. A 409 asks to reconnect the account first;
every outcome rereads the list. The list is read again every 2 seconds
while a sync is waiting or copying, or after a Check now as above, and
otherwise every minute, so the checked time stays current; an account
inbok cannot use waits for its member, so it does not count. When syncing
has stopped on the instance, every signed-in page shows a warning banner
under the offline one (`SyncStopped`), which reads the list again every
minute on any page, so it follows a stop and a recovery: members are
asked to tell their administrator, an administrator to check that the
worker is running.

Sign out, in the member menu, deletes the current session, then
`clearAccountCache` clears account caches, closes any toasts, and sends
Back to mail to the Inbox, and sign-in opens without the page's leave
warning. While the compose window would lose something, Sign out asks
first, in words for typed text, a send running, or a try that may have
saved (`signOutText`). Every
sign-in, sign-out, and ended session calls `clearAccountCache`, so
whatever else holds a member's data clears there too. A 401 also completes
sign-out because that session is already absent or expired. Other failures
show an error toast, and the member can sign out again. Signing in also
clears account data and mutation variables when the request settles, even if the
sign-in page has closed, so another account cannot inherit them. The public
setup status survives authentication cache cleanup. Account actions wait for a fresh session
check when cached identity is being refreshed. Manual session checks attempt
immediately despite the browser's offline indicator; editing or leaving the
form invalidates their result.

A new message is written in the compose window (DESIGN.md "Compose and
reply"), which `_app`'s layout draws after the page, so it stays while
the member moves between mail and settings. One `ComposeStore` per router
(`compose-store.ts`, in the router's context) owns it: whether it is
open, what it holds, what closing it would lose, and what opened it, for
focus to go back to. It closes whenever one member's data ends
(`onAccountCacheClear`), e.g. on sign-out, so the next member to sign in
in the tab never sees it. The sidebar's Compose calls its `open()`,
which brings back a window holding typed text instead
of replacing it; Edit and send calls `open(message)`, which asks before
replacing typed text. `/compose` is no page: its `beforeLoad` opens the
window and redirects to the Inbox, replacing itself in history.

The width when the window opens picks its form until it closes. Wide, it
is docked bottom right, a landmark region named by its title (the
subject, else "New message") that F6 reaches (React Aria's
`useLandmark`; the frame registers the page as one too, so F6 goes back),
with Minimise, Full screen, and Close in a muted strip;
its title also minimises and restores it, and restoring returns focus to
the field it left. Full screen is a modal dialog over the dimmed page,
which turns `inert`, with focus kept inside (React Aria's `FocusScope`); Escape or pressing the dimmed page docks it again,
never closing it, and Escape does nothing while docked. On a phone it is a
full-screen dialog with Close, the title, and Send at the top, where the
keyboard can't cover Send, and it adds one history entry
(`HistoryState.composeWindow`), so Back closes it and Close steps back.
Leaving that entry, by Back or a link, asks first while it would lose
something, through `useBlocker`, which reads the store at the moment it
is asked; Back, a link, and Close all give focus back the same way. A
window opened wide and then narrowed stays docked, never wider than the
screen. A message replacing another, e.g. by Edit and send, keeps the
window, its layout, and its history entry (`ComposeMessage.window`, a
UUID, so an entry kept through a reload never matches a new window).
Once shown, the editor stays even if a later read finds no account able
to send, as the server will refuse it. Close and Discard ask first with typed text
(`closeText`); closing gives focus back to what opened it, else the
page's heading. Closing or reloading the tab gets the browser's own
question while it holds typed text, is sending, or may have saved.

The form (`ComposeForm`) sends from one of the member's connected
accounts, chosen in From and shown by its address. From starts with none
chosen, so the member picks the address recipients see. An account can send
when its `allowedActions` from the server include `send`, never by the
page's own copy of the rule; one that can't is listed but can't be chosen
(marked when it needs reconnecting), and a member with none that can
gets a link to Connected accounts instead, with why: no account yet, each
needs reconnecting, or, usable as they are, Gmail isn't set up on the
inbok ("Your Gmail accounts can't send yet."), which that page words by
role. Focus starts in the
first field to fill in: From, To, or, for Edit and send, the start of the
message, which fills the window's height (`MessageField size="fill"`). A
401 anywhere, while the window holds a message, keeps it to copy
(session-end rule, above): while the window covers the page, the
signed-out banner shows under its title bar, and leaving for sign-in asks
first.
The form is the message editor, the same rows as Reply's card
(`EditorCard`, `editor.tsx`, whose look "window" lets the window be its
edge): rows for From, To, the Cc and Bcc that are filled or added
from the To row's buttons, and Subject, then Message and a bar with Send,
which Ctrl+Enter, or Cmd+Enter on a Mac, presses from any field. It is on
the form kit, so its errors show after the first Send and focus moves to
the first. To, Cc, and Bcc are the kit's `RecipientsField` rows, grouped
for both editors as `RecipientRows`: each address is a chip
(`RecipientInput`, `src/components/recipient-input.tsx`), on React Aria's
`TagGroup`, and the input after them turns typed text into chips on a
comma, semicolon, or Enter, on leaving, and on pasting a list, keeping
the address of "Name <address>" (`parseRecipients`,
`src/lib/recipients.ts`). Backspace in the empty input removes the last chip;
the chips are one Tab stop with Delete to remove one. An address the
contract refuses stays a chip, marked and named "not an email address",
and its field's error names it. At most 100 per list, with at least one
recipient in all; the form's values are the lists themselves.
Edit and send starts the form with a refused message's content
(`ComposeForm`'s `edit`) and sends it through Send Message Again with a
body, the edits (`editRequest`), instead of Send Message: one operation
links a failed message to whatever replaced it, so the refused one reads
Tried again and leaves the Outbox count. From is then a fixed row
(`FixedRow`, the kit's), as is a reply's subject (`reply`), which the
API keeps; a note above says what Send does (`EditNote`). The reply
editor does the same from a refused reply's status in its conversation;
either way the copy stays in the reply's thread. Both editors run both
send hooks, as hooks must, and the edit picks one; Send again takes no
Idempotency-Key, as the message is its own: a repeat returns its copy,
and other edits after it are refused (`sent-again-otherwise`).

Send saves the message, which the server sends in the background, and
the window then holds its status (`MessageStatus look="window"`): a mark,
"Sending to …" then "Sent to …", the sending account, and Open in Outbox;
a failure names the status, says why, and offers its actions in a bar; a
failed reread keeps the status, says it may be out of date, and offers
Try again.
Focus moves to the status. The window closes 3 s after Gmail takes the
message, waiting while the pointer is on it or focus on one of its
controls, and gives focus back only if focus was inside; it stays open on
a failure. Minimised, its strip shows the status's mark.

Each message the form sends carries an `Idempotency-Key`: a UUID made when
the form opens, sent as a quoted string, and kept for every try of that
message, so a repeated request returns the message the first one saved
instead of saving another. The form therefore retries a lost answer by
itself, and after an answer it never got, sending again is safe. After
edits, the server refuses the key if an earlier try saved the old content;
the form then asks the member to check the Outbox, with a link that opens
it in a new tab so the edits stay, and the next Send uses a new key, as a
new message. A refusal because the account needs
reconnecting rereads the accounts and links Connected accounts.

The Outbox (`outbox-page.tsx`) lists at most 50 of the member's messages
from inbok, beside the reading pane, as a mail place (DESIGN.md
"Outbox"), in two `GridListSection`s in the API's order: "Not sent yet",
the `outstanding` ones, which the API lists first so newer finished ones
never push one out, then "Done"; an empty one is left out. Each opens with a
`GridListSectionHeader`, as tall as `headingHeight`. The Outbox reads
the list again every 2 seconds while one is waiting or sending
(`useOutbox`), and a message changes section with the read that gives
its new status, keeping its key, so the open one stays open and focus
stays on its row. Its rows (`row.tsx`) are mail's list
pieces: `GridList`, `SenderAvatar`, mail's rules for names and dates, and
mail's open store and history state, so a row opens its message in the
reading pane and Back on a phone returns through history as a
conversation's does. That one read gives every status the app shows:
the sidebar's count, the Outbox, the reading pane, the compose window's
status after Send, and a sent reply's (`useOutgoingMessage`), so no two
of them disagree and only one request polls. A message's own read, Get
Outgoing Message, gives its content once; its status counts only for a
message the list doesn't hold, one outside the 50 listed or saved after
the list was read, which that read then follows until its send ends; a
message that leaves the list, past 50 newer ones, is read again then.
Each write that changes a status, Send, Send again, and Dismiss, reads
the list again. A message opens in the reading pane at `/mail/outbox/$id`
(`OutgoingReader`, `reader.tsx`), which reads only an ID that is a UUID,
since the client puts it in the API's path as is. The pane is a
conversation's frame (`ReadingPane`): the subject and the sending
account, then the status in a card (`MessageStatus look="pane"`), which
it announces, first after the pane has rendered, then the message as an
open message (`OpenMessage`). A failed reread, of the list or of the
message, says what is shown may be out of date, with Try again, which
reads again what failed; a failed read stops its own polling until
then. A
failed message says why and what to do, and an `unknown` one says to check
the account's Sent folder in Gmail. Send again copies a failed or unknown
message into a new one from the same account and opens it; for an
`unknown` message it asks first, since the first may have gone out. It is
offered only while the message lists `send-again` in its
`allowedActions`, which the server words from the account's own `send`
and from Gmail being set up. A message is sent again at most once, so its
page then says so and links the copy, and drops the reason's advice and
every action, which would send it twice. A message Gmail refused offers
Edit and send instead, while it lists `send-again`, as Gmail would refuse
it again unchanged: it opens the compose window on its content and sends
it again with the member's edits ("Compose window"). Open Connected
accounts shows
while the sending account's status is one a reconnect fixes, whatever
the message failed for. Dismiss, outlined after them, shows while the
message lists `dismiss` in its `allowedActions`: the API's Dismiss
Outgoing Message, which moves it to Done, as no longer `outstanding`.
A dismissed message reads Dismissed (`dismissedAt`), keeps its reason
and its ways on, since it can still be sent again, and its status rises
anew; focus moves from the gone button to the status, unless the member
moved it elsewhere meanwhile. Its write waits for the list's reread, so
the button stays pending, reading "Dismissing...", until the message
shows dismissed; a lost answer the reread shows done says nothing. One function, `nextSteps`
(`src/features/outgoing-messages/text.ts`), works out both what a
message's page says and what it offers, so the two cannot disagree.

### Reply

A conversation's one reply editor answers a message through Reply to
Message. `ReplyProvider` (`src/features/outgoing-messages/reply.tsx`)
holds it around the conversation: which message it answers, in which
mode, and the last reply sent. The conversation view places its pieces:
`MessageReplyActions` in each open message's header (Reply, and Reply all
when someone else received the message, as icon buttons),
`ReplyEditorSlot` below each message, which shows the editor when it
answers that message, and `ConversationReply` after the last message:
Reply and Reply all for the newest, and the sent reply's status. The
editor is inline on every screen, a phone's too, so an earlier message
stays a scroll away while the member writes. While the editor is open every
Reply hides, so the open editor is never replaced or moved, which would
remount it and lose its text and Idempotency-Key. Each piece takes the
answered message as an `AnsweredMessage`: a message of Get Conversation
(senders and recipients as `{name, address}`, and `sent`, whether its
account sent it: it is in that account's Sent mail) plus the
conversation's `accountId`. It reads only the member's accounts, so the
conversation view passes a message in as it is. `replyDefaults`
(`reply-defaults.ts`) fills the editor, with table tests:

- From is the account that received the message while its
  `allowedActions` include `send`; otherwise none is chosen, and From's
  description names the account that needs reconnecting, or, usable as
  it is, says the accounts can't send yet (`cannotSendNote`).
- Reply goes to Reply-To, else From; Reply all also copies To and Cc. A
  message the account itself sent is answered to its own To, and Reply
  all copies its Cc.
- Reply all leaves out the receiving account's address and repeats,
  comparing addresses in any case and keeping the first spelling. The
  member's other accounts and Gmail aliases stay, since they may want the
  copy.

The editor is DESIGN.md's message editor card: rows for From (a quiet
React Aria `select`, each account by its mark, its address, and the name
the member chose), To, and the Cc and Bcc that are filled or added from
the To row's buttons, each a `RecipientsField`; then Message, and a bar with Send
and Discard. There is no subject: the server sets it. A line under From
says when the chosen account didn't receive the message, since the reply
then starts a new conversation in that account's Gmail. Focus starts in
Message; Ctrl+Enter, or Cmd+Enter on a Mac, sends from any text field, as
Send's tooltip says. It checks, keys, retries, and words its errors as the
compose form does, plus a deleted answered message (404
`message-not-found`), which keeps the text to copy. Discard closes an
untouched editor at once and asks first once something was typed, then
returns focus to Reply. Leaving the page asks first (`LeaveWarning`,
`components/leave-warning.tsx`, on TanStack Router's `useBlocker`): a
link, Back, or Forward opens a dialog, and closing or reloading the tab
gets the browser's own question; while sending, or after a try whose
answer was lost, it says what it means for the reply. A step that keeps
the address asks nothing, e.g. the compose window's history entry on a
phone, which steps past every page's warning (`ignoreBlocker`). An
address typed but not yet a chip counts as typed. The editor keeps the
message it answers until it closes, so a newer message synced meanwhile
never changes what is being written. After Send the editor gives way to the reply's status, from the
Outbox's read, drawn as the compose window's (`MessageStatus` look
"window") in the reply's bordered card; a failed reread keeps the status,
says it may be out of date, and offers Try again. It has Send again and a
link to its Outbox page. A refused reply's Edit and send opens the editor
again with its content and sends the reply again with the member's edits,
from its own account, so the copy keeps its threading and the refused one
reads Tried again. The editor keeps its hidden subject, which the copy
keeps too.

## Message view

`MessageContentView` (`src/features/messages/view.tsx`) shows a message's
content: its bodies one after another, then its attachments, under the
header an open message draws (`OpenMessage`, "Conversation view"). Its
prop:

- `content`: `pending` while the server fetches a body it has not
  stored, `error` with a retry, or `success` with the bodies, whether
  the server blocked remote images in them, and the attachments. A
  plain-text body the server clipped carries `wholeUrl`, which the view
  offers as "Download the whole text"; a message with no text says so.

Give it a `key` per message, so the next message starts afresh. Remote
images stay blocked for another message's bodies even without one: the
view remembers the URLs the member allowed, not a switch.

An HTML body is an iframe whose `src` is the body's URL on inbok's
origin. The server sends it sanitized, with a Content-Security-Policy of
its own ([Showing HTML](../backend/README.md#showing-html)). The frame's
`sandbox` (`frame.ts`) repeats that policy's: no script runs, links open
in a new tab, and the body keeps inbok's origin, so inline images load
with the session cookie and the page can read the body's height. Never
give the frame `allow-scripts`: together with `allow-same-origin`, a
body could remove its own sandbox. Never show a body through `srcdoc` or
`innerHTML`: it would take the app's policy, which refuses the message's
CSS, and in the app's own page its HTML would no longer be isolated.

The frame is as tall as its body, so the page scrolls the message.
`watch` (`frame.ts`) shows the body as soon as its document has been
read, without waiting for its images, whose load event a slow remote
image would hold back. From then on a `ResizeObserver` on the body's
root and on the frame refits the frame whenever either changes size:
images arriving, a `details` element opening, or the frame's width
rewrapping it, including mail that fixes its root's width. Each image
that arrives refits it too, since one whose height the mail fixes can
widen the body and leave the root's box as it was. Mail wider
than the frame is scaled to fit with CSS `zoom` on the body's root, down
to 60%, and then scrolls sideways inside the frame; the width is
measured at full size each time, so mail scaled down returns to full
size when the frame widens. The root scrolls sideways even where the
mail's own CSS hides its overflow. It sets the body's `html` and `body`
to `height: auto` first, so mail that makes them 100% of the frame, plus
padding, fits too. A frame whose document the page cannot read, or that
is not HTML, e.g. an API error, or that is not read within 15 seconds,
shows "Could not show this part of the message." with a retry, which
returns focus to the frame or the button. The frame stays white in dark
mode, since mail is written for a white page.

When the server blocked remote images, a note says so and offers "Show
images", which loads each HTML body again from its second URL, for this
view only. A plain-text body is text, which React escapes, with its line
breaks and spaces kept, in its own direction (`dir="auto"`).
`linkify-react` turns its web and email addresses into links; `links.ts`
allows `http`, `https`, and `mailto` only, opens an address without a
scheme over `https`, and leaves a link with a direction override in it
as text, since its text would read differently from where it goes.

Attachments (`attachments.tsx`) are chips that download their file
(DESIGN.md "Reading pane"). React Aria's router leaves a link with
`download` to the browser, so the part route's address is never taken
for a page of the app. `splitName` (`text.ts`) splits a name into its
start, the characters before its extension, and the extension, which
give way in that order when the chip is too narrow, the extension never.
The chip gives screen readers the whole name in one piece: the visible
parts sit in separate boxes, which Chromium reads with a space between
them.

## Conversation view

`ConversationView` (`src/features/messages/conversation.tsx`) shows one
conversation as DESIGN.md "Reading pane" describes it. Its frame, the
pinned toolbar, the title, the line under it, and any marks under that,
is `ReadingPane`
(`reading-pane.tsx`), and each open message is `OpenMessage`
(`open-message.tsx`): its header, details, and content, with folding,
the message it answers, and its actions as optional props. The Outbox's
reading pane uses both, so an outgoing message reads as a conversation's
does; a sender's name shows inside `<bdi>`, so a direction override in a
name cannot reorder the address after it. Its props:

- `subject` and `account` (ID, name, address, colour) for its title, and
  `messages`, oldest first, as Get Conversation sends them: each
  message's header fields, its preview, whether it is unread, whether the
  account sent it (`sent`, in its Sent mail, from any of its addresses),
  the message it answers (`answers`), and whether it is known to have
  attachments (`attachments`, a paperclip on its folded row).
  `messages` is undefined until they are read, and `placeholder` shows in
  their place, under the title; the messages' `Thread` mounts once they
  come, so which messages start open is decided from them.
- `back`: the way back to the list at the toolbar's start, where one pane
  shows at a time; `actions`: the conversation's actions at its end.
- `marks`: where the conversation is, e.g. its label chips
  (`mail/label-chips.tsx`), under the account line.
- `messageActions(message)`: a message's other actions in its header,
  after Reply; shown only while the message is open.

The view holds the reply editor's `ReplyProvider` and places its pieces
("Reply"): Reply in each open message's header, the editor below the
message it answers, whose row then stays out of "N more messages", and
the bottom's Reply after the last message. An open message that answers
one other than the row just above says so, "In reply to Ana Lima on 2 Nov
2026, 9:00 am" (the name in a `<bdi>`), a link that opens that
message (out of "N more messages" if it was there), scrolls it to the
pane's top, focuses the message itself (its `<article>`, `tabIndex`
-1), and flashes it (`flash-selection` in `styles.css`). The view's
`actions` sit in React Aria's `Toolbar`, one Tab stop with arrow keys
between its buttons; the bar pins under the pane's top only while it is
20em wide or more, and its scroll padding goes with the pin. The view
takes focus when it opens and scrolls itself, so give it a height, and a
`key` per conversation, so folding starts afresh; its scroll padding follows the
pinned toolbar's height (a `ResizeObserver`), so nothing focused or
scrolled to sits under it. Each open message loads its content through
`useMessageContent` (`content.ts`), so folded messages cost no request;
content never goes
stale, as a message's never changes, so it is not read again in the
background; the content shows in
`MessageContentView`, the message view's part below its header.
`toContent` keeps only bodies and files at the API's own addresses.

`foldRows` (`folding.ts`) decides the rows: open, folded, or counted in
"N more messages", which gathers only messages the member has not opened
or folded. A message being answered keeps its row, and if sync removes
it, the view keeps a row under its key, so React keeps the editor and
its text. Opening, folding, and showing the folded middle move
focus to the control that took the place of the one pressed, so focus is
never lost. The subject joins the toolbar when the title passes under
it, which an `IntersectionObserver` on the view's own scroll box tells.
The layout follows the pane, not the window, with container queries
(`@container`, `@lg:`, 512 px at the default text size).

It shares the mail list's pieces: names, initials, and the Reply-To check
(`mail/people.ts`), the sender's avatar (`mail/sender-avatar.tsx`), dates
and `useToday` (`mail/dates.ts`), and words such as `subjectText` and
"Unknown sender" (`mail/text.ts`). The mail's `Reader`
(`mail/reader.tsx`) puts it in the reading pane: it reads Get
Conversation and takes the subject and account from the list's row, not
as the query's placeholder, which the query drops once a read fails, so
the frame stays while the messages load or a read fails, with Try again
under the title. It names the tab, shows Page not found only while
nothing is shown (a 404 on a later read leaves a reply being typed in
place), and says when the accounts could not be read. It reads the conversation's row by ID from the mail
collection, so an action anywhere shows at once, and marks an unread
conversation read once its messages are read and the tab shows. Its
toolbar (`mail/toolbar.tsx`) offers what `actionsFor` (`mail/rules.ts`)
gives for the conversation in the list it was opened from, and More's
Move to and Labels, which open their picker under More
(`mail/label-menu.tsx`), each run by `perform` (`mail/perform.ts`),
which takes an action or changes as they are, e.g. a move to a label
with its words, shows its Undo in the Undo bar (`mail/undo.tsx`), and
says why a refused one failed. Under the title, the label chips
(`mail/label-chips.tsx`) run Archive from the Inbox's and take a label
off from a label's. The labels the pane reads come from the
mail it already holds (`useLabelsIn`), so a pane that outlives the
member's mail, e.g. as sign-in opens, starts no reads. An action that
takes the conversation out of that list (`leavesList`) goes back to it,
through history when a row of it opened the conversation (the row's
link sets `fromList` in its history state, or keeps the flag of the
open conversation's entry it replaces), else to the list in its place, and asks the list, through `mail/focus-request.ts`, to focus the
row that took its place, or the conversation's own row after Mark
unread. That rule is `useAct` (`mail/act.ts`), which the list and a
search's results run too for a hovered row's actions (`RowActions` in
`mail/row.tsx`): the open row's as the toolbar's, another row's taking
focus to the row that took its place at once. The toolbar, the picking
bar, and the row lay out their buttons by one slots module
(`mail/slots.ts`), and a list drops its presses for a double click's
time after a row's action (`mail/press-guard.ts`), so a double press
acts once.

A conversation takes actions only while its account's `allowedActions`
from the server include `change-mail` (`changersOf` in `mail/rules.ts`),
never by the page's own copy of the rule. `actionsFor` and `targetsOf`
leave the others out, so the toolbar, the picking bar, a hovered row,
Move to, and Labels follow from them, as does marking read on open; the
label chips drop their ×, a row's star becomes a plain mark, and More
shows only while it holds something. A conversation refused as
`needs-reconnect` reads the accounts again (`send` in `mail/actions.ts`),
so an account that broke while the page was open loses its actions and
the sidebar offers Reconnect.

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

The project uses TypeScript 7. If your VS Code still checks types with
TypeScript 6, install the `TypeScriptTeam.native-preview` extension.

Tests use [Vitest](https://vitest.dev), included in Vite+. Import its
functions from `vite-plus/test`, not `vitest`; `pnpm check` flags the
latter. They run in two Vitest projects set up in `vite.config.ts`:

- `unit`: files named `*.test.ts`, run in Node, for code without a page.
- `browser`: files named `*.browser.test.tsx`, run in a real Chromium that
  [Playwright](https://playwright.dev) drives ([Vitest browser
  mode](https://vitest.dev/guide/browser/)), for components and anything
  that needs focus, pointer events, layout, or storage.

The browser project needs Playwright's Chromium, once per machine and
again after a Playwright upgrade. From `web`:

```sh
pnpm exec playwright install chromium
```

Run every test, one project, or one file:

```sh
pnpm test
pnpm exec vp test run --project browser
pnpm exec vp test run src/lib/theme.browser.test.tsx
```

`make check` runs both projects, headless.

### A browser test

A browser test renders with `render` from `vitest-browser-react`, finds
elements with `page`, and interacts with `userEvent` from
`vite-plus/test/browser/context`, which send real browser input.
`src/test/browser-setup.ts` loads the app's stylesheet first, so
components lay out, hide, and show as they do in the app, and then the
app's font: it is declared `font-display: swap`, so text would otherwise
draw in a fallback font first and change width when the font arrives,
part-way through a test that measures.

Every browser test starts 1440 px wide and 900 high (`defaultViewport`
in `src/test/viewport.ts`), the wide layout with the sidebar beside the
content: the browser project's `browser.viewport` opens the page at that
size and `browser-setup.ts` sets it again before each test. A test about
another width sets it with `page.viewport`, with the numbers it means,
and puts nothing back afterwards.

React Aria's Virtualizer stops virtualizing, and keeps lists at their
top, when `NODE_ENV` is `test`, as Vitest sets it, unless `VIRT_ON` is
set; the browser project sets it (`vite.config.ts`), so a list test sees
the list as the app has it. Scroll a virtualized list with the keyboard,
e.g. End, which React Aria scrolls itself.

Message bodies for the message view's frame come from
`src/test/message-server.ts`, which serves them on the test page's
origin with the headers the Go server's body route sends.

### Which harness a test uses

No test declares an app route again: its id, path, guard, loader, or a
component taken from `src/routes`. A page's test opens the page through
`appRouter` (`src/test/app-router.ts`), the app's own router, and fakes
only the server. A component's test renders the component alone, inside
`stubRouter` when it needs a router around it. Only a test of the
router's own machinery (`route-states`, the setup gate, the root's React
Aria bridge) makes up routes.

`appRouter(address, server)` is the app's router, built by `buildRouter`
in `src/router.tsx` as `getRouter` builds the app's, loaded at the
address; the test renders it in a `RouterProvider`. It differs from the
app's in three ways: its history is in memory, at the address the test
gives; a failed read is not tried again, which would wait 1 s and then
2 s (`src/api/retry.test.ts` holds those rules); and the test mounts it
in a `<div>`, not on the document as the app does. So it renders the
root route's `AppShell` without the `<html>` and `<body>` around it:
React's own `<body>` there makes a later event loop forever
([react#35480](https://github.com/react/react/issues/35480)), and a
hanging test never fails by itself, since its timer runs on the frozen
page. `src/test/app-router.browser.test.tsx` checks that React leaves the
page's `<body>` alone; `make` stops the web tests after five minutes.

A component that needs a router around it, for a link, a navigation, or
a leave warning, gets one from `stubRouter` (`src/test/stub-router.tsx`).
It renders the component as the page at an address, beside blank pages
to leave to, each only a heading with its path, with an optional layout
around them, and returns the router, the query client, and the screen:

```tsx
const { router } = await stubRouter(<ComposeForm {...props} />, {
  pages: ["/elsewhere"],
});
await router.navigate({ href: "/elsewhere" });
await expect.element(page.getByRole("heading", { name: "/elsewhere" })).toBeVisible();
```

The stub holds nothing of the app's routes: no guard, loader, search
rule, or root component.

A component that needs no router gets none: `render`, inside a
`QueryClientProvider` when it reads the API. With or without the stub,
the query client is `testQueryClient()` (`src/test/query-client.ts`),
which does not try a failed read again; `stubRouter` makes one unless
the test passes its own as `client`. Only the transport's own tests
(`src/api/retry.test.ts`, `timeout.test.ts`, `offline.test.ts`) build a
`QueryClient` themselves, from the options they test.

### Faking the API

A test fakes the API by faking `fetch`, with the fake server in
`src/test/server.ts`. Only the transport's tests named above and
`src/api/transport.test.ts` stub `fetch` by hand.

```ts
const server = fakeServer(memberSession);
server.on("GET", "/api/v0/places", () => data({ places: [] }));
server.on("POST", "/api/v0/connected-accounts/{id}/sync", ({ params }) =>
  params.id === "2"
    ? problem(429, "/api/v0/problems/sync-throttled", { retryAfter: 30 })
    : data(sync),
);
const router = await appRouter("/mail/inbox", server);
```

- `on` answers a method at a path from then on. The path is the
  contract's, with its `{id}` parameters, and its type comes from
  `api/generated/openapi.json`, so a path or method the API does not have
  is a type error. Of two routes that match, the one with fewer
  parameters answers, then the later one, so a test replaces a route by
  adding it again. To answer once, register with `once`: the earlier
  route answers again afterwards.
- A handler gets the request (`url`, `params`, the parsed JSON `body`,
  `headers`) and returns a `Response`, built with `data(value, status)`
  for the API's `{ data }` or `problem(status, type, more)` for Problem
  Details with the required members, `Retry-After` from `retryAfter`. Its
  second argument, `earlier`, answers as the route it replaced does, e.g.
  after a wait.
- To hold an answer, the handler awaits the promise of `held()`, which
  the test resolves: `const answer = held()`, `await answer.promise` in
  the handler, then `answer.resolve()`. It stands in for
  `Promise.withResolvers()`, a type error under `tsconfig.json`'s `lib`,
  which also types the app's code. To lose the answer after the server
  acted, as a dropped connection does, the handler ends with
  `return lose()`, and `fetch` rejects.
- The server answers as a quiet inbok, one with nothing to report: setup
  is done, the session read answers its `session`, the member has no
  connected account and syncing runs, system email is saved and tested
  (`testedSystemEmail`), and registration is closed. Those are the reads
  the signed-in frame and sign-in make on their own (the sync banner, an
  administrator's setup reminder, sign-in's registration link), so a test
  adds only its page's routes; its own route for one of them replaces the
  quiet answer. `memberSession` and `adminSession` are the two sessions;
  `fakeServer()` without one is a visitor's server.
  `server.session = undefined` ends the session: every operation that
  needs one then answers a 401 `unauthenticated`.
- `server.requests` holds every request (method, path with search, body,
  headers). A request no route knows answers a 404, is kept in
  `server.unknown`, and fails its test: after each test
  `src/test/setup.ts` calls `expectAllAnswered()`, which throws
  "Requests without an answer: GET /api/v0/..." for the servers installed
  during the test. Give the fake the honest answer; a test about a failed
  read registers a route that answers the failure.
- `appRouter` installs its server; another test calls `server.install()`,
  which puts the server in `fetch`'s place.

Mail pages use `mailApi` (`src/test/mail-api.ts`), which holds
conversations, accounts, and places and answers mail's operations as the
server does, on a fake server of its own: `appRouter(address,
api.server)`, and `api.server.on(...)` for a route of the test's own.
Its typed factories build the values several test files need, each
taking what varies and the fields to override: `conversation(n)`,
`listedAccount(id, email)`, `message(row)` (the one message of a
conversation), and `outgoingMessage()`. `api.messages[row.id] =
[message(row)]` gives a conversation's read its messages; a message's
content answers as `noContent`, nothing to show, unless the test adds
its own route.

Without a fake server installed, a browser test's `fetch` refuses a
request for `/api/` as a lost connection, so no test reaches a real
server through the dev proxy. Such a request comes from work a test left
running, e.g. a navigation whose loader had yet to run when the test
ended.

### Setup and cleanup

Cleanup is central. Before each test Vitest puts back every stubbed
global and every spied method (`unstubGlobals` and `restoreMocks` in
`vite.config.ts`), and after each test `src/test/setup.ts` puts back the
real timers and checks the fake server's unanswered requests. So a test
file calls none of `vi.unstubAllGlobals()`,
`vi.restoreAllMocks()`, or `vi.useRealTimers()`, and makes its stubs and
spies in the test or a `beforeEach`: one made once for the file lasts
only until its first test. The three exceptions put the real thing back
before their test ends, because something reads it before the next
test: `src/lib/local-store.browser.test.tsx` (the cleanup below clears
the real `Storage`), `src/root.browser.test.tsx` (React Aria rereads the
browser's language), and `src/features/mail/folds.browser.test.tsx`
(the page gets its real `sessionStorage` back before the test goes on).

After each browser test `src/test/browser-setup.ts` also, in this order:
unmounts what the test rendered; ends the member's data in every query
client the harness made, as the end of a session does
(`clearTabAccountData`: mail's collections, the undo bar, the folds),
and cancels the reads still running (`endQueryClients` in
`src/test/query-client.ts`); closes the toasts; sets the connection
back online; and clears `localStorage` and `sessionStorage`. The harness
knows the clients `testQueryClient()` made and the client of
`appRouter`'s router. So a test file adds no cleanup for rendering,
query clients, toasts, the connection, or storage, and its own
`afterEach` holds only what no other file needs, e.g. a class it put on
`<html>` or its own module's state. Vitest runs the hooks after a test
in reverse order of registration: the file's own, then
`browser-setup.ts`'s, then `setup.ts`'s, so the page is gone and its
reads are cancelled before the unanswered requests are checked.

### Time

A test never waits out one of the app's timers in real time: a poll, a
delay, a press guard, a timeout. It fakes the clock with `fakeClock()`
(`src/test/clock.ts`) and moves it by the app's own constant, never a
copied number:

```ts
const clock = fakeClock();
const reads = serve({ outbox: [[sending], [sent]], message: [sending] });
const { result } = await read();
await expect.poll(() => result.current.message?.status).toBe("sending");

await clock.advance(sendPollMs);
await expect.poll(() => result.current.message?.status).toBe("sent");
// Nothing is read again once the send ended.
await clock.advance(sendPollMs);
expect(reads.outbox).toBe(2);
```

`fakeClock` fakes `setTimeout`, `setInterval`, and `Date` together, for
a browser test or a unit test, and leaves frames
(`requestAnimationFrame`), `performance.now`, and microtasks real, since
React and React Aria draw on those. Time then stands still until the
test moves it. `clock.advance(ms)` runs each timer that comes due, in
order, and returns once the page has drawn what they changed. The setup
puts the real clock back after each test. Only two kinds of test call
`vi.useFakeTimers` themselves: the transport's unit tests
(`src/api/retry.test.ts`, `retry-after.test.ts`, `timeout.test.ts`,
`offline.test.ts`), and `src/features/messages/frame.browser.test.tsx`,
which fakes only the date, with its reason beside the call.

A wait that proves nothing happened moves the clock past where it would
have happened, then checks with a plain `expect`, as the last two lines
above do. It never sleeps: a sleep as long as the app's own window comes
up short on a busy machine.

What a test author must know about a fake clock in browser mode
(`clock.ts` says how each was checked):

- Call `fakeClock()` before the test renders. A timer the app set on the
  real clock stays real.
- Vitest's own waits run on the real clock: a retrying assertion still
  retries, and a hanging test still times out.
- `expect.element` and `expect.poll` leave the clock alone when they
  pass at the first look. Each failed look moves it: `expect.poll` by
  50 ms, `expect.element` by 0, 20, 50, 100, 100, then 500 ms a look
  while its element is missing. So while an assertion waits, fake time
  runs about as fast as real time, in steps of up to 500 ms.
  `vi.waitFor` moves it 50 ms before every look, the first too.
- An action on an element (`click`, `fill`, `hover`) moves the clock
  50 ms for each 50 ms of real time it takes: usually nothing, more on
  a busy machine. `userEvent.keyboard`, `userEvent.tab`,
  `page.viewport`, and `locator.elements()` never move it. A check that
  something has not happened yet, e.g. no tooltip at 499 ms, holds only
  with nothing but those between the clock's moves; `pointTo`
  (`src/test/pointer.ts`) hovers without a round trip for that.
- TanStack Query hands a result to its components through
  `setTimeout(0)`, so under a fake clock an answer, or the page a click
  opens, shows only once the clock moves, by any amount. A retrying
  assertion does that by itself. So wait for what an action shows before
  moving the clock past the next timer.
- A poll that comes due while its read is still running joins that read.
  Between two polls, wait for the first one's answer: what it shows, or
  `client.isFetching()` back at 0.
- `advance` gives the page a turn after each timer it runs, so a long
  move across a short interval is slow: 16 s across a 100 ms poll took
  0.6 s.
- The transport gives up on a request after 15 s of the fake clock, so
  an answer held across a longer move fails as a timeout. A fake
  server's handler that waits with `setTimeout` waits on the fake clock.

One app timer reads a clock `fakeClock` leaves real: an alert dialog's
confirming button ignores a mouse or touch press within a double-click
(`doubleClickTime`) of the dialog opening, by `performance.now`. A test
gets past it with `pressInDialog("Discard")` (`src/test/dialog.ts`),
which waits for the button named exactly that in the open alert dialog,
focuses it, and presses Enter: a keyboard press counts at once. No test
sleeps through the guard or clicks that button, except the guard's own
tests (`src/components/ui/alert-dialog.browser.test.tsx`), which set
`performance.now` themselves.

A CSS transition or animation is real time too. A test waits for its
end by what React Aria marks, e.g. nothing `data-entering` before a
size is read, and a position that moves is read inside a retried
assertion. Escape closes an open tooltip first, and the pointer rests
where the last click was, so a test that closes a drawer or dialog it
opened by a click presses its button, or moves the pointer away first.

### What a test asserts

A test checks what a member sees or what the server got, so it still
passes after a change no member could notice.

- A test finds an element the way a member would: by role and name, by
  label, or by text. `querySelector`, `data-slot`, a class, or
  `.parentElement` needs its reason in a comment, e.g. the element is
  inside a message's frame, is a `<mark>` or a `<bdi>`, or has no role.
- A test reads the query cache, a collection, or a hook's result only
  where no screen shows the same fact, and says so in a comment.
- A request count is asserted only where the request is the outcome: a
  write the server must get once, an `Idempotency-Key`, a read that must
  not repeat. The test names the README rule it protects when there is
  one.
- An exact pixel value names the `DESIGN.md` rule it comes from, in a
  comment above it, e.g. `// DESIGN.md "Panes and widths": the sidebar
  is 240 px, its rail 56 px.` A number with no such rule becomes a
  relation: inside, left of, not wider than, as far above the Undo bar
  as above the window's edge.
- A class name, or a `data-*` state a library sets for its own use, is
  asserted only when it is inbok's own public styling hook and nothing
  visible says the same. The theme's `dark` class on the page is such a
  hook; React Aria's `data-hovered` beside a style that shows the hover
  is not.
- A test's name says the behaviour, not the mechanism: "shows the focus
  ring after the keyboard, not after a click", not "sets
  data-focus-visible".

### Where tests live

Tests sit next to the code they test. A page's test sits in its
feature's folder, e.g. `features/members/page.browser.test.tsx`, or
`features/password-reset/pages.browser.test.tsx` for a feature's several
pages. Tests of the whole app sit in `src`: `entry-paths` (the addresses
the server links), `session-end` (a session that ends on an open page),
`guards` (the route table's guards), `signed-out-pages` (what every
signed-out page does), and `router` and `root` (the app router's options
and the root route). The harness's own tests sit in `src/test`.

## Styling and components

How screens look, are laid out, and are worded (tokens, components,
layout, patterns, wording) is in [DESIGN.md](DESIGN.md); read it before
changing any screen.

Styling uses [Tailwind CSS](https://tailwindcss.com): utility classes in
`className`, generated from `src/styles.css`. `pnpm fix` sorts them in
Tailwind's recommended order. For class autocomplete, install the
Tailwind CSS IntelliSense extension (VS Code: `bradlc.vscode-tailwindcss`).

UI components come from [shadcn/ui](https://ui.shadcn.com)'s React Aria
flavour (`aria-nova` in `components.json`): each is a styled
[React Aria Components](https://react-aria.adobe.com) part. Its CLI copies
each component's source into `src/components/ui`, where it becomes our
code to edit; there is no component library to upgrade. The theme (tokens,
fonts, dark mode) is in `src/styles.css`. From `web`, add a component with
the pinned CLI, then format it:

```sh
pnpm exec shadcn add dialog
pnpm fix
```

After adding one, read it: some aria-nova files lag React Aria. Ours differ
from the registry here:

- `checkbox`, `radio-group`: built on React Aria's
  `CheckboxField`/`CheckboxButton` (and the Radio pair), which replace the
  deprecated `Checkbox` and `Radio`. The label is the component's children.
  `switch`, built the same way, left with its last caller (Registration's
  old switch); its first new caller restores it from the file's history
  (`git log -- web/src/components/ui/switch.tsx`).
- `alert-dialog`: one `AlertDialog` holds the overlay and the dialog (no
  `AlertDialogContent`); `AlertDialogDescription` is React Aria's
  description slot, so the dialog is described by it; Cancel takes focus.
- `table`, `native-select`, `input-otp`: plain HTML (the registry's
  `table` is React Aria's interactive grid, wrong for read-only rows).
- The shell's components each say how they differ in a `DESIGN:`
  paragraph on their main part; read it before `shadcn add --overwrite`,
  which would bring the registry's version back. In short:
  - `sidebar`: one left sidebar that collapses to a rail; the window's
    layout (`useLayout`), not CSS breakpoints, picks sidebar, rail, or
    drawer; in the rail layout the rail expands into the drawer, over the
    content; its owner stores the open state (no cookie). `Sidebar` is a
    named `nav`, its menus are lists, `SidebarInset` is a `div`. No
    variants, `SidebarRail`, `SidebarInput`, `SidebarGroupAction`, or
    `sm`/`lg` sizes. `useSidebar()` gives the app `rail`, `isDrawer`, and
    `dismiss()`, which closes the drawer. The drawer also closes when the
    window's layout moves it, and a layout change that takes away the
    focused part, e.g. the open drawer or the top bar's button, gives
    focus to the sidebar's button shown now.
  - `sheet`: DESIGN.md's dialog look and timing; a side sheet's width
    comes from `--sheet-width`.
  - `popover`: holds a dialog, so it can mix links, buttons, and a toggle
    group, which a menu can't. `toggle-group`: a segmented control (the
    theme choice); every item needs an `id`; its keys are React Aria's
    toolbar keys (the group is one Tab stop, and arrows move between
    items without choosing), though a one-of-several group is announced
    as radios. React Aria's `RadioGroup` has the radio keys but takes no
    tooltips, which icon-only items need.
  - `tooltip`: the trigger is a React Aria button or link as is. It shows
    after the pointer rests 500 ms, then the next ones at once (React
    Aria's warm-up), and at once on keyboard focus. A tooltip holds no
    words found nowhere else: inside a dialog or drawer, assistive
    technology never reads it. Its `aria-describedby` repeats an icon
    button's name, which is React Aria's doing.
  - `skeleton` does not pulse. `spinner` is decoration.
- `dropdown-menu`: React Aria's `Menu` in a `Popover`; its items are
  44 px on a touch screen (`pointer-coarse:min-h-11`), and it opens below
  its trigger, at its start, unless `placement` says otherwise.
- `grid-list` is ours: the message list, React Aria's `GridList` in its
  `Virtualizer`, rows of one height, type-to-find off. `GridListItem`
  takes `current` for the open row (`aria-current`, which React Aria
  passes on only through its `render` prop), whose open surface stays
  under the pointer; `GridListEnd` is the line under the last row.
- `avatar`: as the registry's; the conversation row rings it in the
  account colour. `empty`: words only.
- `toast` is ours: the registry offers only `sonner` (see DESIGN.md).
  It shows failures only; an action's Undo is `features/mail/undo.tsx`'s
  `UndoBar`, docked under the mail panes, which `showUndo` fills and
  `closeUndo` empties: a landmark F6 reaches (React Aria's
  `useLandmark`, from `react-aria`), whose Undo also runs on Ctrl or
  Cmd+Z, by `isShortcut` (`src/lib/shortcut.ts`), the check the
  sidebar's Ctrl+B shares. While toasts show, `--toast-space` holds the
  height they take, which a
  scrolling box uses as `scroll-pb-(--toast-space)`, so focus never lands
  under a toast (the list and the reading pane do);
  `showError` shows a failure until the member closes it, and not again
  while it shows; `closeToasts` closes all, e.g. on sign-out. It is React
  Aria's alpha toast (`UNSTABLE_`), so recheck it after an upgrade.
- No `drawer`: shadcn's `drawer` is a swipeable panel, usually from the
  bottom, and the React Aria registry's still builds it on Base UI, a
  second component library. The sidebar's drawer is shadcn's `sheet`, a
  side panel on React Aria's `ModalOverlay`. React Aria's own `Sheet`
  (1.22) adds swiping, but is new and built on scroll-driven animations,
  which not every browser has yet.
- Enter and exit animations use DESIGN.md's easing; with reduced motion,
  `src/styles.css` keeps their fades and drops their movement.
- Focus rings use React Aria's `data-focus-visible` and the `focus-ring`
  utility from `src/styles.css`, never `:focus-visible` on a React Aria
  part ([shadcn-ui/ui#11258](https://github.com/shadcn-ui/ui/issues/11258)).
  Text fields ring on any focus (`data-focused`), a click included.
  `focus-ring-inset` draws it inside the edge, for a toast, whose dark
  surface the outer ring would not stand out on.
- A mark made of a fill or a shadow (the open item's bar, the chosen
  theme, a popover's outline) gets a `forced-colors:` variant that keeps
  it in Windows' contrast themes, which drop both.

React Aria components take React Aria props: `isDisabled`, not `disabled`;
`onPress`, not `onClick`; `isSelected` and `onChange` on a checkbox or
switch. A disabled button leaves the tab order, so on a page whose
confirmation closes at once (all but Members and Registration, which use
`ConfirmDialog` below), the button that opened it gets `isPending` while the
write it started runs: it keeps focus, so focus has a place when the dialog
closes, and ignores presses. Its label says what is running, e.g.
"Disconnecting...": React Aria announces the label, not a busy state
([Button, Pending](https://react-aria.adobe.com/Button#pending)). Other
buttons the write blocks get `isDisabled`. A form's `SubmitButton` is
pending the same way while its form sends, and disables the form's other
submit buttons; `ReadError`'s Try again uses `isDisabled` while it reads
again.

`AlertDialogAction`, the confirming button of a dialog that asks first,
ignores a mouse or touch press that starts within 500 ms of the dialog
opening: a dialog can open with that button under the pointer, and the
second click of a double-click would confirm what the member never read.
Enter and Space still confirm at once. With `closes="by-caller"` it leaves
closing to the page: `ConfirmDialog` (`src/components/confirm-dialog.tsx`)
keeps the dialog open while its request runs, with Cancel and Escape
waiting, closes it on success, and shows a failure inside it, where
pressing again retries. When the change removed the button that opened
the dialog, `fallbackFocus` says where focus goes. Its children follow
the description, e.g. a delete's list of what else it takes.

The theme is light, dark, or the system's, kept per browser in
`localStorage` by `src/lib/theme.ts`; `useTheme` from `src/hooks` reads
and changes it, as the member menu does. `applyTheme` sets every duration
to zero while it switches the colours and makes the browser restyle the
page at once, so nothing fades from one theme to the other; zero, not
`none`, because React Aria removes a closing tooltip only when its exit
animation finishes. A script in the root route's head marks the page dark
before the first paint; the shell's Content-Security-Policy allows it by
hash.

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
helper is `@/lib/cn`, the `cn` package's engine with merge tables that
`pnpm generate` compiles from `src/styles.css`'s `@theme` into
`src/lib/generated/cn-tables.ts`. The package's own `cn` knows only
Tailwind's scales: it takes `text-snippet` for a colour and drops it beside
`text-muted-foreground`, and keeps both of `text-sm` and `text-snippet`,
leaving the stylesheet's order to pick. A lint rule refuses
`import { cn } from "cn"`, which `shadcn add` writes, and an import from
`@/lib/utils` fails the type check: change either to `@/lib/cn`.
After changing the theme, run `make generate`; `make check` fails while
the tables are older than the theme.

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
lands on the `/connected-accounts` page with. It also leaves out every
operation whose success answers something other than JSON, e.g. a
message body for a frame or an attachment: the browser loads those
itself from the URLs a JSON answer gives, and transport reads only JSON.
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
the contract no longer lists fails type-checking.

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
Send Message (with its `Idempotency-Key`) and Send Message Again do, and
opt in with `repeatableWriteRetry` from `src/api/retry.ts`: the same
failures and waits as a read, each attempt with the same payload and key.

Writes use `networkMode: "always"` and, unless they opt in as above,
`retry: false`. Query attempts them even when its browser connectivity
indicator says offline. That indicator can be wrong about whether a
self-hosted API is reachable. A failed write reports its communication failure
instead of waiting for reconnection. Reconnecting never submits it again;
another attempt, beyond an opted-in write's own retries, requires the user to
submit. The form keeps entered values after a failed attempt.

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
reconnection are separate operations too. A timeout ends a write's
submission, an opted-in write's retries included, and leaves the form's
values available with the uncertain-outcome message above.

Mail is the exception: its lists read conversations through a [TanStack
DB](https://tanstack.com/db) collection over the generated client
(`features/mail/conversations.ts`), pinned to 1.0.0-rc.1.4 and patched
(`patches/`, see the NOTE in `pnpm-workspace.yaml`), with
`@tanstack/query-db-collection` 1.5.1 under it, so every list shares one
copy of each conversation. A list is a live query that loads 50 rows at
a time as it scrolls; `conversations.ts` turns each load into the API's
pages, and re-reads only each open list's newest page: every 30 s while
the tab shows, when it shows again, and back online (`refreshMail`). The
sidebar's unread counts are a second collection over List Places, read
whole and again with those pages, so a count changes with the rows it
counts; it shares List Places' query with the place route's loader, so a
page reads them once (TanStack DB's `select` takes the list out of the
answer). The connected accounts, whose sync status the sidebar words,
are read then too. Check for new mail (`features/mail/check-mail.tsx`)
asks the server to sync each account the list shows whose
`allowedActions` include `sync`, then reads all
three every 2 s for 30 s. When a page fails, TanStack DB asks for the
whole place to repair the list; `conversations.ts` refuses that
(hundreds of requests for a large Inbox), so the list keeps its rows,
stops loading, and offers Try again, which replaces the conversations
collection with an empty one (`restartMail`); the counts stay. The rules
that keep that cheap are in [DESIGN.md](DESIGN.md#data-for-mail). The
collections live per query client and end with the member's session
(`onAccountCacheClear` in `features/auth/session-cache.ts`), as do the
router's loaded pages. `clearAccountCache` tells the browser's other
inbok tabs over a `BroadcastChannel`, and a signed-in frame that hears
it loads again, so a tab never shows one member's mail beside another's.
Mail actions, e.g. the row's star or the picking bar's Archive, go
through `perform` (`features/mail/perform.ts`), which gives the picking
bar's every action an Undo (`from: "picked"`), leaves feedback about a conversation
to the latest action that changed the same thing on it (its place, read
state, or star), sends several conversations in requests of at most the
contract's limit, one after another (`runAction`'s `after`), and runs
`runAction` (`features/mail/actions.ts`) instead of a mutation hook: it
changes the rows and their unread counts at once, sends the action, and
keeps the rows and counts the API answers with; a conversation the API
refuses, or every one when the request fails, goes back as it was, and
`perform` says why, in one toast for many, whose details (`showError`'s
`details`) list them by reason; a later answer that changed again what
failed on every conversation a toast names closes it (`closeAbout`):
a toast is about keys of a conversation and what failed on it, e.g.
`<id> place <label>`; Archive's and `unarchive`'s (Move to Inbox of an
archived conversation) key is `<id> place inbox`, as they change the
Inbox alone, and the other named actions', `untrash` (Move to Inbox from
Trash) among them, is `<id> place`, which covers each place under it
(`covers`). Each action, an Undo included, takes its turn in one order
as its requests are queued: it is the latest for its own keys and the
earlier keys they cover, so an older action's late failure is said only
while nothing later changed the same thing: after Archive then Move to,
or Move to then Delete, the first one's failure stays unsaid, while a
label added after Archive leaves "Couldn't archive" to be said. A
success closes only failures an older action said (`closeOlder`), each
by its exact key, so a long action's late second request never closes a
newer failure on a conversation its first request already changed.
Actions in flight
at once keep none of their answers' counts, which each hold the whole of
List Places at their own moment; the last to settle reads them again. The reading pane reads its row from the same
collection by ID, loaded from Get Conversation when no list holds it, so
an action anywhere shows there at once; a failed read of it keeps the
row as last confirmed, without an action's guess, and only a 404 lets it
go. Opening an unread conversation runs `read`. A search's results
(`features/mail/search.ts`, `search-results.tsx`) are a TanStack Query
infinite read of Search Conversations, 50 a page, whose conversations join
the same collection as a load of their IDs (`idsOf`), which takes the
rows the search just read (`holdFound`) rather than asking again; so an
action, Mark read included, shows on a result at once and every list
agrees, and the rows leave with the results, as the reading pane's do.
The search keeps no cache once its page closes (`gcTime: 0`): a search
opened again reads again, rather than show rows from before an action.
The package ships agent guides for its installed version in
`node_modules/@tanstack/db/skills`.

Under `pnpm dev`, a page sends each read twice and cancels the first:
React's development mode mounts every component twice to expose bugs.
The `pnpm build` output sends it once.

## Kept in the browser

Any script on inbok's origin can read `localStorage` and
`sessionStorage`, and another site cannot
([MDN](https://developer.mozilla.org/en-US/docs/Web/Security/Same-origin_policy#cross-origin_data_storage_access)).
What guards them is what guards the whole page: the Content-Security-Policy
above and the message body's frame, which runs no script. A script that
got in could already call the API with the member's session, so a value
is kept in the browser by what it would reveal at rest, not to hide it
from scripts. First, what may be kept
([OWASP](https://cheatsheetseries.owasp.org/cheatsheets/HTML5_Security_Cheat_Sheet.html#local-storage)):

- Never a secret: a session, password, code, or key. The server keeps the
  session in an `HttpOnly` cookie, which no script reads.
- A choice, or an ID that means nothing without the API, in
  `localStorage` for the browser or `sessionStorage` for one tab.
- Text the member wrote or received only in `sessionStorage`, and gone
  from every tab when the session ends. A tab's storage outlives the
  reload that `onAccountEndedElsewhere` makes, and a tab the browser
  restores later heard nothing, so such a store, e.g. drafts, clears
  itself in each tab that hears the session end, and shows nothing
  until it knows the signed-in member wrote it.

Then the way to keep it, by the value's shape. Keys start with `inbok-`.

- Read before the first paint: a plain key that the root route's head
  script reads, in its own module, as `lib/theme.ts` does.
- One value, replaced whole: a plain key through `localStore`
  (`lib/local-store.ts`); a tab reads it when it loads, and the last write
  holds.
- Rows changed one at a time: a TanStack DB collection from
  `localStorageCollectionOptions`, read through a live query. In
  `sessionStorage` the rows are the tab's own, as `features/mail/folds.ts`
  keeps them. In `localStorage`, give it `window.localStorage` itself: each
  tab then takes the others' writes into its rows, so its next write
  keeps them, where a whole set written by hand from an old copy would
  drop them. Two tabs writing at the same moment can still lose one
  change; the collection takes no lock
  ([TanStack DB](https://tanstack.com/db/latest/docs/collections/local-storage-collection#synchronization)).

A value that belongs to the member registers its clearing with
`onAccountCacheClear`, which runs in the tab that signs in or out. Other
tabs keep their `sessionStorage` until they sign in or close: harmless
for IDs, which match no other member's accounts, not for text.

| Key                        | Holds                                                                                               | Way                         | At sign-out         |
| -------------------------- | --------------------------------------------------------------------------------------------------- | --------------------------- | ------------------- |
| `inbok-theme`              | `light` or `dark`; none is the system's                                                             | head script, `lib/theme.ts` | kept                |
| `inbok-sidebar-open`       | `true` or `false`: the full layout's sidebar open                                                   | `localStore`                | kept                |
| `inbok-sidebar-folds`      | in `sessionStorage`: this tab's unfolded account IDs and label IDs                                  | collection                  | cleared in this tab |
| `inbok-sidebar-last-folds` | the same, as the member last pressed them in any tab; a tab without folds of its own starts from it | `localStore`                | cleared             |

## Forms

Forms use [TanStack Form](https://tanstack.com/form) through the app's
kit in `src/components/form/`: `useAppForm`, built with
[`createFormHook`](https://tanstack.com/form/latest/docs/framework/react/guides/form-composition),
renders each field as a field kind and the form as `form.Form` with a
`form.SubmitButton`. `src/features/auth/sign-in-form.tsx` is the example:

```tsx
const create = useCreateSession({ mutation: { gcTime: 0 } });
const request = useFormRequest(create, ["email", "password"]);
const form = useAppForm({
  defaultValues: { email: "", password: "" },
  validationLogic: revalidateLogic(),
  validators: { onDynamic: request.check(SignInSchema) },
  onSubmit: async ({ value, formApi }) => {
    const sent = await request.send(formApi, value, { data: SignInSchema.parse(value) });
    if (sent.outcome !== "done") return;
    formApi.reset();
    onDone();
  },
  listeners: { onChange: () => create.reset() },
});

<form.AppForm>
  <form.Form label="Sign in">
    <form.AppField name="email">
      {(field) => <field.EmailField label="Email address" autoComplete="username" />}
    </form.AppField>
    <form.AppField name="password">
      {(field) => <field.PasswordField label="Password" autoComplete="current-password" />}
    </form.AppField>
    <form.SubmitButton working="Signing in...">Sign in</form.SubmitButton>
    <RequestError
      errors={request.summary((err) => sessionMessage(err, "sign-in"))}
      requestId={request.reference}
    />
  </form.Form>
</form.AppForm>;
```

- **Checks.** The schema extends the generated one, e.g. `NewSession`,
  and adds `emailAddress` to an email field: the contract's `idn-email`
  gets no generated check, and Zod's `email()` refuses addresses the server
  takes. `revalidateLogic()` runs the check from the first submit, then on
  every change, so errors appear after a submit and update as the member
  types.
- **Sending.** `request.send` awaits the generated mutation hook's
  `mutateAsync`, and `onSubmit` awaits `send`, so TanStack Form's
  `isSubmitting` lasts as long as the request: the fields lock, the
  button shows its `working` words and ignores presses (React Aria's
  `isPending`), and `Form` ignores Enter.
  A submit started from a field moves focus to the button, which stays
  focusable while pending; after a submit, `Form` moves focus to the first
  invalid field. `send` returns the outcome: `"done"` with the answer,
  `"refused"` with the `ApiError`, or `"left"` when the form was gone when
  the answer came (`useMounted`), so a late answer cannot navigate from a
  page the member has left. Code after it acts only on `"done"`, or on
  `"refused"` for a form-specific end, e.g. an expired link. A failure
  that is not the request's `ApiError` is a fault: `send` throws it, and
  `Form` shows it on the app's crash screen.
- **Refusals.** `send` keeps the server's field errors, from `formErrors`
  in `src/api/problem.ts`, with the values sent; an answer clears them.
  `request.check` returns each one, with the schema's issues, while its
  field holds the refused value: it survives a blur, comes back if the
  member types that value again, and blocks sending it. A rule that spans
  fields must also be in the schema, which refuses it first; the server's
  refusal of it clears only when its own field changes. An error on an
  input the form does not render stays out of the fields.
- **Request failures** stay in the mutation (`create.error`), and an edit
  clears one with `create.reset()`. `request.summary` builds the summary
  under the button from a function that words the failure, which
  `RequestError` (`src/components/request-error.tsx`) shows as an alert,
  announced as it appears: the request's message, then rejected inputs no
  field shows.
  When every rejected input shows on a field, the summary is empty and no
  request reference shows, since the fields tell the whole refusal.
  `request.reference` is the failure's request reference only for a fault
  (a 5xx or a lost answer), so a refusal, e.g. a wrong password, shows
  none.
- **Field kinds** are `TextField`, `EmailField`, `PasswordField`,
  `ChoiceField`, radios with a line about each, `SelectField`, one choice
  from a list that opens under it, each option stepped in by its
  `depth`, and `CodeField`, a one-time code in boxes. Each takes a
  `label` and an optional `description` shown before the control; all
  but `ChoiceField` also take `optional`, and all but `ChoiceField` and
  `SelectField` also take `labelAction`, a link or button beside the
  label, e.g. "Forgot password?". `TextField` takes `text="literal"` for a value typed
  exactly, e.g. a host name, and `inputMode="numeric"` with
  `width="short"` for a number that names something, e.g. a port; the
  form keeps it as typed and the schema turns it into the number the API
  takes. `TextField` and `PasswordField` take `passwordManagers="ignore"`
  for another service's credentials, e.g. a mail server's.
- **Several submit buttons**, e.g. "Save and send test" and "Save only",
  each name an `action`; `onSubmit` gets the pressed one's as
  `meta.action`, with `onSubmitMeta` as the default, and Enter in a field
  presses the first. Only the pressed button shows its `working` words;
  the others wait.
- **Message editor rows** are `FromField`, the quiet select of the
  member's connected accounts (a value of 0 while none is chosen, an
  account whose `allowedActions` lack `send` listed but not choosable, and a
  `description` under the row); `RecipientsField`, chips by the
  `isAddress` it is given, with `after` for the row's end and `onTyping`
  for typed text not yet a chip; `SubjectField`; and `MessageField`, with
  `focus="mount"` to start in it. They render through `RowFrame`: a muted
  label on the left, a hairline under the row, the error under the value.
  A part several forms share, e.g. both editors' recipient rows, is a
  `withFieldGroup` over the fields each form names.
- **A new field kind** goes in `src/components/form/fields.tsx` with its
  first form: render the control through `FieldFrame` for a stacked field,
  or `RowFrame` for a message editor's row (a React Aria control that
  holds its own label, as `ChoiceField`'s radio group and `SelectField`'s
  select do, builds the same parts in its own `Field`); each reads the
  field from `useFieldContext` and gives the control its id, name, value,
  lock, `aria-invalid`, and `aria-describedby`, and a function that sets
  the value; then add it to
  `fieldComponents` in `app-form.ts`. Its browser test joins
  `app-form.browser.test.tsx`.
