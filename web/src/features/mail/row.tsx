import {
  createContext,
  Fragment,
  memo,
  useContext,
  useSyncExternalStore,
  type MouseEvent,
  type PointerEvent,
  type ReactElement,
  type ReactNode,
} from "react";
import { cn } from "@/lib/cn";
import { CheckboxContext, ToggleButton, useSlottedContext } from "react-aria-components";
import { CheckIcon, StarIcon } from "lucide-react";
import { accountBorder, accountName, type AccountColor } from "@/components/account-mark";
import { Badge } from "@/components/ui/badge";
import { GridListItem } from "@/components/ui/grid-list";
import type { SearchMatchOutput } from "@/api/generated/model/searchMatch.zod";
import type { AppHref } from "./places";
import type { Conversation } from "./conversations";
import { fullDate, listDate } from "./dates";
import { Marked } from "./marks";
import { senders } from "./people";
import { SenderAvatar } from "./sender-avatar";
import { RowLabelsContext, type RowLabel } from "./labels";
import { inboxChip, messageCount, moreMarks, rowName, subjectText, unknownSender } from "./text";
import { actionWords } from "./words";

// Picks is what a list lets its rows do: off, no picking, e.g. a search's
// results, whose rows span places that offer different actions; ready,
// rows can be picked and none is; picking, some are, so every row's box
// shows.
export type Picks = "off" | "ready" | "picking";

// PickingContext says what the list lets its rows do, off unless it says
// so. A context, not a prop: the rows a list has drawn stay as drawn, and
// only their boxes need to know.
export const PickingContext = createContext<Picks>("off");

// RowAccount is what a row shows of its conversation's account.
export type RowAccount = { color: AccountColor; email: string; name: string | null };

// rowHeight returns every row's height in pixels: 4.75 rem, three 20 px
// lines and 8 px above and below at the browser's usual 16 px text
// (DESIGN.md "Space, size, shape, motion"). In rem, so a member who sets a
// larger text size gets taller rows rather than clipped text; the
// Virtualizer places rows by pixels.
export function rowHeight(): number {
  return 4.75 * Number.parseFloat(getComputedStyle(document.documentElement).fontSize);
}

// headingHeight returns a list section heading's height in pixels: 2 rem,
// 32 px at the browser's usual 16 px text, a nav row's (DESIGN.md "List
// sections"); in rem as rowHeight is.
export function headingHeight(): number {
  return 2 * Number.parseFloat(getComputedStyle(document.documentElement).fontSize);
}

