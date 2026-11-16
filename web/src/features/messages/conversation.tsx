import {
  useEffect,
  useId,
  useRef,
  useState,
  type ComponentProps,
  type ReactElement,
  type ReactNode,
} from "react";
import { PaperclipIcon } from "lucide-react";
import { cn } from "@/lib/cn";
import type { ConversationMessageOutput } from "@/api/generated/model";
import { AccountMark } from "@/components/account-mark";
import {
  ConversationReply,
  MessageReplyActions,
  ReplyEditorSlot,
  ReplyProvider,
  useAnsweredId,
} from "@/features/outgoing-messages/reply";
import type { AnsweredMessage } from "@/features/outgoing-messages/reply-defaults";
import { listDate, useToday } from "@/features/mail/dates";
import { fullNames } from "@/features/mail/people";
import { SenderAvatar } from "@/features/mail/sender-avatar";
import { subjectText, unknownSender } from "@/features/mail/text";
import { useMessageContent } from "./content";
import { foldRows, initiallyOpen, type Folding } from "./folding";
import {
  conversationActions,
  answeredDeleted,
  formatReceived,
  hasAttachments,
  messageTotal,
  moreMessages,
} from "./text";
import { OpenMessage, type MessageAccount } from "./open-message";
import { ReadingPane } from "./reading-pane";

// ConversationMessage is one message of a conversation, as Get
// Conversation gives it: its header fields, a plain-text preview for its
// folded row, whether it is unread, whether the account sent it, the
// message it answers, and whether it is known to have attachments.
export type ConversationMessage = ConversationMessageOutput;

// ConversationAccount is the connected account a conversation belongs to:
// its ID, its name as the app shows it, its address, and its colour.
export type ConversationAccount = MessageAccount & { id: number };

// ConversationView shows a conversation (DESIGN.md "Reading pane"): a
// toolbar pinned to the top, which shows the subject once the title has
// scrolled away; the title, account, and message count; then its messages,
// oldest first. The newest and unread messages start open, each loading
// its content; the others fold to one line, and the middle of a long
// thread folds into "N more messages". Each open message offers Reply,
// which opens the conversation's one reply editor right below it; after
// the last message, Reply answers the newest, and a sent reply shows its
// status. The view takes focus when it opens, so a screen reader starts at
// its subject. It scrolls itself, so give it a height, and a key per
// conversation, so the next starts afresh.
//
// messages is undefined until they are read; placeholder shows in their
// place meanwhile, e.g. that they are loading or could not be read.
//
// The slots hold what other features build: back, the way back to the
// list at the toolbar's start, where one pane shows at a time; actions,
// the conversation's actions at its end; marks, where the conversation is,
// under the account line; and messageActions, a message's other actions
// in its header, after Reply.
export function ConversationView(props: ConversationProps): ReactElement {
  return (
    <ReplyProvider>
      <Conversation {...props} />
    </ReplyProvider>
  );
}

type ConversationProps = {
  subject: string;
  account: ConversationAccount;
  messages: readonly ConversationMessage[] | undefined;
  placeholder?: ReactNode;
  back?: ReactNode;
  actions?: ReactNode;
  marks?: ReactNode;
  messageActions?: (message: ConversationMessage) => ReactNode;
  startAt?: string;
};

// Conversation is the view inside its reply editor's provider: the
// reading pane, then the messages' thread once they are read.
function Conversation({
  subject,
  account,
  messages,
  placeholder,
  back,
  actions,
  marks,
  messageActions,
  startAt,
}: ConversationProps): ReactElement {
  return (
    <ReadingPane
      title={subjectText(subject)}
      byline={
        <>
          <AccountMark color={account.color} name={account.name} />
          {messages && (
            <>
              <span aria-hidden="true">·</span>
              <span className="shrink-0">{messageTotal(messages.length)}</span>
            </>
          )}
        </>
      }
      marks={marks}
      back={back}
      actions={actions}
      actionsLabel={conversationActions}
    >
      {messages ? (
        <Thread
          account={account}
          messages={messages}
          messageActions={messageActions}
          startAt={startAt}
        />
      ) : (
        placeholder
      )}
    </ReadingPane>
  );
}

