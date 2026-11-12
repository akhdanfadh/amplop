# inbok web design

The rules for how inbok's web app looks, is laid out, and talks. Read this
before changing any screen, component, or interface text. The choices and
their reasons are in ADR031 (docs repo, `decision-record/ADR031-web-design-system-and-mail-layout.md`);
this file is what to do.

Status: decided 2026-11-11. `feat/design-system` builds the foundation
(tokens, the React Aria flavour, the shell, the mail building blocks,
shared form fields); the mail branches build on it. Until a part is built,
the code still shows the old look; build new work to this file, not to the
old code.

## Principles

- **Calm chrome, colour for accounts.** Navigation, buttons, and surfaces
  are neutral grey. The one chrome colour is the primary blue, for what the
  member can act on. Account colours are the only other colour on screen,
  and they mean "which account".
- **Colour never alone.** An account is a colour and its name. A status is
  a colour, an icon, and a word. Unread is weight, not a dot.
- **Only what exists.** A button, menu item, or link appears only when its
  action is built. No placeholders that do nothing.
- **Words, not decoration.** Empty and error states explain the cause and
  the next step in words; no illustrations, glows, gradients, or emoji.
- **First-party first.** shadcn's React Aria flavour, React Aria, and
  TanStack libraries before anything hand-built; documented API before
  workarounds. When a workaround is unavoidable, it gets a `DEBT.md` entry.

## Tokens