// ConversationRow is one conversation in a list (DESIGN.md "Message
// list"): its senders, count, and date; its subject; its preview, an
// Inbox chip while it is in the Inbox and markInbox is set, e.g. in All
// mail, and its star; led by its newest sender's initials, ringed in
// account's colour unless the list shows one account (ringed false), which
// becomes its checkbox while the row is hovered, focused, or picked, and
// on every row while the list is picking (PickingContext; DESIGN.md
// "Message list").
// href opens it, with fromList in its history state as the reading
// pane's Back needs it; openStore says which conversation the reading
// pane shows; now is the day its date is shown at; onRest and onLeave run as
// the pointer comes to rest on it and leaves, to read it ahead; onStar
// stars or unstars it. match, for a search's result, is the message that
// matched best, whose sender, date, subject, and text around the match the
// row shows in place of the conversation's, with the matched words marked
// (DESIGN.md "Message list").
export function ConversationRow({
  conversation,
  match,
  account,
  ringed,
  markInbox,
  href,
  openStore,
  now,
  onRest,
  onLeave,
  onStar,
}: {
  conversation: Conversation;
  match?: SearchMatchOutput;
  account: RowAccount | undefined;
  ringed: boolean;
  markInbox: boolean;
  href: AppHref;
  openStore: OpenStore;
  now: Date;
  onRest: () => void;
  onLeave: () => void;
  onStar: (starred: boolean) => void;
}): ReactElement {
  const open = useIsOpen(openStore, conversation.id);
  // Opening a conversation while another is open replaces that one in the
  // history: choosing in the reading pane is not a new page, so Back, the
  // phone's or the browser's, returns to the list rather than through each
  // conversation. Read as the row opens: React Aria draws a row once and
  // keeps it, so a value set then would be stale (the reason for
  // openStore).
  const replaces = () => openStore.get() !== undefined;
  const names = senders(match ? match.from : conversation.participants, account?.email);
  const who = names.join(", ") || unknownSender;
  const dateText = match ? match.date : conversation.date;
  const date = match ? new Date(match.date) : new Date(conversation.dateMs);
  const shownDate = listDate(date, now);
  const subject = subjectText(match ? match.subject : conversation.subject);
  const count = conversation.messageCount > 1 ? conversation.messageCount : undefined;
  const inInbox = markInbox && conversation.inbox;
  const { labels: known, leaveOut } = useContext(RowLabelsContext);
  // Its labels in the sidebar's order, but the list's own.
  const labels = conversation.labelIds
    .filter((place) => place !== leaveOut)
    .flatMap((place) => known.get(place) ?? [])
    // oxlint-disable-next-line unicorn/no-array-sort -- a new array; tsconfig's lib has no toSorted.
    .sort((a, b) => a.order - b.order);

  return (
    <GridListItem
      id={conversation.id}
      href={{
        ...href,
        // Whether the entry before the conversation's is this list, so the
        // reading pane's Back can go back through history: so when the row
        // adds an entry after the list's; when it replaces an open
        // conversation's, only if that entry's was. The router runs this
        // function as it navigates, with the state of the entry it leaves
        // or replaces.
        state: (left) => ({ fromList: !replaces() || left.fromList === true }),
      }}
      // A getter, so replaces runs at the press.
      // NOTE: React Aria documents only that routerOptions reaches the
      // RouterProvider's navigate; that it passes this object on uncopied,
      // so the getter runs at the press, is what react-aria 3.53 does. The
      // history test in list.browser.test.tsx fails if that changes.
      routerOptions={{
        get replace() {
          return replaces();
        },
      }}
      // A screen reader names the row by this, not by its whole text.
      textValue={rowName({
        who,
        count,
        subject,
        date: shownDate,
        unread: conversation.unread,
        inbox: inInbox,
        labels: labels.map((label) => label.path),
        starred: conversation.starred,
        // The ring is a colour alone, so the name says the account.
        account: ringed && account ? accountName(account) : undefined,
      })}
      current={open}
      onHoverStart={onRest}
      onHoverEnd={onLeave}
      // The list reads the conversation ahead when focus rests here.
      data-conversation={conversation.id}
      className={cn(
        "group/row h-full gap-3 border-b px-4 py-2",
        // An unread row's mark: a 3 px bar in primary at its leading edge,
        // the text's colour in forced colours, which drop the others.
        conversation.unread &&
          "before:absolute before:inset-y-0 before:start-0 before:w-0.75 before:bg-primary forced-colors:before:bg-[CanvasText]",
      )}
    >
      {({ isSelected }) => (
        <>
          {/* 36 px, its top on the first line's and its left edge on the
              list heading's (16 px); 12 px to the text, less than the
              edge, so it reads with its text. The ring is drawn inside
              its edge. */}
          <PickBox picked={isSelected} ring={ringed && account ? account.color : undefined}>
            <SenderAvatar
              sender={(match ? match.from : conversation.latestFrom)[0]}
              ring={ringed && account ? account.color : undefined}
            />
          </PickBox>
          <RowText
            conversation={conversation}
            match={match}
            names={names}
            who={who}
            count={count}
            date={date}
            dateText={dateText}
            shownDate={shownDate}
            subject={subject}
            inInbox={inInbox}
            labels={labels}
            onStar={onStar}
          />
        </>
      )}
    </GridListItem>
  );
}