// Thread lists messages, folded as the view describes, each with the reply
// editor's place below it, then Reply for the newest.
//
// DESIGN: its own component, mounted once the messages are read, so which
// start open is decided from them, not from none.
function Thread({
  account,
  messages,
  messageActions,
  startAt,
}: {
  account: ConversationAccount;
  messages: readonly ConversationMessage[];
  messageActions?: (message: ConversationMessage) => ReactNode;
  startAt?: string;
}): ReactElement {
  const id = useId();
  // The message the open editor answers keeps its row out of "N more
  // messages", so the editor below it stays in sight, folded or not.
  const answeredId = useAnsweredId();
  const newest = messages.at(-1);
  // The message to start at, e.g. the one a search matched, if it is here.
  const [start, setStart] = useState(() =>
    startAt !== undefined && messages.some((message) => message.id === startAt)
      ? startAt
      : undefined,
  );
  const [open, setOpen] = useState(() => {
    const first = initiallyOpen(messages);
    return start === undefined ? first : new Set(first).add(start);
  });
  // The messages the member opened or folded keep their own rows: one
  // folded again never disappears back into "N more messages", nor does
  // the one started at.
  const [touched, setTouched] = useState<ReadonlySet<string>>(
    () => new Set(start === undefined ? [] : [start]),
  );
  const [folding, setFolding] = useState<Folding>("grouped");
  const today = useToday();
  // The control to focus once the rows change: a message's row or its
  // fold button, which replace each other, so focus stays where it was; or
  // a message itself, shown by a link.
  const focusNext = useRef<string | null>(null);
  // Whether that message also scrolls to the pane's top.
  const scrollNext = useRef(false);
  // The message a link just showed, which flashes so the eye finds it.
  // The message started at flashes too, as it opens.
  const [flashed, setFlashed] = useState<string | null>(() => start ?? null);
  // Another message to start at while the conversation stays open, e.g.
  // Back to a search that matched another of its messages, opens, scrolls
  // to, and flashes as the first did: state adjusted as the prop changes.
  const [startedFor, setStartedFor] = useState(startAt);
  if (startAt !== startedFor) {
    setStartedFor(startAt);
    if (startAt !== undefined && messages.some((message) => message.id === startAt)) {
      setStart(startAt);
      setOpen((was) => new Set(was).add(startAt));
      setTouched((was) => new Set(was).add(startAt));
      setFlashed(startAt);
    }
  }

  useEffect(() => {
    if (focusNext.current === null) return;
    const target = document.getElementById(focusNext.current);
    // A message shown from a link starts at the pane's top, under the
    // toolbar (the pane's scroll padding), where the reader looks first.
    if (scrollNext.current) target?.closest("li")?.scrollIntoView({ block: "start" });
    target?.focus({ preventScroll: scrollNext.current });
    focusNext.current = null;
    scrollNext.current = false;
  });

  // The message started at shows at the pane's top, under the toolbar,
  // as one a link shows; focus stays on the title, where a screen reader
  // starts.
  useEffect(() => {
    if (start === undefined) return;
    const target = document.getElementById(`${id}-message-${start}`);
    target?.closest("li")?.scrollIntoView({ block: "start" });
  }, [id, start]);

  // Open, touched, or answered: each keeps its own row.
  const shown = new Set([...open, ...touched]);
  if (answeredId) shown.add(answeredId);
  // A message being answered that sync has since removed, e.g. deleted
  // elsewhere: its row stays, under the same key, so the editor below it
  // and its text stay until the member sends or discards.
  const answeredGone =
    answeredId !== null && !messages.some((message) => message.id === answeredId);
  const byId = new Map(messages.map((message) => [message.id, message]));

  const rowId = (messageId: string) => `${id}-row-${messageId}`;
  const foldId = (messageId: string) => `${id}-fold-${messageId}`;
  const articleId = (shownId: string) => `${id}-message-${shownId}`;

  function openMessage(messageId: string): void {
    focusNext.current = foldId(messageId);
    setTouched((was) => new Set(was).add(messageId));
    setOpen((was) => new Set(was).add(messageId));
  }
  function foldMessage(messageId: string): void {
    focusNext.current = rowId(messageId);
    setTouched((was) => new Set(was).add(messageId));
    setOpen((was) => {
      const next = new Set(was);
      next.delete(messageId);
      return next;
    });
  }
  function showFolded(first: ConversationMessage): void {
    focusNext.current = rowId(first.id);
    setFolding("all");
  }
  // showMessage opens shownId, out of "N more messages" if it was there,
  // scrolls it to the top of the pane, focuses the message itself, so a
  // second Enter does not fold it, and flashes it, so the press shows even
  // when the message was open and in sight.
  function showMessage(shownId: string): void {
    scrollNext.current = true;
    openMessage(shownId);
    focusNext.current = articleId(shownId);
    // Cleared for a frame first, so a second press flashes it again.
    setFlashed(null);
    requestAnimationFrame(() => setFlashed(shownId));
  }
  // answersLine returns the line naming the message that message answers,
  // by its sender and its header's date, unless that message is the row
  // just above, which the reader has just read; nothing when it answers
  // none.
  function answersLine(message: ConversationMessage, above: string | undefined) {
    const parent = message.answers === null ? undefined : byId.get(message.answers);
    if (!parent || parent.id === above) return undefined;
    return {
      sender: fullNames(parent.from, account.email) || unknownSender,
      date: formatReceived(parent.date).short,
      onShow: () => showMessage(parent.id),
    };
  }
  const rows = foldRows(messages, shown, folding);
  // messageAbove returns the ID of the message on the row above row i, if
  // that row is one message, not "N more messages".
  function messageAbove(i: number): string | undefined {
    const above = rows[i - 1];
    return above?.kind === "message" ? above.message.id : undefined;
  }

  return (
    <>
      {/* Present from the start, so a screen reader announces what
          enters it: the answered message going while the member types. */}
      <output className="sr-only">{answeredGone ? answeredDeleted : ""}</output>
      <ol className="-mx-2 flex flex-col border-t">
        {[
          ...rows.map((row, i) =>
            row.kind === "more" ? (
              <li key={`more-${row.messages[0].id}`} className="border-b">
                <button
                  type="button"
                  onClick={() => showFolded(row.messages[0])}
                  className="flex w-full items-center gap-3 px-2 py-2 text-sm text-muted-foreground outline-none hover:bg-accent focus-visible:focus-ring-inset"
                >
                  {/* The circle repeats the count the words say. */}
                  <span
                    aria-hidden="true"
                    className="flex size-9 shrink-0 items-center justify-center rounded-full border text-xs font-semibold text-foreground tabular-nums"
                  >
                    {row.messages.length.toLocaleString("en")}
                  </span>
                  {moreMessages(row.messages.length)}
                </button>
              </li>
            ) : (
              <li
                key={row.message.id}
                className={cn("border-b", flashed === row.message.id && "flash-selection")}
                onAnimationEnd={(event) => {
                  if (event.target === event.currentTarget) setFlashed(null);
                }}
              >
                {open.has(row.message.id) ? (
                  <LoadedMessage
                    message={row.message}
                    account={account}
                    fold={{ id: foldId(row.message.id), onFold: () => foldMessage(row.message.id) }}
                    id={articleId(row.message.id)}
                    answers={answersLine(row.message, messageAbove(i))}
                    actions={
                      <>
                        <MessageReplyActions message={answered(row.message, account)} />
                        {messageActions?.(row.message)}
                      </>
                    }
                    today={today}
                  />
                ) : (
                  <FoldedMessage
                    message={row.message}
                    account={account}
                    id={rowId(row.message.id)}
                    onOpen={() => openMessage(row.message.id)}
                    today={today}
                  />
                )}
                <ReplyEditorSlot messageId={row.message.id} />
              </li>
            ),
          ),
          // In the same list as the rows, under the answered message's
          // key, so React keeps the editor in it rather than remounting it.
          answeredGone && (
            <li key={answeredId} className="border-b">
              <p className="px-2 py-3 text-sm text-muted-foreground">{answeredDeleted}</p>
              <ReplyEditorSlot messageId={answeredId} />
            </li>
          ),
        ]}
      </ol>
      {newest && (
        <div className="mt-4">
          <ConversationReply message={answered(newest, account)} />
        </div>
      )}
    </>
  );
}

