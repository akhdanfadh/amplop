# inbok web design

The rules for how inbok's web app looks, is laid out, and talks. Read this
before changing any screen, component, or interface text. The choices and
their reasons are in ADR031 (docs repo, `decision-record/ADR031-web-design-system-and-mail-layout.md`);
this file is what to do.

Status: decided 2026-11-11. `feat/design-system` builds the foundation
(tokens, the React Aria flavour, the shell, the status badge);
`feat/design-forms` the shared form fields, the settings and signed-out
pages, messages, leave warnings, and banners; the mail branches build on
both and add the list's building blocks with the list (ADR031, "For branch
3"). Until a part is built, the code still shows the old look; build new
work to this file, not to the old code. Not built yet: one rule for a
session that ends on an open page (`feat/session-end`), and the actions
on a hovered row and the row menu (ADR031, "Mitigations and follow-up").

## Principles

- **Calm chrome, colour for accounts.** Navigation, buttons, and surfaces
  are neutral grey. The one chrome colour is the primary blue, for what the
  member can act on. Account colours are the only other colour on screen,
  and they mean "which account".
- **Colour never alone.** An account is a colour and its name. A status is
  a colour, an icon, and a word. Unread is weight and a bar at the row's
  edge, with read rows a step quieter; never a colour alone.
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