// RowText is a row's three lines: who, count, and date; subject; and
// preview, the Inbox chip while inInbox, and the star, which onStar sets;
// match's own, words marked, for a search's result.
// Memoized: React Aria draws a row again at each selection, focus, or
// press change, while its text stays.
const RowText = memo(function RowText({
  conversation,
  match,
  names,
  who,
  count,
  date,
  dateText,
  shownDate,
  subject,
  inInbox,
  labels,
  onStar,
}: {
  conversation: Conversation;
  match: SearchMatchOutput | undefined;
  names: string[];
  who: string;
  count: number | undefined;
  date: Date;
  dateText: string;
  shownDate: string;
  subject: string;
  inInbox: boolean;
  labels: readonly RowLabel[];
  onStar: (starred: boolean) => void;
}): ReactElement {
  return (
    <div
      className={cn(
        "flex min-w-0 flex-1 flex-col text-sm leading-5",
        // Unread is weight, heavier in dark, where light text on dark
        // looks bolder at any weight; a read row's text is a step
        // quieter, so the two differ in brightness too.
        conversation.unread ? "font-semibold dark:font-[650]" : "text-read-foreground",
      )}
    >
      {/* One 20 px line, whatever the smaller texts' own line height.
            dir="auto" sets each text's direction by its first letter, so
            a right-to-left name or subject is cut at its own end. Several
            senders read in the page's direction, in order, each name in its own
            direction (bdi), so a right-to-left first name can't turn the
            whole list around. */}
      <div className="flex h-5 items-baseline gap-2">
        <span dir={names.length > 1 ? undefined : "auto"} className="truncate">
          {names.length > 1
            ? names.map((name, i) => (
                // oxlint-disable-next-line react/no-array-index-key -- a sender listed twice has the same name; the list never reorders.
                <Fragment key={`${i} ${name}`}>
                  {i > 0 && ", "}
                  <bdi>{name}</bdi>
                </Fragment>
              ))
            : who}
        </span>
        {count !== undefined && (
          <span className="shrink-0 text-xs leading-5 text-muted-foreground">
            {messageCount(count)}
          </span>
        )}
        <time
          dateTime={dateText}
          title={fullDate(date)}
          className={cn(
            "ml-auto shrink-0 text-xs leading-5 tabular-nums",
            !conversation.unread && "text-muted-foreground",
          )}
        >
          {shownDate}
        </time>
      </div>
      <span dir="auto" className="truncate">
        {match && match.subject.trim() !== "" ? (
          <Marked text={match.subject} spans={match.subjectMatches} />
        ) : (
          subject
        )}
      </span>
      {/* The chip and the star end the preview's line, the row's marks
            beside its date, so the text above starts where it does. Its
            width decides how many marks show (RowMarks). */}
      <div className="@container flex h-5 items-center gap-1.5">
        <p
          dir="auto"
          className="min-w-0 flex-1 truncate text-snippet font-normal text-muted-foreground"
        >
          {match ? (
            <Marked text={match.preview} spans={match.previewMatches} />
          ) : (
            conversation.preview
          )}
        </p>
        <RowMarks inbox={inInbox} labels={labels} />
        <StarButton starred={conversation.starred} onStar={onStar} />
      </div>
    </div>
  );
});

