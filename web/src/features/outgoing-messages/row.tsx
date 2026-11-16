import { Fragment, memo, type ReactElement } from "react";
import { linkOptions } from "@tanstack/react-router";
import type { OutgoingMessageSummary } from "@/api/generated/model";
import { accountName, type AccountColor } from "@/components/account-mark";
import { GridListItem } from "@/components/ui/grid-list";
import { fullDate, listDate } from "@/features/mail/dates";
import { senders } from "@/features/mail/people";
import { useIsOpen, type OpenStore } from "@/features/mail/row";
import { SenderAvatar } from "@/features/mail/sender-avatar";
import { subjectText } from "@/features/mail/text";
import { noRecipients } from "@/features/messages/text";
import { MessageStatusWord, messageLook } from "./status-badge";
import { outboxRowName, rowReason, rowTo } from "./text";

// RowAccount is what a row shows of its message's sending account.
export type RowAccount = { color: AccountColor; email: string; name: string | null };

// OutboxRow is one message in the Outbox's list (DESIGN.md "Outbox"): To
// and its recipients, by mail's rules for names, and the date it was
// saved; its subject; its status as an icon and word, then a short reason.
// It is led by its first recipient's initials, ringed in account's colour
// unless the list shows one account (ringed false). It opens the message
// in the reading pane, as a conversation's row does; openStore says which
// message the pane shows; now is the day its date is shown at.
//
// DESIGN: no unread weight and no checkbox: the member wrote each message,
// and the Outbox has no action on several at once.
export function OutboxRow({
  message,
  account,
  ringed,
  openStore,
  now,
}: {
  message: OutgoingMessageSummary;
  account: RowAccount | undefined;
  ringed: boolean;
  openStore: OpenStore;
  now: Date;
}): ReactElement {
  const open = useIsOpen(openStore, message.id);
  // Opening a message while another is open replaces that one in the
  // history, as a conversation's row does (mail/row.tsx).
  const replaces = () => openStore.get() !== undefined;
  const names = recipientNames(message);
  const to = names.join(", ") || noRecipients;
  const first = message.to[0] ?? message.cc[0] ?? message.bcc[0];
  const shownDate = listDate(new Date(message.createdAt), now);
  const subject = subjectText(message.subject);
  const ring = ringed && account ? account.color : undefined;

  return (
    <GridListItem
      id={message.id}
      href={{
        ...linkOptions({ to: "/mail/outbox/$id", params: { id: message.id } }),
        // Whether the entry before the message's is this list, so the
        // reading pane's Back can go back through history (mail/row.tsx).
        state: (left) => ({ fromList: !replaces() || left.fromList === true }),
      }}
      // A getter, so replaces runs at the press (mail/row.tsx's NOTE).
      routerOptions={{
        get replace() {
          return replaces();
        },
      }}
      textValue={outboxRowName({
        to,
        subject,
        status: messageLook(message).label,
        date: shownDate,
        // The ring is a colour alone, so the name says the account.
        account: ring !== undefined && account ? accountName(account) : undefined,
      })}
      current={open}
      className="h-full gap-3 border-b px-4 py-2"
    >
      <SenderAvatar
        sender={first === undefined ? undefined : { name: "", address: first }}
        ring={ring}
      />
      <RowText message={message} shownDate={shownDate} subject={subject} />
    </GridListItem>
  );
}

// RowText is a row's three lines: To and date; subject; status and reason.
// Memoized: React Aria draws a row again at each focus or press change,
// while its text stays.
const RowText = memo(function RowText({
  message,
  shownDate,
  subject,
}: {
  message: OutgoingMessageSummary;
  shownDate: string;
  subject: string;
}): ReactElement {
  const reason = rowReason(message);
  const names = recipientNames(message);
  return (
    <div className="flex min-w-0 flex-1 flex-col text-sm leading-5 text-read-foreground">
      {/* One 20 px line, as a conversation's row. */}
      <div className="flex h-5 items-baseline gap-2">
        <span className="truncate">
          <span className="text-muted-foreground">{rowTo} </span>
          {names.length === 1 ? (
            // One recipient is its whole address, left to right with its
            // domain whatever its letters, as an open message shows one.
            <bdi dir="ltr">{names[0]}</bdi>
          ) : names.length > 1 ? (
            // Several read in the page's direction, in order, each in its
            // own direction, as a conversation's senders do (mail/row.tsx).
            names.map((name, i) => (
              // oxlint-disable-next-line react/no-array-index-key -- a recipient listed twice has the same name; the list never reorders.
              <Fragment key={`${i} ${name}`}>
                {i > 0 && ", "}
                <bdi>{name}</bdi>
              </Fragment>
            ))
          ) : (
            noRecipients
          )}
        </span>
        <time
          dateTime={message.createdAt}
          title={fullDate(new Date(message.createdAt))}
          className="ml-auto shrink-0 text-xs leading-5 text-muted-foreground tabular-nums"
        >
          {shownDate}
        </time>
      </div>
      <span dir="auto" className="truncate">
        {subject}
      </span>
      <p className="flex h-5 min-w-0 items-center gap-1 text-snippet text-muted-foreground">
        <MessageStatusWord message={message} />
        {reason && <span className="truncate">· {reason}</span>}
      </p>
    </div>
  );
});

// recipientNames returns how a row names message's recipients, To, Cc,
// then Bcc, by mail's rules for names: one by its address, several by
// their addresses' names.
function recipientNames(message: OutgoingMessageSummary): string[] {
  const recipients = [...message.to, ...message.cc, ...message.bcc];
  return senders(
    recipients.map((address) => ({ name: "", address })),
    undefined,
  );
}