// answered returns message as the reply editor takes it, from account.
function answered(message: ConversationMessage, account: ConversationAccount): AnsweredMessage {
  return {
    id: message.id,
    accountId: account.id,
    from: message.from,
    to: message.to,
    cc: message.cc,
    replyTo: message.replyTo,
    sent: message.sent,
  };
}

// FoldedMessage is a message folded to its row: avatar, sender, preview,
// and date, one line from 512 px of pane, two below. Pressing it opens the
// message.
function FoldedMessage({
  message,
  account,
  id,
  onOpen,
  today,
}: {
  message: ConversationMessage;
  account: ConversationAccount;
  id: string;
  onOpen: () => void;
  today: Date;
}): ReactElement {
  const when = formatReceived(message.date);
  return (
    <button
      type="button"
      id={id}
      aria-expanded={false}
      onClick={onOpen}
      className="flex w-full items-center gap-3 px-2 py-2 text-left text-sm outline-none hover:bg-accent focus-visible:focus-ring-inset"
    >
      <SenderAvatar sender={message.from[0]} />
      <span className="flex min-w-0 flex-1 flex-col @lg:flex-row @lg:items-center @lg:gap-3">
        <span
          dir="auto"
          className={cn("truncate @lg:w-40 @lg:shrink-0", message.unread && "font-semibold")}
        >
          {fullNames(message.from, account.email) || unknownSender}
        </span>
        <span dir="auto" className="min-w-0 flex-1 truncate text-muted-foreground">
          {message.preview}
        </span>
      </span>
      {message.attachments && (
        <span className="mt-0.5 shrink-0 self-start @lg:mt-0 @lg:self-center">
          <PaperclipIcon aria-hidden="true" className="size-4 text-muted-foreground" />
          {/* Read as part of the row's name. */}
          <span className="sr-only">{hasAttachments}</span>
        </span>
      )}
      <time
        dateTime={message.date}
        title={when.full}
        className="shrink-0 self-start text-xs leading-5 text-muted-foreground tabular-nums @lg:self-center"
      >
        {listDate(new Date(message.date), today)}
      </time>
    </button>
  );
}

// LoadedMessage is a conversation's message shown whole (OpenMessage),
// loading its content. The other props go to OpenMessage.
function LoadedMessage({
  message,
  ...props
}: Omit<ComponentProps<typeof OpenMessage>, "head" | "content"> & {
  message: ConversationMessage;
}): ReactElement {
  const content = useMessageContent(message.id);
  return <OpenMessage head={message} content={content} {...props} />;
}