// RowMarks ends a row's preview line with where its conversation is: the
// Inbox chip while inbox, then its labels by their row names, then how
// many more, the full paths on hover, in at most 60% of the line. A row
// is one line, so a chip is cut rather than wrapped, at 6rem, or shorter
// to fit, down to 3.5rem, a short word: as the line narrows below what
// that floor needs, it shows two marks, then one, then none, each with
// how many more; one at most where the list says so (RowLabelsContext). Screen readers hear every label in the row's name
// (rowName), and the reading pane shows them whole.
//
// DESIGN: the line's width picks the marks in CSS, so no row measures
// itself. A step shows where its marks fit 60% of the line at their
// floor, with "+N" (2.25rem) and the gaps: two chips need 9.75rem, so a
// 16.25rem line; one needs 6rem, so a 10rem line. In rem, so it holds at
// any text size.
function RowMarks({
  inbox,
  labels,
}: {
  inbox: boolean;
  labels: readonly RowLabel[];
}): ReactElement | null {
  const marks = [
    ...(inbox ? [{ key: "inbox", name: inboxChip, path: inboxChip }] : []),
    ...labels.map((label) => ({ key: label.path, name: label.name, path: label.path })),
  ];
  // Search results show one at most: their snippet says why each is
  // there.
  const { most = 2 } = useContext(RowLabelsContext);
  if (marks.length === 0) return null;
  // 16 px, the meta text's line and the star's icon, so 2 px stay between
  // it and the subject above. A hovered or open row's surface is the
  // chip's own fill, so there it takes the page's, to keep its shape.
  const chip = "h-4 px-1.5 py-0 in-aria-[current=page]:bg-background in-data-hovered:bg-background";
  return (
    <span
      // The row's name says them all; the hover's title repeats it.
      aria-hidden="true"
      title={marks.map((mark) => mark.path).join(", ")}
      className="flex max-w-[60%] min-w-0 shrink-0 items-center gap-1"
    >
      <Badge
        variant="secondary"
        className={cn(chip, "hidden max-w-24 min-w-14 shrink @min-[10rem]:inline-flex")}
      >
        <span dir="auto" translate="no" className="truncate">
          {marks[0].name}
        </span>
      </Badge>
      {most === 2 && marks.length > 1 && (
        <Badge
          variant="secondary"
          className={cn(chip, "hidden max-w-24 min-w-14 shrink @min-[16.25rem]:inline-flex")}
        >
          <span dir="auto" translate="no" className="truncate">
            {marks[1].name}
          </span>
        </Badge>
      )}
      {most === 2 && marks.length > 2 && (
        <Badge
          variant="secondary"
          className={cn(chip, "hidden tabular-nums @min-[16.25rem]:inline-flex")}
        >
          {moreMarks(marks.length - 2)}
        </Badge>
      )}
      {marks.length > 1 && (
        <Badge
          variant="secondary"
          className={cn(
            chip,
            "hidden tabular-nums",
            most === 2 ? "@min-[10rem]:@max-[16.25rem]:inline-flex" : "@min-[10rem]:inline-flex",
          )}
        >
          {moreMarks(marks.length - 1)}
        </Badge>
      )}
    </span>
  );
}

// PickBox is a row's leading 36 px, in a list that picks: children, its
// avatar, or its
// checkbox, ticked while picked, inside ring, the avatar's account colour,
// when given, which shows while picking or picked, and
// while the row is hovered or focused by the keyboard. A press on it picks
// or unpicks the row, even while nothing is picked, when a press elsewhere
// on the row opens it; with Shift while picking, it picks every row from
// the last one picked, as a Shift press on the row does.
//
// DESIGN: the row is what assistive technology and the keyboard reach,
// selected or not (Space picks it), so the box is only drawn. It keeps a
// plain press from the row, which would open the conversation, and picks
// through the row's selection checkbox slot, as React Aria's own checkbox
// would; a Shift press goes on to the row, whose selection keeps React
// Aria's anchor for ranges. The avatar and the box are both always drawn,
// one hidden by the row's state: swapping the elements as hover began,
// during a press, left the browser no element to click.
function PickBox({
  picked,
  ring,
  children,
}: {
  picked: boolean;
  ring: AccountColor | undefined;
  children: ReactNode;
}): ReactElement {
  const picks = useContext(PickingContext);
  const selection = useSlottedContext(CheckboxContext, "selection");
  const picking = picks === "picking";
  const range = (event: PointerEvent | MouseEvent) => picking && event.shiftKey;
  const onClick = (event: MouseEvent) => {
    if (range(event)) return;
    event.stopPropagation();
    selection?.onChange?.(!picked);
  };
  const boxed = picking || picked;
  // A list that does not pick keeps the avatar alone.
  if (picks === "off") return <>{children}</>;
  return (
    // oxlint-disable-next-line jsx-a11y/no-static-element-interactions, jsx-a11y/click-events-have-key-events -- the row is the control: Space picks it; this is the pointer's way.
    <span
      data-slot="pick-box"
      // A touch screen's target reaches 4 px past it, 44 px, as the
      // star's does, without moving anything.
      className="relative flex size-9 shrink-0 items-center justify-center pointer-coarse:after:absolute pointer-coarse:after:-inset-1"
      onPointerDown={(event) => {
        if (!range(event)) event.stopPropagation();
      }}
      onClick={onClick}
    >
      <span
        className={cn(
          boxed
            ? "hidden"
            : "contents group-data-focus-visible/row:hidden group-data-hovered/row:hidden",
        )}
      >
        {children}
      </span>
      {/* The checkbox's box (ui/checkbox.tsx), its border a step stronger
          over a hovered row's fill. */}
      <span
        aria-hidden="true"
        className={cn(
          "size-4 items-center justify-center rounded-sm border bg-card transition-colors [&>svg]:size-3.5",
          boxed ? "flex" : "hidden group-data-focus-visible/row:flex group-data-hovered/row:flex",
          picked
            ? "border-primary bg-primary text-primary-foreground"
            : "border-input group-data-hovered/row:border-muted-foreground",
        )}
      >
        {picked && <CheckIcon />}
      </span>
      {ring !== undefined && (
        // The avatar's account ring stays around the box, as the avatar
        // draws it (DESIGN.md "Message list"), so a list of several
        // accounts still says whose each row is while picking.
        <span
          aria-hidden="true"
          className={cn(
            "pointer-events-none absolute inset-0 rounded-full border-2",
            accountBorder(ring),
            boxed
              ? "block"
              : "hidden group-data-focus-visible/row:block group-data-hovered/row:block",
          )}
        />
      )}
    </span>
  );
}