Tokens are the CSS variables in `src/styles.css`, mapped for Tailwind in
`@theme inline` ([shadcn theming](https://ui.shadcn.com/docs/theming)).
Components use token classes (`bg-primary`, `text-muted-foreground`),
never raw colours or Tailwind palette classes (`text-gray-600`).

### Colour

Every text pair below meets 4.5:1, every meaningful mark 3:1, checked in
both themes. A new pair is checked before use (antislop-human's
`contrast-check.py` or the WCAG formula); never assume a grey pair passes.

| Token                            | Light                 | Dark                  | Use                                               |
| -------------------------------- | --------------------- | --------------------- | ------------------------------------------------- |
| `background`                     | `#fafafa`             | `#1c1c1c`             | Page and panes (mild, never pure white or black)  |
| `foreground`                     | `#0a0a0a`             | `#fafafa`             | Body text                                         |
| `sidebar`                        | `#f2f2f2`             | `#161616`             | Sidebar; also `muted` and `secondary` surfaces    |
| `card`, `popover`                | `#ffffff`             | `#262626`             | Raised surfaces, menus                            |
| `sheet` (new)                    | `#ffffff`             | `#ffffff`             | The message body's sheet, white in both themes    |
| `muted-foreground`               | `#666666`             | `#a3a3a3`             | Secondary text, meta, snippets                    |
| `border`                         | `#e3e3e3`             | `#2e2e2e`             | Hairlines; decoration only                        |
| `input`                          | `#848484`             | `#808080`             | Field outlines (3:1, WCAG 1.4.11)                 |
| `accent`                         | `#ececec`             | `#2e2e2e`             | Hover surface (shadcn's name; not a brand colour) |
| `primary` / `primary-foreground` | `#2557d6` / `#ffffff` | `#86a8ff` / `#1c1c1c` | Primary buttons, links, the focus ring            |
| `selection` (new)                | `#e5eaf6`             | `#272a33`             | The open row, the current nav item                |
| `destructive`                    | `#a8000a`             | `#ffa3a5`             | Text and icons on its 8% tint (12% in dark)       |
| `success` (new)                  | `#0f6630`             | `#4ade80`             | Same tint rule                                    |
| `warning` (new)                  | `#904500`             | `#fbbf24`             | Same tint rule                                    |
| `info` (new)                     | = `primary`           | = `primary`           | Same tint rule                                    |
| `highlight` (new)                | `#fff3b0`             | `#5c4a00`             | Search matches; text on it uses `foreground`      |
| `ring`                           | = `primary`           | = `primary`           | Focus ring                                        |

**Account colours** (`account-1` ... `account-8`), handed out in the order
accounts are connected and stored with the account:

| #   | Light             | Dark      | Note                                                            |
| --- | ----------------- | --------- | --------------------------------------------------------------- |
| 1   | `#dd5309` orange  | `#fb923c` | 1 to 4 stay distinct under protanopia, deuteranopia, tritanopia |
| 2   | `#0891b2` cyan    | `#67e8f9` |                                                                 |
| 3   | `#5b21b6` violet  | `#8b5cf6` |                                                                 |
| 4   | `#9d174d` wine    | `#ec4899` |                                                                 |
| 5   | `#c026d3` fuchsia | `#e879f9` | 5 to 8 distinct for typical vision only                         |
| 6   | `#115e59` teal    | `#2dd4bf` |                                                                 |
| 7   | `#db2777` pink    | `#f9a8d4` |                                                                 |
| 8   | `#a16207` yellow  | `#facc15` |                                                                 |

No account colour is blue, red, or pure green, so an account never reads as
a link or a status.

**Measured contrast**, the worst pair per token over every surface it can
sit on (background, card, sidebar, accent, selection), WCAG 2 formula,
checked 2026-11-11. A hover tint is one step stronger (12%, 16% in dark);
only the destructive button uses it. Recheck a row when its token or a
surface changes.

| Token                       | Needs | Light (worst surface) | Dark (worst surface) |
| --------------------------- | ----- | --------------------- | -------------------- |
| `foreground`                | 4.5   | 16.43 (selection)     | 8.26 (highlight)     |
| `muted-foreground`          | 4.5   | 4.77 (selection)      | 5.38 (accent)        |
| `primary` as link text      | 4.5   | 5.11 (selection)      | 5.86 (accent)        |
| `primary-foreground`        | 4.5   | 6.15 (primary)        | 7.35 (primary)       |
| `destructive` on its tint   | 4.5   | 5.62 (selection)      | 5.57 (accent)        |
| `destructive` on hover tint | 4.5   | 5.22 (selection)      | 5.10 (accent)        |
| `success` on its tint       | 4.5   | 5.23 (selection)      | 6.05 (accent)        |
| `warning` on its tint       | 4.5   | 5.12 (selection)      | 6.24 (accent)        |
| `info` on its tint          | 4.5   | 4.57 (selection)      | 4.69 (accent)        |
| `input` (field outlines)    | 3     | 3.10 (selection)      | 3.44 (accent)        |
| `ring`                      | 3     | 5.11 (selection)      | 5.86 (accent)        |
| `account-1` ... `account-8` | 3     | 3.06 (2 on selection) | 3.21 (3 on accent)   |

### Type

IBM Plex Sans Variable (`@fontsource-variable/ibm-plex-sans`), one family,
bundled with the app (ADR026 allows no font service). It tells `I`, `l`,
`1`, `O`, and `0` apart, which sender addresses and codes need.

| Use                                       | Size / line height | Weight                       |
| ----------------------------------------- | ------------------ | ---------------------------- |
| Meta: dates, counts, field help           | 12 / 16            | 400                          |
| Snippets, secondary text                  | 13 / 20            | 400                          |
| Interface body: rows, nav, forms, buttons | 14 / 20            | 400; 500 for buttons and nav |
| Unread sender, subject, date              | 14 / 20            | 600                          |
| Plain-text message body                   | 15 / 24            | 400                          |
| Pane and dialog titles                    | 18 / 24            | 600                          |
| Message subject in the reading pane       | 22 / 28            | 600                          |
| Page titles                               | 24 / 32            | 600                          |

Sentence case everywhere; no uppercase or letter-spaced labels; no negative
letter spacing below 16 px; `tabular-nums` on every number that lines up;
lines under about 75 characters (the message sheet caps near 640 px of
text).

### Space, size, shape, motion

- **Spacing:** Tailwind's 4 px steps. Inside a control 8-14 px; rows in a
  nav or menu 2 px apart; related fields 8-12 px; groups 24-32 px; pane
  and page edges 24 px desktop, 16 px phone. Equal things get equal sizes;
  no one-off values such as `mt-[13px]`.
- **Control height:** 36 px for a fine pointer, 44 px for a coarse one
  (`pointer-coarse:`), set in the components themselves, never per page.
  Nav rows 32 px (44 px coarse); list rows 76 px. Buttons in dense bars
  (the sidebar's header, a toast, a drawer's close) 32 px (44 px coarse),
  so they sit level with the nav rows beside them.
- **Radius:** `--radius: 0.5rem`: controls 8, menu items 6, menus about
  10, cards and the message sheet 12, dialogs 14. Pills only for status
  badges and account chips.
- **Elevation:** no shadow on page, panes, list, or sheet. Menus and
  popovers: `0 4px 12px` at 8% black in light, none in dark (a lighter
  surface and a hairline). Dialogs: `0 16px 40px` at 14% (50% in dark)
  over a dimmed page.
- **Motion:** one easing, `cubic-bezier(0.22, 1, 0.36, 1)`; 150 ms hover
  and press, 200 ms menus and tooltips, 250 ms dialogs and sheets. Animate
  colour, background, border, opacity, and transform only
  (`transition-colors`, never `transition-all`). Reduced motion turns
  movement into fades. A theme change switches every colour at once, with
  no fades. A tooltip shows after the pointer rests 500 ms, then the next
  ones at once; keyboard focus shows it at once. Nothing loops except a
  spinner while work is actually running.
- **Focus:** a 2 px `ring`-coloured outline, 2 px offset, on every
  focusable element, defined once in the components.
- **Links:** primary colour, underlined; the underline thickens on hover.

### Themes

Light, dark, and system, chosen in the member menu and stored per browser.
A script in the root route's head applies the class before the first paint
(the Content-Security-Policy allows the shell's inline script; see
`web/README.md`). Every screen is checked in both themes.

## Components

shadcn/ui's **React Aria flavour** for every component, added with
`make web-add-ui NAME=...` into `src/components/ui`, which is our code to
edit, each with its first caller. Icons are `lucide-react`, 16 px, in the
text colour of their role.

| Need                                         | Component                                                       |
| -------------------------------------------- | --------------------------------------------------------------- |
| Sidebar, rail, drawer                        | `sidebar`                                                       |
| List and reading-pane split                  | `resizable`                                                     |
| Member menu with the theme choice            | `popover` holding a `toggle-group`                              |
| More menus                                   | `dropdown-menu`                                                 |
| Right-click row menu                         | `context-menu`                                                  |
| Names for icon buttons                       | `tooltip`                                                       |
| Undo and error toasts                        | `toast.tsx` on React Aria's Toast (see below)                   |
| Phone editors and search                     | `sheet`                                                         |
| From picker in compose                       | `select` (shows the account mark)                               |
| Search box and its dropdown, recipient chips | `combobox`                                                      |
| Placeholder rows, spinners, empty states     | `skeleton`, `spinner`, `empty`                                  |
| Settings rows                                | `item`                                                          |
| Status                                       | `badge` with tones neutral, info, success, warning, destructive |
| Initials avatars                             | `avatar`, plus the account ring                                 |
| Confirmations                                | `alert-dialog`                                                  |

**Toasts** use React Aria's own
[Toast](https://react-aria.adobe.com/Toast), wrapped in inbok's
`src/components/ui/toast.tsx`: it pauses its timer while hovered or
focused, and F6 reaches it. React Aria labels it alpha (`UNSTABLE_`
names); `DEBT.md` gets an entry when the toast is built. shadcn's React
Aria flavour offers only `sonner`, whose runtime `<style>` the
Content-Security-Policy blocks and whose timer keeps running when the
keyboard reaches Undo. For the demo: Undo toasts last 10 s, error toasts
stay until closed, and toasts stack newest first.

**The message list** is inbok's own `src/components/ui/grid-list.tsx` on
React Aria's [GridList](https://react-aria.adobe.com/GridList) inside its
[Virtualizer](https://react-aria.adobe.com/Virtualizer), with
`selectionMode="multiple"`, `selectionBehavior="toggle"`, and
`disallowTypeAhead`. Rows navigate through `href` and React Aria's
`RouterProvider` wired to TanStack Router (click opens, Ctrl/Cmd-click
opens a new tab). Right-click opens inbok's row menu. The list scrolls its
own box on every screen size. It is the only GridList; search results use
the same component.

## Layout

### Panes and widths

| Window         | Layout                                                                           |
| -------------- | -------------------------------------------------------------------------------- |
| 1280 px and up | Sidebar 240 px, list 380 px (drag 300-560), reading pane the rest (at least 480) |
| 1024-1279      | Sidebar starts as a 56 px rail (68 px coarse); expanding opens it as a drawer    |
| 780-1023       | Sidebar as a drawer from the sidebar button; list and reading pane               |
| Under 780      | One pane at a time: the list or the conversation                                 |

The sidebar collapses with its button (always at the far left of its
header, the wordmark after it) or Ctrl/Cmd+B; the list and reading-pane
divider drags, takes arrow keys, and resets on double-click. Widths and
the collapsed state are saved per browser.

### URLs

| Place             | URL                                                                                                     |
| ----------------- | ------------------------------------------------------------------------------------------------------- |
| A view            | `/mail/inbox`, `/mail/starred`, `/mail/sent`, `/mail/all`, `/mail/spam`, `/mail/trash`                  |
| A label           | `/mail/<label mailbox id>`                                                                              |
| Account filter    | `?account=<account id>`                                                                                 |
| Open conversation | `/mail/<place>/<conversation id>`                                                                       |
| Search            | `/mail/search?q=...&account=...`; a result: `/mail/search/<conversation id>?q=...&message=<message id>` |
| Settings          | `/settings/<page>`                                                                                      |

The URL holds where the member is, never passing state (picked rows,
unsent text, the compose window). Clicking a conversation adds a history
entry. Guarded layout routes (`_app`, `_auth`, `_admin`) check the session,
TOTP, and role in `beforeLoad` before anything draws.

### Sidebar

- Compose (the one blue button).
- Views: Inbox, Starred, Sent, All mail (everything but Spam and Trash),
  Spam, Trash.
- Accounts: All accounts, then each account (its mark, its member-chosen
  name or address, its inbox unread count), filtering every view. An
  account's labels nest under it (Gmail `/` names as a tree); a label
  belongs to one account.
- Account status in words beside it: "Syncing" with a spinner (details on
  hover: "Copying the last 90 days: N messages so far."), "Reconnect",
  "Sync failed".
- Outbox, only while a message is waiting, sending, failed, or unknown.
- The member at the bottom, opening the member menu: Settings, Theme
  (Light, Dark, System, side by side, as icons named by tooltips; the
  chosen one has the `selection` background and an `input` outline, as
  the current nav item has its background and bar), Sign out. Its keys
  are React Aria's toolbar keys: arrows move, Space or Enter chooses.
- Unread: name and count in 600 weight and `foreground`; counts only on
  Inbox, accounts, and labels. Selected: `selection` background and a 2 px
  bar in `foreground` at its left edge, never bold. Icons keep one column,
  open or as a rail. What follows a name (a count, status words) sits in
  the row after it and keeps its width; a long name or address is cut
  short with an ellipsis before it, never overlapped. A count is read
  with its meaning, e.g. "12 unread".

### Message list

- **Rows are conversations** (one per provider thread), newest first by
  the newest message in the view; search results are messages.
- **Row, three lines:** who (first names, "me", count) and date / subject
  and markers / snippet. A 28 px initials avatar ringed in the account
  colour leads (initials of the newest sender in the view); filtered to one
  account, the ring is dropped. Unread rows are 600 weight. Markers: an
  attachment clip, a star filled in the text colour.
- **Dates:** today the time (the browser's 12 or 24 hour clock),
  "Yesterday", the weekday within 7 days, "2 Nov" this year, "2 Nov 2025"
  before; the full date and time on hover.
- **States:** open (`selection`), hover (`accent`), keyboard focus (the
  ring) look different.
- **Picking:** the avatar becomes a checkbox on hover; Space, Shift ranges,
  long press on touch; the list header becomes the action bar with only
  built actions. No bulk action built means no picking.
- **Loading more:** pages of 50 as the member nears the end. New mail while
  scrolled waits behind a "N new conversations" button. The end says
  "That's everything inbok has copied. Older mail stays in Gmail."
- **Check for new mail:** a button in the list header (and the phone top
  bar) that syncs the accounts in the filter; in-app pull-to-refresh runs
  it on touch.

### Reading pane

- A pinned conversation toolbar (archive, delete, mark unread, star, More
  with Open in Gmail); the subject appears in it once the title scrolls
  away.
- Title (22 px), the account mark, the message count. Older messages fold
  to one line; the middle of long threads folds into "N more messages".
- An open message: 40 px ringed avatar; sender name and full address,
  always; "to ..." with details (From, To, Cc, Date, Received by; a Reply-To
  that differs from From is called out); Reply, Reply all (when others
  received it), and More (Reply, Reply all, Forward / Mark unread from
  here, Star this message, Delete this message / Show original, Download
  .eml, Open in Gmail), each only once built.
- The body sits on the white `sheet`; the frame grows to the body and the
  pane scrolls; wider mail scales to fit down to 60%, then scrolls
  sideways. A notice explains hidden remote images, with "Show images".
- Attachments as chips: type icon, name, size.

### Compose and reply

- **Reply** opens inline at the bottom of the conversation: labelled
  Reply, Reply all, Forward buttons; one editor per conversation; From is
  the receiving account, changeable and always visible; a hidden "Re:"
  subject; the quote folded; Ctrl/Cmd+Enter sends.
- **A new message** opens in a 560 px window docked bottom right that stays
  while the member moves around; minimise, full screen, close. On phones
  both are full-screen editors.
- **After Send**, the message waits in place as a card with its status
  until synced mail replaces it.
- **Unsent text** lives in the tab's session storage, cleared on send,
  discard, and sign-out; its conversation row shows "Draft" in `primary`.

### Search

The box at the top of the list ("Search mail"). From 2 characters, after
200 ms, a dropdown shows the five best matches (WAI-ARIA combobox: arrows,
Enter, Escape). "Show all results" or Enter replaces the list pane with
results, most relevant first, with account chips and the note "Spam and
Trash are not searched." Matched words use `highlight`, from the API's
match positions, never from matching text in the browser.

### Phones

One pane at a time; the list scrolls its own box. The top bar (menu,
title and unread count, Check for new mail, search) hides while scrolling
down and returns on a small scroll up or when focus enters it. Compose is
a labelled floating button. A conversation has Back and its actions on
top, and labelled Reply, Reply all, Forward at the bottom. Pull-to-refresh
is inbok's own and runs Check for new mail; the list box sets
`overscroll-behavior-y: contain`.

### Settings and signed-out pages

- `/settings/<page>` in the same frame with its own sidebar: Back to mail
  (to the exact mail URL), Preferences, Security, Connected accounts, then
  for administrators a "This inbok" heading with Members, Registration,
  System email. An administrator without two-factor authentication sees
  that group locked with one line pointing to Security.
- A page: title, one-sentence lead, then sections divided by hairlines,
  heading and explanation on the left, controls on the right; content caps
  at 720 px; no cards around everything.
- Signed-out pages (sign-in, setup, registration, password reset, email
  verification): the wordmark and one centred column about 400 px wide.

## Patterns

### Forms

- Build fields with the shared `createFormHook` components (`useAppForm`:
  text, email, password, radio, checkbox, submit). They own the label,
  description, error, `aria-describedby`, and the server-refusal check.
- Schemas extend the generated Zod schemas; messages come from
  `src/api/validation.ts`; fields read `maxLength`, `inputMode`, and
  `autocomplete` from the same schema. Forms set `noValidate`.
- Label above; description before the control; error under it, with an
  icon and words. "(optional)" marks optional fields; required ones are
  unmarked.
- Errors appear after the first submit, then update live. Focus moves to
  the first invalid field. A server refusal stays while the field holds
  the refused value; a refusal naming two fields clears when either
  changes.
- The submit button names what it does while working ("Saving...") and
  ignores repeats. A request failure shows under the button through
  `RequestError` (a reference only for faults); success is a quiet
  "Saved".
- A 429 wait stays visible while the member retypes: "Try again at 14:32
  (in 4 minutes).", then "You can try again now." Times are English with
  the browser's 12 or 24 hour clock.
- Any form with unsaved typing warns before in-app navigation with
  `LeaveWarning`; closing the tab gets the browser's question.
- One independent choice (theme, a switch) applies at once; related fields
  save together with Save.

### Feedback

- **Reversible actions** (archive, delete to Trash, read, star, and bulk
  versions) happen at once; a toast says what happened, with Undo, which
  sends the opposite action. Toasts last 10 s.
- **Irreversible actions** (disconnect, deactivate, turn off two-factor
  authentication, discard typed text) ask first in an `AlertDialog`: the
  title asks, the body states the consequence and what is kept, the
  buttons name the actions ("Disconnect Studio"), focus starts on the safe
  one.
- **A failed action** rolls back and shows an error toast that stays until
  dismissed, with the fix.
- **Toasts** only report actions whose result left the screen; form
  results stay inline. Nothing important lives only in a toast.
- **Banners** for conditions that last: offline at the top of the content;
  an account problem above that account's mail; setup an administrator
  owes at the top of settings.
- Every mail action is defined once in the actions registry (label, icon,
  bulk support, run, undo, result text); toolbars, menus, the picked-rows
  bar, and toasts read from it.

### States

- **Loading:** routes load data in `loader` with `ensureQueryData`;
  `pendingComponent` appears after 300 ms and stays at least 500 ms, as
  placeholder rows shaped like real ones. Background re-reads show
  nothing. Hovering or focusing a row preloads its conversation, and the
  reading pane starts from the list row's data.
- **Empty:** words only, the cause and one next step: "Connect your first
  account" with Connect Gmail; "Nothing in Inbox"; "Nothing in Studio's
  Inbox"; "Studio is still syncing. Its mail appears here as it is
  copied."; "No spam"; "Trash is empty".
- **Failure:** what failed, that nothing was lost, Try again, a reference
  for faults. A failed refresh keeps the list and adds a banner ("Couldn't
  refresh. inbok isn't answering; showing mail from 10:42."). A real "Page
  not found" and a crash screen with Reload live in the shell.

### Status badges

One `badge`, a soft tint, always an icon and a word:

| Tone        | Used for                                                                 |
| ----------- | ------------------------------------------------------------------------ |
| Neutral     | Waiting, Waiting to sync, Not verified, Admin, Deactivated, This browser |
| Info        | Sending, Syncing (spinner)                                               |
| Success     | Sent, Up to date (Connected accounts page only), Verified, Two-factor on |
| Warning     | Unknown, Reconnect                                                       |
| Destructive | Failed, Sync failed                                                      |

## Data for mail

Mail lists, the reading pane, and mail actions use TanStack DB collections
over the generated client functions; everything else uses TanStack Query
hooks. Rules from the spike (branch 3 confirms them against the real API):

- Sort by a numeric date, then the ID (`stringSort: "lexical"`); dates are
  never null; one fixed order.
- Refresh with inbok's own timer that re-reads only the newest page; the
  built-in `refetchInterval` re-reads every loaded page.
- Apply the "N new conversations" cut-off when drawing, never in the query.
- One live query per view, each with an explicit key; no joins in paged
  lists (look account details up instead); a conversation's places are
  boolean flags.
- Action handlers store the rows the server returns (`writeUpsert`) instead
  of refetching.

## Wording

- Plain and exact, in the second person. "inbok" is lowercase, even at the
  start of a sentence.
- Sentence case. Buttons are a verb and what it acts on, and the verb
  carries into the result ("Archive", "Conversation archived").
- Errors say what happened and what to do; no "Sorry", "Oops", or
  exclamation marks.
- Full sentences end with a period; labels, buttons, headings, and badges
  do not.
- Only characters a keyboard types: no em dashes or curly quotes.
- Digits, with a comma for thousands ("2,140 messages").
- Placeholders show a format ("name@example.com"), never instructions.
- Interface text lives in each feature's `text.ts`.

| Use                       | Means                                          | Not              |
| ------------------------- | ---------------------------------------------- | ---------------- |
| member                    | A person who signs in to inbok                 | user, customer   |
| account                   | A connected mailbox (one Gmail address)        | mailbox, inbox   |
| this inbok                | The installation, in admin text                | instance, server |
| conversation              | One list row, a thread                         | thread           |
| message                   | One email in a conversation                    | mail item        |
| label                     | A Gmail label (IMAP folders later shown alike) | tag              |
| sync                      | Copying mail from the provider                 | fetch, import    |
| Trash, Spam, Archive      | As Gmail names them                            | Bin, Junk        |
| two-factor authentication | Short: two-factor                              | 2FA, MFA, TOTP   |

## Checking a screen

Before a web checkpoint is done:

- Both themes, a 390 px phone width, a 1024 px and a 1440 px window.
- Keyboard only: every control reachable, the focus ring visible, Escape
  closes what opened.
- Every new colour pair through the contrast checker.
- Empty, loading, and failure states seen, not only the happy path.
- antislop's delivery checklist (the antislop skills) with its results in
  the checkpoint report.
- Component behaviour tested in Vitest's browser mode (real Chromium),
  driven by Vitest's `userEvent`, which sends real input
  (`web/README.md`, "Checks and tests").

## Not now

Recorded for later, not built: app keyboard shortcuts, a command palette,
a multi-account filter, a single-message view option, list density and
reading-pane position settings, sender pictures (BIMI logos after DMARC,
contact photos), merging one email received by two accounts, swipe
gestures, drafts beyond the tab, an installable app with offline mail,
per-member theme storage.
