import { Fragment, useSyncExternalStore, type ReactElement } from "react";
import { cn } from "@/lib/cn";
import { ToggleButton } from "react-aria-components";
import { StarIcon } from "lucide-react";
import { accountName, type AccountColor } from "@/components/account-mark";
import { GridListItem } from "@/components/ui/grid-list";
import type { AppHref } from "./places";
import type { Conversation } from "./conversations";
import { fullDate, listDate } from "./dates";
import { senders } from "./people";
import { SenderAvatar } from "./sender-avatar";
import { messageCount, rowName, starName, subjectText, unknownSender } from "./text";

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
// list"): its senders, count, and date; its subject; its preview and star;
// led by its newest sender's initials, ringed in account's colour unless
// the list shows one account (ringed false). href opens it; openStore
// says whether it is the one in the reading pane; now is the day its date
// is shown at; onRest and onLeave run as the pointer comes to rest on it
// and leaves, to read it ahead; onStar stars or unstars it.
export function ConversationRow({
  conversation,
  account,
  ringed,
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
  href: AppHref;
  openStore: OpenStore;
  now: Date;
  onRest: () => void;
  onLeave: () => void;
  onStar: (starred: boolean) => void;
}): ReactElement {
  const open = useIsOpen(openStore, conversation.id);
  const names = senders(conversation.participants, account?.email);
  const who = names.join(", ") || unknownSender;
  const date = new Date(conversation.dateMs);
  const shownDate = listDate(date, now);
  const subject = subjectText(conversation.subject);
  const count = conversation.messageCount > 1 ? conversation.messageCount : undefined;

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
      onHoverStart={onRest}
      onHoverEnd={onLeave}
      // The list reads the conversation ahead when focus rests here.
      data-conversation={conversation.id}
      className="group/row h-full gap-3 border-b px-4 py-2"
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
        {/* The star ends the preview's line. A starred row's preview gives
            way to it; an unstarred row's keeps its whole width, and its
            end fades under the star while the star shows, so the text
            doesn't move as the pointer passes. */}
        <div className="group/line relative flex h-5 items-center gap-1.5">
          <p
            dir="auto"
            className={cn(
              "min-w-0 flex-1 truncate text-snippet font-normal text-muted-foreground",
              !conversation.starred &&
                "not-pointer-coarse:group-has-data-focus-visible/line:fade-end not-pointer-coarse:group-data-focus-visible/row:fade-end not-pointer-coarse:group-data-hovered/row:fade-end",
            )}
          >
            {conversation.preview}
          </p>
          <StarButton starred={conversation.starred} onStar={onStar} />
        </div>
      </div>
    </GridListItem>
  );
}

// StarButton stars a row (DESIGN.md "Message list"): shown while the row is
// hovered or has keyboard focus, or while starred. A touch screen, where
// nothing hovers, gets no button, only the star's mark while starred; the
// row's name already says it is starred.
//
// DESIGN: 24 px, the smallest target WCAG 2.2 allows (2.5.8), so it fits
// its 20 px line with 2 px to spare under the subject's letters; DESIGN.md
// lets a control in a list row be this size. Unstarred, it lies over its
// line's end rather than beside it, so the preview keeps its width while
// the star is hidden and doesn't jump when it shows. A click leaves focus
// on it, so focus shows it only when it came from the keyboard. No tooltip: its
// icon and pressed state are its meaning, and one popping up over the
// rows below as the pointer drifts would only get in the way.
function StarButton({
  starred,
  onStar,
}: {
  starred: boolean;
  onStar: (starred: boolean) => void;
}): ReactElement {
  return (
    <>
      <ToggleButton
        aria-label={starName}
        isSelected={starred}
        onChange={onStar}
        className={cn(
          // Shows and hides at once, in the same frame as its preview's
          // fade: a star still fading out over the preview's returning
          // text would overlap it. The outline star darkens under the
          // pointer, as the row's own hover surface is behind it.
          "flex size-6 shrink-0 items-center justify-center rounded-md transition-colors outline-none data-focus-visible:focus-ring-inset pointer-coarse:hidden",
          // 2 px past its 20 px line above and below, and 4 px into the
          // row's padding, which puts its icon over the line's last 16 px.
          starred
            ? "-my-0.5 -mr-1 text-foreground"
            : "absolute -top-0.5 -right-1 text-muted-foreground opacity-0 group-data-focus-visible/row:opacity-100 group-data-hovered/row:opacity-100 data-focus-visible:opacity-100 data-hovered:text-foreground",
        )}
      >
        <StarIcon aria-hidden="true" className={cn("size-4", starred && "fill-current")} />
      </ToggleButton>
      {starred && (
        <StarIcon
          aria-hidden="true"
          className="hidden size-4 shrink-0 fill-current pointer-coarse:block"
        />
      )}
    </>
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