// StarButton stars a row (DESIGN.md "Message list"), on every row, as
// Gmail's list and app show it: outlined, then filled in the text colour
// once starred.
//
// DESIGN: 24 px, the smallest target WCAG 2.2 allows (2.5.8), so it fits
// its 20 px line with 2 px to spare under the subject's letters; DESIGN.md
// lets a control in a list row be this size. A touch screen's target
// reaches 8 px past it, without moving anything. No tooltip: its icon and
// pressed state are its meaning, and one popping up over the rows below
// as the pointer drifts would only get in the way.
function StarButton({
  starred,
  onStar,
}: {
  starred: boolean;
  onStar: (starred: boolean) => void;
}): ReactElement {
  return (
    <ToggleButton
      aria-label={actionWords.star.label}
      isSelected={starred}
      onChange={onStar}
      className={cn(
        // 2 px past its 20 px line above and below, and 4 px into the
        // row's padding, which puts its icon over the line's last 16 px.
        "relative -my-0.5 -mr-1 flex size-6 shrink-0 items-center justify-center rounded-md transition-colors outline-none data-focus-visible:focus-ring-inset data-hovered:text-foreground pointer-coarse:after:absolute pointer-coarse:after:-inset-2",
        starred ? "text-foreground" : "text-muted-foreground",
      )}
    >
      <StarIcon aria-hidden="true" className={cn("size-4", starred && "fill-current")} />
    </ToggleButton>
  );
}

// OpenStore holds which conversation the reading pane shows, so a row asks
// only whether it is the one: opening another redraws two rows, not every
// row the list has loaded.
export type OpenStore = {
  get: () => string | undefined;
  set: (id: string | undefined) => void;
  subscribe: (listener: () => void) => () => void;
};

// createOpenStore returns an OpenStore holding open.
export function createOpenStore(open: string | undefined): OpenStore {
  let current = open;
  const listeners = new Set<() => void>();
  return {
    get: () => current,
    set: (id) => {
      if (id === current) return;
      current = id;
      for (const listener of listeners) listener();
    },
    subscribe: (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
  };
}

// useIsOpen reports whether store holds id, and redraws only when that
// answer changes.
export function useIsOpen(store: OpenStore, id: string): boolean {
  return useSyncExternalStore(store.subscribe, () => store.get() === id);
}
