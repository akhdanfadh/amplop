import { Fragment, useSyncExternalStore, type ReactElement } from "react";
import { cn } from "@/lib/cn";
import { ToggleButton } from "react-aria-components";
import { StarIcon } from "lucide-react";
import { accountName, type AccountColor } from "@/components/account-mark";
import { Badge } from "@/components/ui/badge";
import { GridListItem } from "@/components/ui/grid-list";
import type { AppHref } from "./places";
import type { Conversation } from "./conversations";
import { fullDate, listDate } from "./dates";
import { senders } from "./people";
import { SenderAvatar } from "./sender-avatar";
import { inboxChip, messageCount, rowName, subjectText, unknownSender } from "./text";
import { actionWords } from "./words";

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

// ConversationRow is one conversation in a list (DESIGN.md "Message
// list"): its senders, count, and date; its subject; its preview, an
// Inbox chip while it is in the Inbox and markInbox is set, e.g. in All
// mail, and its star; led by its newest sender's initials, ringed in
// account's colour unless the list shows one account (ringed false).
// href opens it, with fromList in its history state as the reading
// pane's Back needs it; openStore says which conversation the reading
// pane shows; now is the day its date is shown at; onRest and onLeave run as
// the pointer comes to rest on it and leaves, to read it ahead; onStar
// stars or unstars it.
export function ConversationRow({
  conversation,
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
  const names = senders(conversation.participants, account?.email);
  const who = names.join(", ") || unknownSender;
  const date = new Date(conversation.dateMs);
  const shownDate = listDate(date, now);
  const subject = subjectText(conversation.subject);
  const count = conversation.messageCount > 1 ? conversation.messageCount : undefined;
  const inInbox = markInbox && conversation.inbox;

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
        starred: conversation.starred,
        // The ring is a colour alone, so the name says the account.
        account: ringed && account ? accountName(account) : undefined,
      })}
      current={open}
      onHoverStart={onRest}
      onHoverEnd={onLeave}
      // The list reads the conversation ahead when focus rests here.
      data-conversation={conversation.id}
      className="h-full gap-3 border-b px-4 py-2"
    >
      {/* 36 px, its top on the first line's and its left edge on the list
          heading's (16 px); 12 px to the text, less than the edge, so it
          reads with its text. The ring is drawn inside its edge. */}
      <SenderAvatar
        sender={conversation.latestFrom[0]}
        ring={ringed && account ? account.color : undefined}
      />
      <div
        className={cn(
          "flex min-w-0 flex-1 flex-col text-sm leading-5",
          conversation.unread && "font-semibold",
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
            dateTime={conversation.date}
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
          {subject}
        </span>
        {/* The chip and the star end the preview's line, the row's marks
            beside its date, so the text above starts where it does. */}
        <div className="flex h-5 items-center gap-1.5">
          <p
            dir="auto"
            className="min-w-0 flex-1 truncate text-snippet font-normal text-muted-foreground"
          >
            {conversation.preview}
          </p>
          {inInbox && (
            // 16 px, the meta text's line and the star's icon, so 2 px
            // stay between it and the subject above. A hovered or open
            // row's surface is the chip's own fill, so there it takes the
            // page's, to keep its shape.
            <Badge
              variant="secondary"
              className="h-4 px-1.5 py-0 in-aria-[current=page]:bg-background in-data-hovered:bg-background"
            >
              {inboxChip}
            </Badge>
          )}
          <StarButton starred={conversation.starred} onStar={onStar} />
        </div>
      </div>
    </GridListItem>
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