| Token                            | Light                 | Dark                  | Use                                                                                               |
| -------------------------------- | --------------------- | --------------------- | ------------------------------------------------------------------------------------------------- |
| `background`                     | `#fafafa`             | `#1c1c1c`             | Page and panes (mild, never pure white or black)                                                  |
| `foreground`                     | `#0a0a0a`             | `#fafafa`             | Body text                                                                                         |
| `sidebar`                        | `#f2f2f2`             | `#161616`             | Sidebar; also `muted` and `secondary` surfaces                                                    |
| `card`, `popover`                | `#ffffff`             | `#262626`             | Raised surfaces, menus                                                                            |
| `sheet` (new)                    | `#ffffff`             | `#ffffff`             | The message body's sheet, white in both themes                                                    |
| `muted-foreground`               | `#666666`             | `#a3a3a3`             | Secondary text, meta, snippets                                                                    |
| `read-foreground` (new)          | `#3d3d3d`             | `#bdbdbd`             | A read row's sender and subject, a step quieter than an unread row's `foreground`                 |
| `border`                         | `#e3e3e3`             | `#2e2e2e`             | Hairlines; decoration only                                                                        |
| `input`                          | `#848484`             | `#808080`             | Field outlines (3:1, WCAG 1.4.11)                                                                 |
| `accent`                         | `#ececec`             | `#2e2e2e`             | Hover surface (shadcn's name; not a brand colour)                                                 |
| `primary` / `primary-foreground` | `#2557d6` / `#ffffff` | `#86a8ff` / `#1c1c1c` | Primary buttons, links, the focus ring                                                            |
| `primary-hover` (new)            | `#3a67da`             | `#7b9ae8`             | A hovered primary button: primary at 90% over `background`, solid, so content never shows through |
| `selection` (new)                | `#e5eaf6`             | `#272a33`             | The open row, the current nav item                                                                |
| `destructive`                    | `#a8000a`             | `#ffa3a5`             | Text and icons on its 8% tint (12% in dark)                                                       |
| `success` (new)                  | `#0f6630`             | `#4ade80`             | Same tint rule                                                                                    |
| `warning` (new)                  | `#904500`             | `#fbbf24`             | Same tint rule                                                                                    |
| `info` (new)                     | = `primary`           | = `primary`           | Same tint rule                                                                                    |
| `highlight` (new)                | `#fff3b0`             | `#5c4a00`             | Search matches; text on it uses `foreground`                                                      |
| `ring`                           | = `primary`           | = `primary`           | Focus ring                                                                                        |

**Account colours** (`account-1` ... `account-8`), handed out in the order
accounts are connected and stored with the account. Each new account takes
the lowest colour the member's other accounts use least: a disconnected
account's colour goes to the next one connected, and a ninth account
repeats one, its name telling them apart.

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
| `read-foreground`           | 4.5   | 9.02 (selection)      | 4.59 (highlight)     |
| `primary` as link text      | 4.5   | 5.11 (selection)      | 5.86 (accent)        |
| `primary-foreground`        | 4.5   | 6.15 (primary)        | 7.35 (primary)       |
| `primary-foreground` hover  | 4.5   | 5.08 (primary-hover)  | 6.19 (primary-hover) |
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

| Use                                                                    | Size / line height | Weight                                          |
| ---------------------------------------------------------------------- | ------------------ | ----------------------------------------------- |
| Meta: dates, counts, field help                                        | 12 / 16            | 400                                             |
| Snippets, secondary text                                               | 13 / 20            | 400                                             |
| Interface body: rows, nav, forms, buttons, settings leads and sections | 14 / 20            | 400; 500 for buttons, nav, and a picker's title |
| Unread sender, subject, date                                           | 14 / 20            | 600; 650 in dark                                |
| Plain-text message body                                                | 15 / 24            | 400                                             |
| Settings section headings                                              | 16 / 24            | 600                                             |
| Pane and dialog titles                                                 | 18 / 24            | 600                                             |
| Message subject in the reading pane                                    | 22 / 28            | 600                                             |
| Page titles                                                            | 24 / 32            | 600                                             |

Sentence case everywhere; no uppercase or letter-spaced labels; no negative
letter spacing below 16 px; `tabular-nums` on every number that lines up;
lines under about 75 characters (the message sheet caps near 640 px of
text).

Dates and times are inbok's British English on the browser's 12 or 24 hour
clock and in its time zone, everywhere: "2:32 pm" or "14:32", "14 Nov
2026", "14 Nov 2026, 2:32 pm" (`src/lib/datetime.ts`). The browser's
language never changes the words or the digits.

In dark, text is drawn with grayscale smoothing: macOS browsers otherwise
thicken every stroke by the same amount, most visibly light on dark,
which brings 400 and 600 close. Unread rows also take 650 there.

### Space, size, shape, motion

- **Spacing:** Tailwind's 4 px steps. Inside a control 8-14 px; rows in a
  nav or menu 2 px apart; related fields 8-12 px; groups 24-32 px; pane
  and page edges 24 px desktop, 16 px phone. Equal things get equal sizes;
  no one-off values such as `mt-[13px]`.
- **Control height:** 36 px for a fine pointer, 44 px for a coarse one
  (`pointer-coarse:`), set in the components themselves, never per page.
  A control inside a list row, e.g. the star, is 24 px, WCAG 2.2's
  smallest target (2.5.8), so it fits its 20 px line; on a touch screen
  its target reaches 8 px past it, moving nothing.
  Nav rows 32 px (44 px coarse); list rows 76 px. Buttons in dense bars
  (the sidebar's header, a toast, a drawer's close) 32 px (44 px coarse),
  so they sit level with the nav rows beside them.
- **Radius:** `--radius: 0.5rem`: controls 8, menu items 6, menus about
  10, cards and the message sheet 12, dialogs 14. Pills only for status
  badges, a list row's marks, and recipient chips.
- **Elevation:** no shadow on page, panes, list, or sheet. Menus and
  popovers share the card's surface and may open over a card, so an edge
  and a shadow set them apart: `0 4px 16px` at 12% black and the `border`
  hairline in light; `0 4px 12px` at 40% black and a `foreground` 20%
  hairline in dark (1.88:1 on a card, 2.11:1 on the page; `border` there
  is 1.11:1). Not a lighter surface: dark hovers use `accent`, which would
  be darker than it. Dialogs: `0 16px 40px` at 14% (50% in dark)
  over the page dimmed by `scrim`, black at 30% (60% in dark); the drawer
  dims it the same way.
- **Motion:** one easing, `cubic-bezier(0.22, 1, 0.36, 1)`; 150 ms hover
  and press, 200 ms menus, tooltips, a toast's fade in, and the Undo
  bar's rise and fade, 250 ms dialogs and sheets. Animate
  colour, background, border, opacity, and transform only
  (`transition-colors`, never `transition-all`). Reduced motion turns
  movement into fades; the Undo bar's draining line keeps draining, as it
  is the countdown, not decoration. A theme change switches every colour at once, with
  no fades. A message a link sends the reader to (the reading pane's "In
  reply to") flashes the `selection` tint: held 600 ms, then faded out
  over 1 s, slow at both ends (ease-in-out), 1.6 s in all, so the eye finds where it
  landed; the interface's easing would spend most of the fade in its
  first 300 ms. A colour change only, so reduced motion keeps it. A
  tooltip shows after the pointer rests 500 ms, then the next ones at
  once; keyboard focus shows it at once; it closes as soon as the
  pointer or focus leaves. Nothing loops except a spinner while work is
  actually running.
- **Press:** a pressed button scales to 97% over 150 ms, so a tap shows it
  landed before any answer; a text link does not, and reduced motion keeps
  buttons still. A keyboard press scales it too: a press is a press, and no
  button here is pressed often enough for 150 ms to slow anyone. A button
  stands back up while the menu, dialog, or sheet it opened is open. A
  sending form's button shows a spinner before its working words ("Signing
  in..."). With reduced motion a spinner fades in and out instead of
  turning, so it still says that work runs where no words sit beside it.
- **Step change:** when a signed-out page, or a settings page holding a
  task of several steps, moves to its next step, e.g. from the password to
  the code, the step's content fades in and rises 4 px over 200 ms
  (`stepEnter`); the page's first step is there at once, a step that keeps
  its title keeps its header still, and reduced motion keeps only the fade.
- **Focus:** a 2 px `ring`-coloured outline, 2 px offset, on every
  focusable element, defined once in the components. One exception: the
  message editor's text fields, which have no box, show only the caret,
  and the editor card's border turns `input` while focus is inside it; its
  chips, buttons, and From keep the ring for the keyboard. Every page
  focuses its heading when it opens, the first load included, so the
  keyboard starts in the page, not in the sidebar, whose landmark is
  named. A step that continues what the member is typing focuses its one
  field instead, its instruction as the field's description: the sign-in
  code, so a code typed at once lands. No skip link: a skip button (a `#`
  link would change the address) is the next step if testing shows a
  need.
- **Cursor:** the hand only over links, which take the member somewhere
  else; buttons, list rows, and every other control keep the arrow, as in
  desktop apps (Tailwind 4's default for buttons).
- **Links:** primary colour, underlined; the underline thickens on hover
  (`textLink` in `src/components/link-style.ts`; `lineLink` for a link on
  a line of its own, 44 px tall; `smallLink` for one in small text beside
  a label, 24 px tall, the least WCAG 2.2 allows outside a sentence). An
  action inside a line of muted text that stays on the page, e.g. the
  reading pane's "In reply to", is a muted link: a button drawn as a link
  (`Button variant="link"`) in `muted-foreground`, underlined, with the
  arrow cursor of a button, 44 px tall on touch.

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

| Need                                     | Component                                                                                   |
| ---------------------------------------- | ------------------------------------------------------------------------------------------- |
| Sidebar, rail, drawer                    | `sidebar`                                                                                   |
| List beside the reading pane             | none: the list has a fluid width ("Panes and widths")                                       |
| Member menu with the theme choice        | `popover` holding a `toggle-group`                                                          |
| More menus                               | `dropdown-menu`                                                                             |
| Right-click row menu                     | `context-menu`                                                                              |
| Icon button names, and shortcuts         | `tooltip`, but none on an in-row toggle whose icon and state are its meaning, e.g. the star |
| Error toasts                             | `toast.tsx` on React Aria's Toast (see below)                                               |
| An action's Undo                         | the Undo bar, `features/mail/undo.tsx` ("Feedback")                                         |
| Phone editors                            | `sheet`                                                                                     |
| From picker in compose and reply         | `select` (shows the account mark)                                                           |
| Recipient chips                          | React Aria's `TagGroup` in the field; `combobox` once contacts suggest addresses            |
| Search box                               | `input-group` holding a search input; no dropdown ("Search")                                |
| A day, and a range of days               | `calendar` (month and year menus), React Aria's `DateField` for typed days                  |
| Placeholder rows, spinners, empty states | `skeleton`, `spinner`, `empty`                                                              |
| Settings rows                            | `item`                                                                                      |
| Status                                   | `badge` with tones neutral, info, success, warning, destructive                             |
| Initials avatars                         | `avatar`, plus the account ring                                                             |
| Confirmations                            | `alert-dialog`                                                                              |

**Toasts** use React Aria's own
[Toast](https://react-aria.adobe.com/Toast), wrapped in inbok's
`src/components/ui/toast.tsx`: it pauses its timer while hovered or
focused, and F6 reaches it. It is a dark surface in both themes, so it
stands apart from the page. React Aria labels it alpha (`UNSTABLE_`
names), which a `DEBT.md` entry covers. shadcn's React Aria flavour
offers only `sonner`, whose runtime `<style>` the
Content-Security-Policy blocks and whose timer keeps running when the
keyboard reaches Undo. Toasts report failures only: they stay until
closed, an error already showing is not shown again, and they stack
newest first, a scrolling box keeping what it shows above them
(`--toast-space`). An action's Undo is the Undo bar (see Feedback), not
a toast: a floating, timed toast covered rows and raced keyboard and
screen-reader users (ADR031, checkpoint 7c).

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

| Window         | Layout                                                                        |
| -------------- | ----------------------------------------------------------------------------- |
| 1280 px and up | Sidebar 240 px, list 26% of the window (300-440 px), reading pane the rest    |
| 1024-1279      | Sidebar starts as a 56 px rail (68 px coarse); expanding opens it as a drawer |
| 780-1023       | Sidebar as a drawer from the sidebar button; list and reading pane            |
| Under 780      | One pane at a time: the list or the conversation                              |

The sidebar collapses with its button (always at the far left of its
header, the wordmark after it) or Ctrl/Cmd+B; its collapsed state is
saved per browser. The list grows with the window from 300 px to 440 px,
so the reading pane keeps at least 480 px down to 780 px. No divider to
drag: it would need a way to resize without dragging, a handle visible at
rest, and its width in pixels, for a choice the fluid width mostly makes;
resizing is in ADR031's backlog.

### URLs

| Place             | URL                                                                                                                                                                            |
| ----------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| A view            | `/mail/inbox`, `/mail/starred`, `/mail/sent`, `/mail/all`, `/mail/spam`, `/mail/trash`                                                                                         |
| A label           | `/mail/<label mailbox id>`                                                                                                                                                     |
| Account filter    | `?account=<account id>`                                                                                                                                                        |
| Open conversation | `/mail/<place>/<conversation id>`                                                                                                                                              |
| Search            | `/mail/search?q=...&account=...&place=...&from=...&date=...&since=...&before=...&has=attachment`; a result: `/mail/search/<conversation id>?<the search>&message=<message id>` |
| Settings          | `/settings/<page>`                                                                                                                                                             |
| Outbox            | `/mail/outbox`, a message `/mail/outbox/<message id>`                                                                                                                          |
| A new message     | `/compose`, an entry point only: it opens the compose window over the Inbox                                                                                                    |

The URL holds where the member is, never passing state (picked rows,
unsent text, the compose window). Clicking a conversation adds a history
entry. Guarded layout routes check access in `beforeLoad` before anything
draws: `_app` the session, and sends an administrator without two-factor
authentication to `/two-factor-setup`; `_app/settings/_admin` the role;
`_auth/_signed-out` that a signed-in member skips sign-in.
The addresses the server puts in emails and redirects (`/sign-in`,
`/verify-email`, `/reset-password`, `/register/complete`, `/security`,
`/connected-accounts`) always open their page, or old emails break; the
last two redirect into settings, so settings can move without the server.

### Sidebar

- Compose (the one blue button).
- Views: Inbox, Starred, Sent, All mail (everything but Spam and Trash),
  Spam, Trash.
- Accounts: All accounts, then each account (its mark, its member-chosen
  name or address, its inbox unread count), filtering every view; the
  filter stays as the member moves between views. Choosing an account
  keeps the open view, or the open label when it is that account's, else
  opens the Inbox. An account's labels nest under it as List Places
  nests them (each label's `parent`), each level in name order. A label
  belongs to one account and opens in it: its account becomes the
  filter, from every account's mail, another account's, or an old link,
  so the sidebar marks the account the label is in; All accounts on a
  label opens every account's Inbox. The Accounts section always marks
  one scope, All accounts or one account, beside the open place. An open
  label's heading names it with its parents ("Work/Projects"). Labels
  show no icon: every one would show the same, and the name gets the
  room.
- Folding: an account's labels, and a parent label's children, fold
  behind a chevron at the row's end, shown at rest on every device (as iOS
  places it on touch), pointing right when folded and down when open. All
  start folded; the way to the open place (its account and parent labels,
  or the filtered account) unfolds as it opens. What the member folds or
  unfolds is kept per tab, through its reloads, so two tabs can show two
  accounts' labels; a tab without folds of its own starts from the
  member's last press in any tab; signing out clears both in that tab, and another tab's own folds when it next signs in. The chevron is a 24 px button, named "Labels of <account>"
  or "Labels under <label's path>", whose state screen readers hear. Every row of the Accounts
  section keeps the chevron's column, folding or not, so its counts end
  at one edge. Opening the sidebar, or another place, scrolls the open
  place's row into view.
- A name cut short by the ellipsis shows in full on hover or focus: an
  account's before its status's details, a label's with its parents'. A
  touch has neither, so there a name cut short stays cut (a known gap,
  e.g. a label three levels deep in the drawer); the page it opens names
  it in full, a label's heading and an account's mark in the list's
  header.
- Account status beside it, as its status's icon alone in the status's
  tone (a spinner for Syncing, a triangle for Reconnect, a circle for Sync
  failed), so a 240 px row keeps room for the name; the words are read to
  screen readers and lead the details on hover, e.g. "Syncing. Copying the
  last 90 days: N messages so far.". A touch has no hover, so with a
  coarse pointer the icon and words take a line under the name, in
  `text-xs`, and the row keeps its 44 px. Connected accounts shows
  the full badge. An account up to date shows nothing. If the accounts
  cannot be read, the group says "Couldn't load your accounts." with Try
  again; the rail shows one alert icon that tries again, the words in its
  tooltip. On the rail an account is its mark, with its name, count, and
  status in the tooltip; labels show only open. The open place's item is
  the current page; the filtering account's is marked as current too, but
  not as a page.
- Outbox, always, with how many of its messages are not sent yet
  (waiting, sending, or failed or unknown and not sent again): the
  sidebar stays still, and the count is what tells the member a send
  needs them.
- The member at the bottom, opening the member menu: Settings, Theme
  (Light, Dark, System, side by side, as icons named by tooltips; the
  chosen one has the `selection` background and an `input` outline, as
  the current nav item has its background and bar), Sign out. Its keys
  are React Aria's toolbar keys: arrows move, Space or Enter chooses.
- Unread: name and count in 600 weight and `foreground`; counts only on
  Inbox, accounts, and labels, with a comma ("12,847"). The Outbox's
  count of messages not sent is in 600 weight, its name not: it is not
  unread mail. Selected: `selection` background and a 2 px bar in
  `foreground` at its left edge, never bold; the scope beside it (All
  accounts, or the filtering account) has the bar alone, so the open
  place stands out from the account it is in. Icons keep one column,
  open or as a rail. What follows a name (a count, status words) sits in
  the row after it and keeps its width; a long name or address is cut
  short with an ellipsis before it, never overlapped. A count is read
  with its meaning, e.g. "12 unread". When the sidebar scrolls, its edge
  fades while there is more that way, so a row cut at the edge reads as
  more below; each fade is whole within its 24 px, and a row that takes
  focus scrolls clear of it.

### Message list

- **Rows are conversations** (one per provider thread), newest first by
  their newest message. A search result is a conversation too, shown
  through its best-matching message: that message's sender, date,
  subject, and the text around the match. A conversation is in a view
  when one of its messages is. Its row, the same in every view, is
  made from its messages outside Spam and Trash, or from its Spam and
  Trash ones when it has no other; Gmail drafts are not shown. So a reply
  moves its conversation up in every view it is in, as in Gmail's
  conversation view.
- **Row, three lines:** who (first names, "me", count) and date / subject /
  snippet, ended by the row's marks: an "Inbox" chip (the `secondary`
  badge, 16 px) outside the Inbox while the conversation is in it, so All
  mail, Starred, Sent, and labels tell it from an archived one, as
  Gmail's list does; then its labels, in the sidebar's order, the list's
  own left out, two chips in all, then "+N" (`RowMarks`): a label by its
  own name, then its parent's path when another of the account's has
  the same own name ("Acme · Clients"), so a cut keeps the own name;
  each chip cut at 6rem, as a row is one line (the pane's chips and the
  pickers wrap); the preview gives way first, the chips taking up to 60%
  of the line, shorter to fit down to 3.5rem, never a stub. A line too
  narrow for two at that floor (under 16.25rem) shows one chip and "+N",
  and one too narrow for one (under 10rem, e.g. a phone at 200% text)
  none, by a container query on the line, in rem so it follows the text
  size. Search results show one chip at most, then "+N": a result's
  snippet shows why it matched, so it keeps the room. The full paths
  show on hover, screen readers hear every label in the row's name, last
  ("…, Labels Clients/Acme and Hiring"), and the reading pane shows them
  whole; then the star button, on every row as in Gmail's
  list and app, outlined until starred (→ reaches it from the row);
  pressing it stars or unstars at once. A 36 px initials
  avatar ringed in the account colour leads (initials of the newest
  message's sender), its top on the first line's, 16 px from the list's edge
  (the heading's inset) and 12 px from the text, closer than the edge so it
  reads with its text; filtered to one account, the ring is dropped.
  Unread rows are 600 weight (650 in dark) with a 3 px `primary` bar at
  their leading edge, the text colour in forced colours; read rows' sender
  and subject are `read-foreground`, a step quieter, so weight,
  brightness, and the bar all tell them apart. The bar is not the
  sidebar's 2 px `foreground` bar for the current item: a list row's
  current state is its `selection` fill. Markers: an attachment clip, a star filled in the
  text colour, the Inbox chip; a screen reader hears "In Inbox" and
  "Starred" in the row's name.
- **Dates:** today the time (the browser's 12 or 24 hour clock),
  "Yesterday", the weekday within 7 days, "2 Nov" this year, "2 Nov 2025"
  in another year, a later day too; the full date and time on hover.
- **Missing data:** no sender reads "Unknown sender" with a person icon in
  the avatar; no subject "(no subject)"; counts take a comma ("1,284").
- **States:** open (`selection`, and `aria-current` for screen readers),
  hover (`accent`), keyboard focus (the ring) look different.
- **Picking:** the avatar becomes a checkbox while its row is hovered,
  focused by the keyboard, or picked, and on every row while any is; a
  picked row's only mark is its ticked box, so the fills stay the open
  row's and hover's; the box keeps the avatar's 36 px, 44 to touch, and,
  in a list of several accounts, its account ring, so the rows still say
  whose they are while picking. The box, Space, Shift ranges (Shift on a
  press or an arrow), Ctrl or Cmd+A, and a long press on touch pick;
  while picking, a tap picks too, and a mouse press on a row's text, or
  Enter, still opens it, the picks staying. Escape (from the list or the
  bar), Back, and unticking the last row stop picking, focus going back
  to the list's row, where coming back to the list while picking puts it
  too, the heading being the bar's hidden one then. Starting adds a
  history entry, which Back takes, at every width. Picks follow
  the rows: a row the list no longer shows is unpicked, except one an
  action took out until it answers, so a refused row comes back picked;
  Undo brings rows back unpicked. A place's list picks; a search's
  results, which span places, keep their avatars (ADR031, "Mitigations
  and follow-up").
- **Picking bar:** it takes the list header's place (the top bar's on a
  phone, see Phones): a tri-state checkbox over the rows' boxes, mixed
  while some loaded rows are picked, which picks every loaded row
  ("Pick all 25 loaded conversations"), and unpicks all once all are;
  how many are picked ("25 picked"), "All 60 picked" only once the list
  has reached its end; with touch-size buttons the number alone ("3",
  "All 60"), as phone mail apps show it, and elsewhere the word dropped
  where it does not fit; then the toolbar's slots, move, Delete,
  mark, star, and More, the list header's 32 px buttons; where they do not
  fit beside the count, e.g. at large text, they wrap onto a row of their
  own.
  Each slot shows its usual action while any picked row needs it
  (Archive while any is in the Inbox, Mark read while any is unread,
  Star while any is unstarred, as `actionsFor` over the rows offers
  them); the pair's other action goes into More when the rows are mixed,
  with Move to… and Labels… (see Reading pane, Labels) and Report spam;
  the star is a plain Star or Remove star. An action
  goes only to the picked rows it changes, every guess showing at once,
  and always gets an Undo (see Feedback). Picking all picks the loaded
  rows, as fixed IDs; acting on a whole place is Not now. When an action
  leaves no picked row, focus goes to the row that took the first one's
  place.
- **Loading more:** pages of 50 as the member nears the end. New mail while
  scrolled waits behind a "N new conversations" button floating over the
  list's top, centred: a small primary button with an up arrow and the
  popover shadow, which shows them on top and moves focus to the newest.
  Rows changing above the screen, e.g. a reply moving one to the top,
  leave the rows on screen where they are. The end says
  "That's everything inbok has copied. Older mail stays in Gmail.", 12 px
  under the last row; the loading spinner before it is centred in a row's
  height.
- **Check for new mail:** an icon button at the end of the list's header
  (the phone's top bar), named by a tooltip, that asks each account in the
  filter that inbok can use to check for mail now; its icon turns while
  the requests run, whole turns, and dims instead with reduced motion.
  Lists, counts, and account status are then read every 2 s for 30 s, so
  new mail shows within seconds. A check started moments ago counts as
  started; an account refused, none to check, or the browser offline is
  said in an error toast, one sentence per reason naming every account it
  holds ("3 accounts are no longer connected: A, B, and C.").

### Reading pane

- A pinned conversation toolbar of icon buttons, each named by its
  tooltip: where one pane shows at a time, an arrow Back first, named
  "Back to list"; then the conversation's state's first action, Delete,
  Mark read or unread, Star (a toggle), and More with Move to… and
  Labels… (see Labels below), Report spam, and Open in Gmail, which opens
  Gmail's own page for the conversation in a new tab, an address Google
  does not document.
  The actions follow the conversation, not the address, except in Spam
  and Trash (`actionsFor` in `features/mail/rules.ts`): in a Spam list,
  Not spam, Delete, Mark read or unread, Star; in a Trash list, Move to
  Inbox, Mark read or unread, Star; elsewhere in the inbox, Archive,
  Delete, Report spam, Mark read or unread, Star; archived, Move to
  Inbox in place of Archive; only in Spam or only in Trash, as in those
  lists. No Delete forever. At most five buttons, then More: a sixth
  action goes in More, chosen by hand, never moved there by width; the
  same buttons at every width. The faded subject may shrink under a
  512 px pane, so the bar keeps one 48 px row, and hides under a 28em
  pane, e.g. a phone, where it would be a few letters; the bar wraps
  only for large text, and pins only while its buttons fit one row:
  under a 20em pane (a phone at 200% text) it scrolls away with the
  title rather than hold a third of the screen. Its icons are named by
  tooltips only, which touch never shows, as Gmail's app does. Once the title scrolls under
  it, the subject fades in (150 ms) and a hairline shows below it.
- After an action the pane goes back to its list when the conversation
  was in that list's place and account before and is not after, and
  always after Mark unread; otherwise it stays, e.g. Archive inside a
  label. Back is the history's when a row of that list opened the
  conversation, else the list in its place, so an action never leaves
  mail, e.g. after Settings' Back to mail. Once an action has taken the
  conversation out, the pane takes no more actions on it, so a double
  click never runs Archive and then Move to Inbox. The row that took its
  place takes focus: the next older, else the one above; after Mark
  unread, the conversation's own row; with no row left, the list's
  heading. A button whose action changes, e.g. Archive to Move to Inbox,
  keeps focus. A refused action leaves the member on the list with an
  error toast; one the provider no longer has also removes the row.
- Opening an unread conversation marks it read, once: when its messages
  have come (they decide which start open) and the tab shows, and not for
  an account that needs reconnecting. Its row un-bolds and the counts
  drop at once. A read the server refuses or that fails puts both back
  silently and is not tried again; arrow keys in the list only move
  focus, never open.
- **Labels** (ADR031, "Labels as built"): More's "Move to…" and
  "Labels…" close More and open a picker in its place, under More's
  button, at every width (a submenu beside More ran off a 320 px screen
  and large text); one picker for both and later the row menu
  (`features/mail/label-menu.tsx`): its title, "Move to" or "Labels",
  which names the dialog, as the two read alike once More closes; a
  filter field under it (React Aria's `Autocomplete`, focused as it
  opens, but not from a touch, where the keyboard would cover the list),
  then the labels as the sidebar's tree, each under its parent with the
  sidebar's thin line per level, by its own name; while filtering, each
  match by its path, the parents muted. 240 px wide, at most 28rem tall,
  the list scrolling under the field. One section per account, headed by
  its name wherever the list shows several accounts' mail, even when one
  account's conversations are left. An account with none says so in
  words ("No labels in Work yet. Labels you make in Gmail show
  here."); beside accounts that have some, a note under the list names
  it ("No labels in Work yet. ..."), as React Aria drops a section with
  no items. Names you choose from wrap, never cut: a long name or path
  takes more lines, and one without spaces, e.g. an address, breaks at
  the edge, so two long labels alike at the start can still be told
  apart on a phone, where no tooltip shows. Enter ticks a label, as a
  press does, and Tab reaches Apply; Escape clears the filter, then
  closes, focus back on More, and the field's × clears it on a phone,
  which has no Escape. It opens in More's place with no entrance, as
  More closes there.
  - **Move to** files the conversation away: it adds the chosen label
    and takes the conversation out of the list's place, the Inbox, a
    label, Spam, or Trash, as Gmail does; offered only in lists whose
    place a change can name (the contract's `ChangePlace`), so not in
    All mail, Starred, Sent, or a search across places. The label moved
    from is left out. Choosing sends at once, for that account's
    conversations only.
  - **Labels** tags without moving, offered for conversations with a
    message outside Spam and Trash (a row wholly there lists no labels,
    a workaround recorded in ADR031 and DEBT.md): a checkbox per label,
    ticked from what the conversations have, a dash for one some of them
    have (pressing it goes to all, none, then back to some), and
    **Apply** under the list, disabled until something changed, which
    sends every change as one request, focus back on More; Escape or a
    click outside sends nothing. It waits, saying so, while the ticks
    add or remove more than the contract's limit for one change ("Add or
    remove up to 100 at once.").
  - **Label chips** under the account line: the Inbox while the
    conversation is in it, then each of its labels by path, in the
    sidebar's order, as the list's Inbox chip (the `secondary` badge,
    rounded rather than a pill, so a long path wraps inside it; the
    recipient chips' sizes to touch, 36 px with a 44 px × target),
    on React Aria's `TagGroup`. Each opens its list and has an × that
    sends at once: the Inbox's runs Archive, a label's takes that label
    off. One Tab stop reaches them; arrow keys move between them, Delete
    or Backspace removes one; with the last gone, focus goes to the
    title. No chip for Spam or Trash, whose toolbar already says where
    the conversation is.
- Title (22 px), the account mark, the message count. The title and
  account draw from the list's row at once; "Loading messages..." stands
  under them until the messages come, and the view takes focus, so a
  screen reader starts at the subject. The newest message
  and every unread one start open; the others fold to one row: avatar,
  sender, preview, a paperclip when inbok knows it has attachments (only
  once its content was fetched: the last 90 days' in the background,
  older ones when opened, so an older unopened message shows none), date
  (as the message list dates rows), one line from a 512 px pane, two
  below. The first message and the newest three always show; between
  them, a run of two or more folded messages the member has
  not opened or folded becomes one "N more messages" row, which shows them
  all, folded; a message the member opened or folded keeps its own row, so
  one folded again never disappears into the run. Pressing a row opens
  its message, which only then loads its content; a click on an open
  message's header, or its avatar, folds it. Rows fill the band between
  their hairlines on hover, square, with an inset focus ring.
- An open message: 36 px avatar (the list's size, so names line up),
  unringed: a conversation is one account's, which the account mark under
  the title names, so a ring, as in a list filtered to one account, would
  say nothing (the list rings rows only where accounts mix); the sender's name and full address (every sender's, when there are
  several) on one line while both fit,
  the address moving to the next line whole, never cut, since its domain
  tells real mail from fake; the date ("14 Nov 2026, 6:18 pm": inbok's
  words, the browser's clock, the day and time in full on hover), which
  moves to the "to" line in a
  pane under 512 px. "to ..." (recipient names, "me" for the account, "to
  no one" when there are none) and a 20 px chevron that shows the details
  inline below: From, Reply-To, To, Cc (each lists 10 addresses, then "Show
  all N"), Date, Received by; labels beside values from 512 px, above them
  below. A Reply-To at a domain none of the senders is at gets its own
  line, "Replies go to ..., not the sender.", in muted text, not a warning:
  a Reply-To at the sender's own domain, as newsletters use, is not called
  out, nor is one on a message the account sent, which is answered to its
  To. A message with no From reads "Unknown sender"; a name that is its
  address shows once; a group without an address ("Undisclosed
  recipients") shows its name alone. A message answering one other than
  the row just above says which, under the sender: "In reply to Ana Lima
  on 2 Nov 2026, 9:00 am", a muted link ("Links") naming the answered
  message by its sender and its own header's date, so two messages from
  one sender on one day stay apart; "on" keeps the date from reading as
  one more sender. It opens that message,
  out of "N more messages" if it was there, scrolls it to the pane's top
  under the toolbar, focuses the message, and flashes it ("Motion"), so
  the press shows even when the message was open and in sight (Gmail
  shows a thread flat by date, so a late reply to an early message
  otherwise reads as answering the one above it). The pane's scroll
  padding follows the pinned toolbar's height, so nothing focused or
  scrolled to sits under it. After the date: Reply, Reply all
  when others received it, and More (Forward / Mark unread from here,
  Star this message, Delete this message / Show original, Download .eml),
  each only once built. They end the sender's line at every width, in
  `foreground`, so they never move from one message to the next; a 32 px
  box overhanging the 20 px line, whose target ::before grows to 44 px on
  touch without growing the line, 12 px apart there so the targets do not
  meet.
- Reply on a message opens the conversation's one editor right below that
  message; Reply and Reply all after the last message answer the newest,
  so the editor opens at the bottom. While it is open every Reply hides,
  so a second never replaces typed text; Discard returns focus to the
  Reply that opened it. If sync removes the message being answered, its
  place keeps "The message you're answering was deleted." and the editor
  below it, until the member sends or discards. A sent reply's status
  shows at the bottom, where the reply lands, as the newest message.
- An HTML body sits on the white `sheet`; a plain-text body keeps the
  theme's surface. The frame grows to the body and the pane scrolls; wider mail scales to fit down to 60%, then scrolls
  sideways. A notice explains hidden remote images, with "Show images".
- Attachments, after the bodies, as chips 8 px apart that wrap. A chip is
  a link that downloads the file: 36 px (44 px coarse), radius 8, an
  `input` outline on `card`, `accent` on hover. It holds a 16 px type icon
  in `muted-foreground` (image, audio, video, document, spreadsheet,
  archive, other), the name at 14 px, and the size as meta, tabular
  (`640 bytes`, `36 KB`, `2.4 MB`). A name that does not fit is cut in the
  middle (`Q3-report-north...-final-v12.xlsx`): its start shortens first,
  down to a character and its ellipsis, then the up to 10 characters
  before the extension, which tell similar files apart; the extension
  always shows. A cut name shows whole, wrapped, in a tooltip on hover
  and keyboard focus; screen readers read it whole. A file the sender
  left unnamed reads "Unnamed file".
- A plain-text body longer than about 100 KB shows its start, then "This
  message is long, so inbok shows only its start." with a "Download the
  whole text" link, as secondary text. A message with no text says "This
  message has no text." Content loads behind "Loading message..." after
  300 ms; a failed load is a `ReadError` with Try again.

### Compose and reply

- **Reply** opens inline below the message answered (the newest, from the
  labelled Reply and Reply all buttons after it; an older one, from its
  header's Reply); one editor per conversation; From is the receiving
  account, changeable and always visible; a hidden "Re:" subject;
  Ctrl/Cmd+Enter sends. Not built yet: Forward beside them, and the
  answered message quoted and folded below the reply.
- **The editor** is one `card`: a row each for From, To, and the Cc and
  Bcc that are filled or asked for, a muted label on the left and its
  value on the right, divided by hairlines; then the message, with no box
  of its own; then a bar with Send, its tooltip naming the shortcut as
  the sidebar's does ("Send (Ctrl+Enter)", Cmd on a Mac), and Discard (a
  trash icon with its tooltip). Empty Cc and Bcc wait behind buttons at
  the end of the To row. Labels and values share one 32 px
  line, so a wrapped list keeps its label level with the first line. On
  a card narrower than 24rem, e.g. a phone or text at 200%, each label
  sits on its own line above its value, which then has the whole width.
  The message grows with its text up to 60% of the window, then
  scrolls.
- **From** is a quiet `select`: no outline at rest, `accent` on hover and
  while its list is open, its chevron and label marking it. Each account
  shows its mark, its address, which recipients see, and the name the
  member chose for it, which only the member does. A line under
  it says when the chosen account didn't receive the message (the reply
  then starts a new conversation in that account's Gmail), or, when none
  could be chosen, which account needs reconnecting, or, while Gmail isn't
  set up on the inbok, that the accounts can't send yet, pointing to
  Connected accounts, which says why for whoever reads it. Only accounts
  the server lists as able to send (`allowedActions`) can be chosen.
- **Recipients are chips**, one pill per address (`secondary`, 14 px, 28
  px tall, 36 px for a coarse pointer). A comma, semicolon, Enter,
  leaving the field, or pasting a list makes chips; "Name <address>"
  keeps the address. Backspace in the empty input removes the last chip;
  the chips are one Tab stop, arrows move between them, and Delete
  removes one; the remove buttons stay out of the Tab order. An address
  that isn't one stays a chip in `destructive` on its tint, named "not an
  email address", and the field's error says which.
- **A new message** opens in the compose window, which holds the same
  editor rows with a Subject row after the recipients, and stays while
  the member moves between mail and settings. Docked: 560 px wide,
  bottom right on the window's bottom edge, `card` with the dialog
  shadow and the `border` hairline (`foreground` 20% in dark), its top
  corners 12 px; a 40 px `muted` strip (44 px for a coarse pointer) holds its title (the subject, else
  "New message") and Minimise, Full screen, and Close, 32 px icon buttons
  with tooltips; pressing the title minimises and restores it too.
  Minimised, only the strip shows, 288 px wide. Never wider than the
  screen: a window narrowed after it opened stays docked. Full screen: a 960 px
  window, 88% of the height, over the page dimmed by `scrim`, the dialog
  radius 14 px; Escape or pressing the dimmed page docks it again. Send
  and Discard sit in the editor's bar; the message fills the height left.
  It rises 16 px and fades in over 250 ms as it opens; minimising,
  restoring, and full screen change at once. Close and Discard ask first
  with typed text; Escape never closes it.
- **The compose window and the keyboard:** docked or minimised, it is a
  landmark named by its title, beside a page that stays in use; F6 moves
  between it and the page, which is a landmark too ("Mail page",
  "Settings page"). Full screen, and on phones, it is a modal dialog: the
  page behind it inert, Tab going round its own controls. Its edge turns
  `input` while focus is inside, as the reply's card does. Focus starts in
  the first field to fill in (From, else To, else the start of the
  message); minimising moves it to the strip's title,
  restoring back to the field it left; Send moves it to the status;
  closing gives it back to what opened the window, else the page's
  heading. Compose, while the window holds a message, brings it back
  instead of starting another.
- **On phones** a new message is a full-screen editor: Close, the title,
  and Send in a 56 px top bar, where the keyboard can't cover Send, and
  Discard in the bar under the message. The layout when it opens holds
  until it closes, so turning a phone or resizing never moves what is
  being written. It is a history entry: Back closes it, asking first with
  typed text, and Close steps back. A reply stays inline on phones too:
  without saved drafts, a full-screen reply would make checking an
  earlier message cost the text; inline, it is a scroll away, and leaving
  the page with typed text asks first.
- **After Send**, a reply waits in place as a card with its status until
  synced mail replaces it. The compose window holds the new message's
  status instead of the editor, shrunk to it: a 32 px circle in the
  status's tone with its icon, one line ("Sending to ana@example.com...",
  then "Sent to ana@example.com"), and under it in muted text the sending
  account's mark, its address, and the time; Open in Outbox below. A
  failure names its status in that line, says why under it, and puts its
  actions in a bar (Edit and send or Send again, then Open in Outbox). A
  message sent again reads Tried again, neutral, with a link to its copy.
  The window closes 3 s after Gmail takes the message, waiting while the
  pointer is on it or focus on one of its controls; minimised, its strip
  shows the status's mark before the title.
- **Unsent text** lives in the tab's session storage, cleared on send,
  discard, and sign-out; its conversation row shows "Draft" in `primary`.
  Not built yet: until then a reply's text lives only in its open editor,
  and leaving the page with typed text asks first.

### Outbox

The member's messages from inbok, as a mail place: the list beside the
reading pane, one shown at a time on a phone ("Panes and widths"). Its
heading, Outbox, heads the list, or sits in the top bar on a phone. No
search box, no picking, and no row actions: search finds mail, which
these messages are not until Gmail has them, and nothing acts on several
messages at once. Statuses read again every 2 s while one is waiting or
sending (web/README.md, "Routes").

- **List:** the newest 50, newest first, by when each was saved; then, as
  the list's end, "inbok keeps your newest 50 messages here, not the ones
  you send from Gmail itself."
- **Row, three lines,** a conversation's row's size: "To" in
  `muted-foreground`, then the recipients by mail's rules for names (To,
  Cc, then Bcc; "no one" for none), and the date it was saved / subject /
  the status as its icon and word in its tone's colour, without a pill
  (`StatusWord`, "Status badges"), then "·" and a short reason in muted
  text: why it failed, what an unconfirmed one asks, or "You sent it
  again as a new message."; nothing after Waiting, Sending, or Sent,
  which say it alone. One recipient shows as its address, left to right
  whatever its letters; several each keep their own direction, as a
  conversation's senders do. A 36 px initials avatar of the first
  recipient leads, ringed in
  the sending account's colour where accounts mix. No unread weight and
  no checkbox: the member wrote each message. A message sent again reads
  Tried again, neutral: its copy carries the status that matters.
- **States:** placeholder rows while the list loads; a `ReadError` with
  Try again when it can't; a failed reread keeps the rows under a
  `ReadError` saying they may be out of date; an empty Outbox says
  "Nothing sent from inbok yet", what will show, and to write one with
  Compose.
- **Reading pane:** a conversation's frame ("Reading pane"): the pinned
  toolbar, with Back on a phone and later the message's own actions; the
  subject; the sending account's mark, or its address once the account
  is gone. Then the status in a `card`, the compose window's summary
  ("Compose and reply") without Open in Outbox: a mark, one line, the
  reason or the account and time, and the actions in a bar (Send again,
  Edit and send, Open Connected accounts); the status shows in place as
  a message opens and rises only when it changes while open. Then the
  message, open, as in
  a conversation, without folding, its details naming the account as
  "Sent from". With nothing open, "No message open."

### Search

- **The box** heads the list ("Search mail", a search input in an
  `input-group`), from 1024 px; below that the top bar's search icon
  turns the bar into the box, with a Back arrow ("Phones"). It holds
  words only: every filter is in the sentence under it. Empty, it names
  what it searches: "Search Receipts" in a label or a view but the Inbox
  and All mail, which search all mail; "Search mail" otherwise.
- **Live results, no dropdown.** From 2 characters, once typing rests
  200 ms, or at Enter, the results take the list's place at
  `/mail/search`, the box still focused. Started in a place, the search
  keeps to it (none from the Inbox or All mail); further typing replaces
  the address, so Back leaves the search rather than stepping through
  each word. Escape or the box's clear button returns to the place it
  started from, or the one place it searches, or the Inbox, focus kept
  in the box (on a phone, back on the search icon). Each box has one
  search in flight: typing cancels the one before, and the last results
  stay on screen, a spinner in the box, until the next arrive. The
  server lets a member run two searches at once; refused (429 with
  Retry-After), the box waits that long and asks again without a word.
- **The filters read as one sentence** under the box: "Searching all
  mail in every account, from anyone, any time, with or without
  attachments." Each underlined phrase (a dotted line, solid on hover)
  opens its menu, and a change applies at once: where (any of the views,
  All mail among them, and the account's labels, up to the API's limit,
  past which only a ticked place can change and the menu says the limit;
  it stays open while the member ticks; named in the menu's order, three
  then "and N more", a label by its own name, its path in the menu),
  which account, from whom (addresses as recipient chips, any of them,
  up to the API's limit), when, and attachments. A label is one
  account's, so choosing another account drops it. A phrase keeps to one
  line, cut with an ellipsis when too long for it, e.g. a long sender
  address, and shows whole in a tooltip on hover and focus.
- **Dates:** a popover with the choices relative to today (Any time,
  Past 7 days, Past 30 days, Past year, Older than a year), which apply
  at once, beside a range: Since and Before as typed days (14/11/2026,
  inbok's British order) over one calendar with month and year menus, so
  mail from years back is two choices away. The field last focused takes
  a day picked; filling one moves to the other while it is empty;
  typing a day moves the calendar to it. Since is filled, the days
  between shaded, and Before outlined, as the range stops short of it
  ("Before is not included. Leave either empty."); no day after today
  ("Pick days up to today."), and none typed only in part ("Finish typing
  the day, or clear it."), Apply waiting meanwhile; Apply sets it. The
  sentence says "in the past 7 days" or "since 2 Nov 2026 and before 14
  Nov 2026".
- **Results** are conversations in the list's GridList and row, most
  relevant first, 50 at a time: each shows its best-matching message's
  sender, date, subject, and the text around the match, the rest (unread,
  star, count, Inbox chip, account ring) the conversation's. Matched
  words use `highlight`, from the API's match positions (UTF-16 offsets,
  as JavaScript strings count), never from matching text in the
  browser; a match only in the sender or recipients marks nothing. A row
  stars, and its conversation's actions show, at once, as in every list;
  one an action takes out of the places searched leaves the results.
  Under the sentence: how many conversations ("1,000 or more" at the
  API's limit), then, unless Spam or Trash is searched, "Spam and Trash
  are not searched." with "Search them too", which adds both, offered
  only while both fit the API's limit of places. The end reads "That's
  every match.".
- **Opening a result** goes to `/mail/search/<conversation id>` with the
  search and `message=<message id>`: the conversation opens at that
  message, open, scrolled to the pane's top, and flashed as a message a
  link shows; its toolbar follows the conversation, and Back returns to
  the results.
- **States:** before anything to search, "Type 2 or more characters, or
  choose a filter."; words longer than the API takes (its `maxLength`, in
  characters) are not sent: "Search with 200 characters or fewer."; the
  first load shows placeholder rows after 300 ms; no match names the
  words ("Nothing matches "invoice""), says which accounts are still
  syncing, and offers one next step: Clear filters when any is set, else
  Search Spam and Trash too. A failure says why
  (search not answering, no connection, or another failure) with Try
  again; a failed later page keeps the rows, with Try again under them.

### Phones

One pane at a time; the list scrolls its own box. A conversation's Back
goes back in history, as the phone's own back does. The top bar holds
the menu, then the place's heading (a label's own name, its path read to
screen readers), the account it is filtered to, and Check for new mail,
then search, whose icon turns the bar into the box (a Back arrow, then
the box) until the member leaves the search; no unread count, as in
phone mail apps: the list's bold rows show what is unread, and the
drawer has the counts. It stays in
place, and an open conversation leaves it to the wordmark. While picking
it is the picking bar, the menu turned into an arrow, "Stop picking", and
the bar is the phone's way to the picked rows' actions; picking closes
an open search box. From 780 px
to 1023 px, where the sidebar is also a drawer, the same bar holds them.
Compose is a labelled floating button. A conversation has Back and its
actions on top, and labelled Reply, Reply all, Forward at the bottom.
The list box sets `overscroll-behavior-y: contain`, so the browser's own
pull-to-refresh, which would reload the app, never starts.

### Settings and signed-out pages

- `/settings/<page>` in the same frame with its own sidebar: Back to mail
  (to the exact mail URL), Security, Connected accounts, then
  for administrators a "This inbok" heading with Members, Registration,
  System email. An administrator without two-factor authentication never
  sees settings: the server lets their session only set it up, on the
  signed-out card at `/two-factor-setup`.
- A page (`SettingsPage`, `SettingsSection`): title, one-sentence lead,
  then sections divided by hairlines, heading and explanation on the left,
  controls on the right, stacked below 512 px of page; content caps at
  720 px; no cards around everything. A result to report, e.g. "Two-factor
  authentication is on.", sits under the lead, with an icon in its tone:
  green for a change that worked, grey for news that changed nothing
  ("Connecting was cancelled on Google's page, so nothing changed."), red
  for a problem the member can fix, which is an alert. It stays until the
  next action.
- A task of several steps, e.g. setting up two-factor authentication, gets
  its own page under its settings page (`/settings/security/two-factor`),
  with "‹ Security" above the title; the settings page keeps one row with
  the status and a way in. A section whose step needs more than the right
  column, e.g. a QR code, stacks its heading over it
  (`layout="stacked"`); a form fits the right column.
- Settings that save together, e.g. System email, show what is saved as a
  summary with "Edit settings"; the form opens in its place, in the same
  columns, with Save and Cancel, and closes on a save, so the page always
  says what is saved. With nothing saved the form shows at once, without
  Cancel. What depends on the saved settings, e.g. a test message, waits
  while the form is open. A form with several groups titles each in the
  right column (`FieldSet` and `FieldLegend`), e.g. Connection, Sign-in,
  Sender.
- Connected accounts lists each account in a row: on the left its name,
  whole, wrapping (an address at its seams), a pencil beside it (a ghost
  icon button, tooltip "Rename"), its badge, its address under a chosen
  name, and one line on where it stands; on the right its buttons, which
  wrap under the text on a narrow page. The pencil opens the name form in
  place of the name, under the dot and the address, which say whose name
  it is ("Name (optional)", explained in the field: a name
  only the member sees, never sent to recipients, unlike Gmail's sender
  name; it shows in mail instead of the address, and an empty one goes
  back to the address), with Save and Cancel; Escape cancels, and focus
  returns to the pencil. Check now keeps turning ("Checking...") until
  the list shows that check's outcome, at most 30 s, rather than
  announcing a start: the row's own line then says it, and a screen
  reader hears that line once, politely ("Personal: Up to date, checked
  at 8:09 am."), since the button only reads its own name again.
- A setting with one change at a time, e.g. Registration, states what it is
  in a sentence (and a badge beside the title) and offers one button for
  the change that fits, e.g. Open registration or Close registration. A
  change the server does not allow now has no button; a sentence says why
  and a link leads to the fix, e.g. "Go to System email".
- A page that is one list, e.g. Members, fills the window
  (`SettingsPage height="fill"`, `SettingsSection layout="fill"`): its title
  and section heading stay, and the list scrolls in the height left, down
  to 12 rem, below which the page scrolls too.
- A table scrolls inside one box both ways, focusable while it scrolls so
  the keyboard can scroll it (no Tab stop when it fits), named for what
  scrolls ("Member list"), with a fade at its right edge while columns lie
  beyond it, e.g. a row's action on a phone, and at its bottom while rows
  do. Its header stays at the top and its first column, the one that says
  whose row it is, stays at the left; each draws its own edge, since a
  table's borders scroll with its rows, and the first column's edge shows
  only while the rest scroll sideways. A long address wraps at its seams
  (before "@" and before dots) and breaks anywhere only when a part cannot
  fit. A row's action is a button until a row has more than one; then a
  menu. The member list is not paged: an inbok is a household or a team.
- A QR code for a phone app also links its address (e.g. "Add to
  authenticator app", `otpauth://`) on a narrow page or a touch pointer: a
  phone cannot scan its own screen.
- Below 1024 px the settings sidebar is a drawer, as mail's is.
- Signed-out pages (sign-in, setup, registration, password reset, email
  verification): the `muted` surface with one column about 400 px wide,
  centred both ways. The wordmark sits above a `card` holding a centred
  page title, a one-line lead, then the form or message; a link out of the
  flow, e.g. "No account yet? Create an account", sits under the card
  (`SignedOutPage`). "Forgot password?" sits beside the Password label.

## Patterns

### Forms

- Build fields with the shared `createFormHook` components (`useAppForm`
  in `src/components/form/`: a field kind per control, e.g. email and
  password, plus the form and its submit button; a new kind joins with its
  first form). They own the label, description, error, `aria-describedby`,
  and the lock while the form sends; the form owns focus after a submit.
- Schemas extend the generated Zod schemas; messages come from
  `src/api/validation.ts`. An email field takes what the
  server takes, an @ with text on both sides (`emailAddress`), never Zod's
  stricter `email()`; a refused address reads "Enter an email address,
  like name@example.com.", from the web and the server alike. Fields set
  no `maxLength`, which would cut a pasted value without a word; the
  length error says it instead. Forms set `noValidate`.
- Label above (the message editor's rows put it on the left); description
  before the control; error under it, with an icon and words. "(optional)"
  marks optional fields; required ones are unmarked. A link or button about
  the field sits at the end of the label's line in small text, e.g. "Forgot
  password?" or "Use a backup code", and comes after the control in Tab
  order, so Tab from one field reaches the next, not the action.
- A one-time code is one box per character in two halves; it takes a
  paste and the browser's one-time-code fill.
- A number that names something, e.g. a port, is a text box with a number
  keyboard (`inputMode="numeric"`), short; `type="number"` is for amounts,
  since its arrow keys and wheel change the value.
- Another service's username and password, e.g. a mail server's, keep
  password managers out (`passwordManagers="ignore"`), so they neither
  fill in the member's inbok sign-in nor save these as one. Browsers' own
  managers have no such switch and may still offer to save them.
- Errors appear after the first submit, then update live. Focus moves to
  the first invalid field. A server refusal stays while its field holds
  the refused value, returns if the member types that value again, and
  keeps that value from being sent. A rule that spans fields is also in
  the form's schema, which refuses it first.
- The submit button names what it does while working ("Saving...") and
  ignores repeats. A second submit, e.g. "Save only" beside "Save and send
  test", is outlined and comes after the main one, which Enter in a field
  presses; while one sends, the other waits. A request failure shows under the button through
  `RequestError` (a reference only for faults). Success shows as what the
  save leads to: the settings view again, with its badge and a note under
  it naming any next step ("Settings saved. Send a test message to check
  them."), or an outcome screen; no form stays open to say "Saved".
- A 429 says when to try again, as a clock time rounded up to the minute
  ("Too many attempts. Try again after 2:32 pm."), with the weekday when it
  is another day, and "later" past six days; it stays while the member
  retypes. A time stays true while it is on screen, which "in 4 minutes"
  would not. Accepted: a wait under a minute reads as the next minute
  (only scripts reach the per-browser limit), and the text never turns
  into "You can try again now" (people meet only limits of 15 minutes or
  more). Times are English with the browser's 12 or 24 hour clock. A limit
  under a minute, e.g. Check now's 15 s, says it in words instead ("Work
  was checked moments ago. Check again in a few seconds."), in a grey
  notice: a clock time rounded up would overstate the wait.
- A page warns before leaving (`LeaveWarning`) only when leaving loses what
  can't be redone in moments: a long text (a reply), settings being edited
  (System email), backup codes that won't show again. The compose window
  comes along between pages, so it asks only as it closes, as the page
  unloads, and on a phone as its history entry is left. Sign-in, the code
  forms, setup, and the pages emailed links open don't: what they hold is
  quick to type again, and an emailed link still works from the email. In
  app the warning is a dialog; closing the tab gets the browser's
  question. Typing back what the form opened with counts as no change.
  Cancel on a form opened in place discards without asking: its name says
  so, and what it drops is settings, quick to enter again. Discard on a
  reply asks first when it would drop typed text, as leaving would: a
  reply is a long text.
- A lost answer to a save that replaces settings asks to save again
  ("Save again to make sure."): saving the same values twice is safe, so
  the form stays open and editable.
- One independent choice (theme, a switch) applies at once; related fields
  save together with Save.

### Outcome screens

When a signed-out page's form gives way to a result, e.g. "Check your
email", "This link no longer works", or "This sign-in has ended" on the
code step, the result becomes the page (`SignedOutPage`), in the same card
as the form pages and, like them, without an icon: the title says the
outcome.

- The title says what happened and takes focus, so a screen reader reads
  the outcome first. A short lead or paragraph says why and what next.
- An email address the outcome is about, e.g. where a link went, sits on
  a line of its own under the title, in the text colour (`AddressLine`):
  set inside a sentence, a long address wraps at its own hyphens and reads
  like hyphenated words. The sign-in notice after an account or password
  change shows it the same way.
- The ways on are full-width buttons (`OutcomeLinks`): the most useful
  primary, any other outlined. A button that only brings the form back,
  e.g. "Use a different email address" under "Check your email", stays
  outlined even alone: the way on is the inbox. A failed check's "Try
  again" is its way on, so primary. A link out of the flow, e.g. "Back to
  sign in", stays under the card.
- Showing the form again, e.g. "Use a different email address", keeps
  what was typed and puts focus in the field.

### Feedback

- **Reversible actions** (archive, delete to Trash, read, star, and bulk
  versions) happen at once, with the unread counts; an action that moves
  a conversation to another place, any action on several, and every
  label change (a row shows at most two labels, so its result can be
  out of view)
  shows the **Undo bar** at once, docked
  at the bottom of the list, under the row the action took away, and on
  a phone, one pane at a time, under whichever pane shows, as an action
  can keep the conversation open there: what happened,
  the conversation's subject, or how many ("12 conversations"), Undo in
  the primary blue, and Close. It is
  part of the layout, so it covers nothing, and Tab reaches it from the
  list; it is a landmark named "Undo", which F6 reaches, and Ctrl or
  Cmd+Z runs its Undo from anywhere but a text field or an open menu or
  dialog, as Undo's tooltip says ("Undo (Ctrl+Z)"). However it closes,
  by Undo, Close, its countdown, or its action's refusal, focus inside it
  goes back to where it came from, a list's row rather than the row's
  star, else to the list's heading. Screen readers hear it as it
  appears, through a live region always in the page, with where the
  conversation went ("Find it in Trash."), and "Undone: <subject>." once
  an Undo has run; Undo's description names the conversation. Error
  toasts stand above it while it shows. In the list's width, Undo and
  Close stay near its words. Under 18em wide, e.g. a phone
  at large text, its buttons wrap under the words, so the subject keeps
  the width. It lasts 10 s, a line along its top draining as they pass,
  paused while the pointer or focus is on it and while the tab is
  hidden, and running on through a theme change; the next action's Undo
  takes its place, and opening another conversation leaves it. Undo
  applies the server's changes for the conversation's row and opens
  nothing: the API answers each action with the changes that undo it,
  and Undo sends those, after the action's answer, so it never reaches
  Gmail first. They restore the row, not each message: a message deleted
  on its own before a Delete comes back out of Trash with the rest, and
  undoing a Delete puts the member's own replies in the Inbox too
  (DEBT.md, "Undo per message"). One conversation's result that stays in
  view, e.g. a star or Mark read, gets no Undo; several conversations'
  do, as which of them it changed is no longer in view. A bulk action
  Gmail refuses for some conversations rolls back only those, the Undo
  bar's count lowered to what changed (closed when nothing did), and one
  error toast says why: "Couldn't archive 3 conversations", a sentence
  per reason (the accounts to reconnect named, the gone and failed
  counted), and Show details, a disclosure listing the conversations by
  reason, with their accounts when they span several. A newer answer
  that changed again what failed on every conversation an older
  failure's toast named closes that toast: Hiring's removal working
  leaves Acme's failure said, and a move, e.g. Move to Inbox, covers
  every place. Several conversations go in requests of at most 25 (the
  contract's limit, what Gmail changes within the server's budget), one
  after another, every guess showing at once.
- **Irreversible actions** (disconnect, deactivate, discard typed text)
  ask first in an `AlertDialog`: the title asks, the body states the
  consequence and what is kept, the buttons name the actions ("Disconnect
  Studio"), focus starts on the safe one. The dialog holds the change until
  the server answers (`ConfirmDialog`): its button turns and says what is
  happening ("Deactivating..."), Cancel and Escape wait, it closes on
  success, and a failure or a lost answer shows inside it, where pressing
  again retries. Focus then returns to the button that opened it, or, when
  the change removed that button, to what took its place, e.g. the next
  account's pencil after a disconnect. Members, Registration, and
  Disconnect on Connected accounts do this; the sign-outs on Security,
  Send again, and cancelling two-factor setup still close at once and
  report on the page (Not now). One that already asks for a code
  or a password, e.g. turning off two-factor authentication, confirms with
  it on the page instead: the red button opens the field, the consequence
  sits above it, the submit is red (`SubmitButton variant="destructive"`),
  and a way back keeps things as they are.
- **A failed action** rolls back and shows an error toast that stays until
  dismissed, with the fix: a reconnect sends the member to Connected
  accounts.
- **Toasts** report failures, and stay until closed; an action's Undo is
  the Undo bar's, not a toast's. Form
  results stay inline. Nothing important lives only in a toast.
- **Banners** for conditions that last (`Banner`: an icon, a sentence, and,
  where something fixes it, a link to it, tinted, with a line in its tone
  at 35% under it so stacked banners stay apart; the line only separates,
  so it needs no contrast check): an account problem above that account's
  mail; at the top of the content of every signed-in page, stacked, offline
  (`warning`), syncing stopped on this inbok (`warning`: "inbok has stopped
  getting new mail for every account.", then "Ask your administrator.", or
  for an administrator "Check that its worker is running."), then each
  setup step the member owes (`info`): system email
  for an administrator (nothing saved: "Finish setting up system email:
  until it works, members can't reset a password or verify their
  address."; no passing test: "Finish setting up system email: until a
  test message passes, registration can't open."), then an unverified
  address. Each banner follows one fact from the server and none waits for
  another: what a step can't do yet, e.g. a verification link while nothing
  is saved, the server refuses and says why. A banner stays on every page
  until its step is done; on the page where it is done it keeps its
  sentence and drops its link, which would lead to itself. The setup
  banners sit in a "Setup reminders" landmark: they come before the heading
  a page focuses, so a screen reader finds them there. For the same
  reason offline and syncing stopped are each a landmark of their own
  ("Offline", "Mail sync stopped") while they show, as well as announced
  as they appear. The system
  email step reflects the last test, not whether mail sent since arrives:
  the server records only tests (Not now). Signed-out pages show only
  offline.
- Every mail action has its words once, in `features/mail/words.ts`
  (label, icon, failure, and for an action that moves a conversation its
  result and where it went), a `Record` over the API's actions, so a new
  one fails the build until it has words; `perform` runs an action with
  its guess, counts, Undo bar, and error toast; toolbars, menus, the
  picked-rows bar, the Undo bar, and toasts read from them.

### States

- **Loading:** routes load data in `loader` with the query client's
  `query` (`ensureQueryData` is deprecated), every one through
  `cachedRead`: a cached read serves until the member's own change marks
  it out of date, and a visit back then waits for that reread instead of
  drawing the page as it was (web/README.md "Routes"); a page visited
  offline draws what it has under the offline banner. The page's own read
  then refreshes older data in place. `pendingComponent` appears after
  300 ms and stays at least 500 ms (on the first load it is not held, so a
  quick one shows nothing): a list's own as placeholder rows shaped like
  real ones, any other page's the router's default, a spinner with
  "Loading...". Background re-reads show nothing. Hovering or focusing a
  row preloads its conversation, and the reading pane starts from the list
  row's data.
- **Empty:** words only, the cause and one next step: "Connect your first
  account" with Connect Gmail, or, with no Google client, "Gmail can't be
  connected yet" with a next step for whoever reads it (a member asks
  their administrator; an administrator adds the client); "Nothing in
  Inbox", also under an account
  filter, whose header already names the account; "Nothing in Projects",
  a label by its own name, its parents in the header; "Studio is still
  syncing. Its mail appears here as it is copied."; "No spam"; "Trash is
  empty".
- **Failure:** what failed, that nothing was lost, Try again, a reference
  for faults. A failed refresh keeps the list and adds a banner ("Couldn't
  refresh. inbok isn't answering; showing mail from 10:42."). A failed
  older page keeps the rows shown and stops loading, with "Couldn't load
  older conversations." and Try again under the list. A real "Page
  not found" and a crash screen with Reload live in the shell.

### Status badges

One `badge`, a soft tint, always an icon and a word: `StatusBadge`
(`src/components/status-badge.tsx`) takes the tone, the icon, and the word.
The tone says what the status asks of the member:

| Tone        | Asks         | Used for                                                                                                 |
| ----------- | ------------ | -------------------------------------------------------------------------------------------------------- |
| Neutral     | Nothing      | Waiting, Tried again, Waiting to sync, Not verified, Admin, You, Deactivated, This browser, Open, Closed |
| Info        | To wait      | Sending, Syncing (spinner)                                                                               |
| Success     | Nothing more | Sent, Up to date, Verified, Two-factor on, Tested                                                        |
| Warning     | A look       | Not confirmed, Needs reconnecting, Reconnect, Not set up, Paused                                         |
| Destructive | A fix        | Not sent, Sync failed                                                                                    |

Deactivated, Open, and Closed are an admin's choice, not a fault, so
neutral; Paused (registration opened, but system email untested) asks for
a look. Up to date shows only on the page whose job is that status
(Connected accounts); elsewhere the usual state shows nothing. Members
marks only what differs from an ordinary member, You, Admin, Not
verified, Deactivated, and writes Active as plain grey text in its Status
column. Two-factor on shows on Security and the two-factor page. Tested,
Not confirmed, and Not set up show beside System email's title: whether
inbok can send its own email, which sign-in codes, password resets, and
Registration need. The sidebar shows an account's Syncing, Reconnect, or
Sync failed as the icon alone, in its tone, the words read to screen
readers and shown on hover, and with the icon on a line under the name
for a touch.

A line of text with no room for a pill, e.g. an Outbox row's third line,
shows the status inline: `StatusWord`, its 14 px icon and its word in
500 weight in the tone's colour (`muted-foreground` for neutral), which
holds 4.5:1 on a row's surface, hovered and selected, in both themes.

## Data for mail

Mail lists, the reading pane, and mail actions use TanStack DB collections
over the generated client functions; everything else uses TanStack Query
hooks. A message's content is the exception inside the reading pane: it
never changes, so each open message reads it with its generated Query
hook, cached for good (`messages/content.ts`). Rules from the spike,
confirmed against the real API in branch 3's gate (ADR031, "TanStack DB
gate"):

- Sort by the date as a number (`dateMs`), then the ID
  (`stringSort: "lexical"`); dates are never null; one fixed order. The
  API's date text does not sort by time.
- Refresh with inbok's own timer that re-reads only each open list's
  newest page; the built-in `refetchInterval` re-reads every loaded page.
- Apply the "N new conversations" cut-off when drawing, never in the query.
- One live query per view, each with an explicit key; no joins in paged
  lists (look account details up instead); a conversation's views are
  boolean flags, and a label list tests `labelIds`
  (`inArray(label, labelIds)`), so rows keep it.
- Each page asks for one row more than it shows, so the next load of that
  date's ties needs no request; "older than a date" is asked as the date
  alone, without an ID.
- A list never reads its whole place: the data layer refuses it, and
  TanStack DB is patched so a row leaving a list before its first page
  answers costs a page read again (ADR031, TanStack DB gate, rule 9). A
  list that cannot read its rows in order says so, with Try again, never
  that its place is empty.
- Action handlers store the rows the server returns (`writeUpsert`) instead
  of refetching; an action whose guess changes nothing sends its request
  itself, since TanStack DB skips a transaction without changes. A list
  read in flight never lands over those rows: TanStack DB keeps a direct
  write over a read that started before it.
- Which places a conversation is in comes from one table of flags
  (`rules.ts`), which both filters the lists (`placeWhere`) and guesses
  an action's unread counts (`countChanges`), so a count and its list
  agree. The counts change at once, in the action's transaction; the
  answer's counts go into List Places' query cache, after cancelling a
  read of them in flight, which that cache would let land over them.
- The reading pane reads its row by ID (`eq(id)`): the list's, or loaded
  from Get Conversation through the shared query cache, which joins the
  pane's own read, so a link costs no extra request. That load holds the
  row while the pane is open, after its list drops it. A failed load gives
  no row, never the collection's error, which every list would show. It is
  read again with each list's newest page, every 30 s, when the tab shows
  again, and back online, so a row the pane holds after its list let it
  go is as current as the list.

## Wording

- Plain and exact, in the second person. "inbok" is lowercase, even at the
  start of a sentence.
- Sentence case. Buttons are a verb and what it acts on, and the verb
  carries into the result ("Archive", "Conversation archived"). Gmail's
  own names win where members know them: "Not spam". The Undo bar's
  title is the result alone ("Archived"), as its second line names the
  conversation; Delete's reads "Moved to Trash", as Gmail says, since it
  moves the conversation rather than deleting it for good.
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

Recorded for later, not built: the search's filter sentence scrolling
away with its results, which at 200% text fills most of a short list
pane before the first result (the results' own box scrolls, so the
sentence would have to move inside it); a result's mark past where its
subject or snippet is cut short, which shows only once the row is
opened, as in Gmail's list; app keyboard shortcuts, a command palette,
a multi-account filter, a single-message view option, list density and
reading-pane position settings, sender pictures (BIMI logos after DMARC,
contact photos), merging one email received by two accounts, swipe
gestures, drafts beyond the tab, an installable app with offline mail,
per-member theme storage, folds kept across devices (per member, on the
server), a Preferences settings page (the theme, the only preference,
lives in the member menu), the remaining confirmations holding their
change in the dialog (`ConfirmDialog`) as Members and Registration do, a
setup reminder when system email stops delivering (the server would first
have to record failed sends), dismissing a banner. Pull-to-refresh and a
top bar that hides while scrolling down: Check for new mail is one tap in
the top bar, both gestures need hand-written touch handling over the
Virtualizer's scrolling box that only a real phone can check, and a
native app gets both from its platform (ADR031, branch 3 checkpoint 5).

App keyboard shortcuts, once built, go on TanStack Hotkeys (`useHotkey`,
its `Mod` key, and its display formatter), which then also takes the
sidebar's Ctrl+B, Send's Ctrl+Enter, and the Undo bar's Ctrl+Z. Until
then those three are plain key handlers, the two letter ones sharing one
check of what the key may reach (`isShortcut` in `src/lib/shortcut.ts`):
the library was in beta in 2026-11, and three shortcuts did not need it.
