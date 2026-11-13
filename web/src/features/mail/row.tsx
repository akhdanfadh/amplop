import { Fragment, useSyncExternalStore, type ReactElement } from "react";
import { cn } from "@/lib/cn";
import { StarIcon, UserIcon } from "lucide-react";
import { accountName, accountRing, type AccountColor } from "@/components/account-mark";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { GridListItem } from "@/components/ui/grid-list";
import type { AppHref } from "./places";
import type { Conversation } from "./conversations";
import { fullDate, listDate } from "./dates";
import { initials, senders } from "./people";
import { messageCount, noSubject, rowName, unknownSender } from "./text";

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
// list"): its senders, count, and date; its subject; its preview, ended
// by a star while starred; led by its newest sender's initials, ringed in
// account's colour unless the list shows one account (ringed false). href
// opens it; openStore says whether it is the one in the reading pane; now
// is the day its date is shown at.
export function ConversationRow({
  conversation,
  account,
  ringed,
  href,
  openStore,
  now,
}: {
  conversation: Conversation;
  account: RowAccount | undefined;
  ringed: boolean;
  href: AppHref;
  openStore: OpenStore;
  now: Date;
}): ReactElement {
  const open = useIsOpen(openStore, conversation.id);
  const names = senders(conversation.participants, account?.email);
  const who = names.join(", ") || unknownSender;
  const date = new Date(conversation.dateMs);
  const shownDate = listDate(date, now);
  const subject = conversation.subject.trim() || noSubject;
  const count = conversation.messageCount > 1 ? conversation.messageCount : undefined;
  const letters = initials(conversation.latestFrom[0]);

  return (
    <GridListItem
      id={conversation.id}
      href={href}
      // Opening a conversation while another is open replaces that one in
      // the history: choosing in the reading pane is not a new page, so
      // Back, the phone's or the browser's, returns to the list rather than
      // through each conversation. A getter, read as the row opens: React
      // Aria draws a row once and keeps it, so a value set then would be
      // stale (the reason for openStore).
      // NOTE: React Aria documents only that routerOptions reaches the
      // RouterProvider's navigate; that it passes this object on uncopied,
      // so the getter runs at the press, is what react-aria 3.53 does. The
      // history test in list.browser.test.tsx fails if that changes.
      routerOptions={{
        get replace() {
          return openStore.get() !== undefined;
        },
      }}
      // A screen reader names the row by this, not by its whole text.
      textValue={rowName({
        who,
        count,
        subject,
        date: shownDate,
        unread: conversation.unread,
        starred: conversation.starred,
        // The ring is a colour alone, so the name says the account.
        account: ringed && account ? accountName(account) : undefined,
      })}
      current={open}
      className="h-full gap-3 border-b px-4 py-2"
    >
      {/* 36 px, its top on the first line's and its left edge on the list
          heading's (16 px); 12 px to the text, less than the edge, so it
          reads with its text. The ring is drawn inside its edge. */}
      <Avatar
        className={cn(
          "size-9",
          // Unringed, the avatar keeps its hairline edge, which marks the
          // muted disc on the page.
          ringed &&
            account && ["after:border-2 after:mix-blend-normal", accountRing(account.color)],
        )}
      >
        <AvatarFallback className="text-xs font-semibold">
          {letters || <UserIcon aria-hidden="true" className="size-4" />}
        </AvatarFallback>
      </Avatar>
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
        <div className="flex h-5 items-center gap-1.5">
          <p
            dir="auto"
            className="min-w-0 flex-1 truncate text-snippet font-normal text-muted-foreground"
          >
            {conversation.preview}
          </p>
          {/* The row's name already says it is starred. */}
          {conversation.starred && (
            <StarIcon aria-hidden="true" className="size-4 shrink-0 fill-current" />
          )}
        </div>
      </div>
    </GridListItem>
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
