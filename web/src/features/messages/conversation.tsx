import {
  Fragment,
  useEffect,
  useId,
  useRef,
  useState,
  type ReactElement,
  type ReactNode,
} from "react";
import { ChevronDownIcon, ChevronUpIcon, PaperclipIcon, ReplyIcon } from "lucide-react";
import { Toolbar } from "react-aria-components";
import { cn } from "@/lib/cn";
import type { ConversationMessageOutput } from "@/api/generated/model";
import { AccountMark, type AccountColor } from "@/components/account-mark";
import { Button } from "@/components/ui/button";
import {
  ConversationReply,
  MessageReplyActions,
  ReplyEditorSlot,
  ReplyProvider,
  useAnsweredId,
} from "@/features/outgoing-messages/reply";
import type { AnsweredMessage, MailAddress } from "@/features/outgoing-messages/reply-defaults";
import { listDate, useToday } from "@/features/mail/dates";
import { fullNames, repliesElsewhere } from "@/features/mail/people";
import { SenderAvatar } from "@/features/mail/sender-avatar";
import { subjectText, unknownSender } from "@/features/mail/text";
import { useMessageContent } from "./content";
import { foldRows, initiallyOpen, type Folding } from "./folding";
import {
  conversationActions,
  detailsLabel,
  answeredDeleted,
  fieldLabels,
  foldLabel,
  formatReceived,
  hasAttachments,
  answersLead,
  answersOn,
  messageTotal,
  moreMessages,
  noRecipients,
  notSendersDomain,
  notTheSender,
  receivedBy,
  repliesGoTo,
  sentTo,
  showAllAddresses,
} from "./text";
import { MessageContentView } from "./view";

// ConversationMessage is one message of a conversation, as Get
// Conversation gives it: its header fields, a plain-text preview for its
// folded row, whether it is unread, whether the account sent it, the
// message it answers, and whether it is known to have attachments.
export type ConversationMessage = ConversationMessageOutput;

// ConversationAccount is the connected account a conversation belongs to:
// its ID, its name as the app shows it, its address, and its colour.
export type ConversationAccount = {
  id: number;
  name: string;
  email: string;
  color: AccountColor;
};

// How many addresses a details field lists before "Show all".
const shownAddresses = 10;

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
// conversation, so the next starts afresh. onUnauthorized runs when a read
// finds the session gone.
//
// messages is undefined until they are read; placeholder shows in their
// place meanwhile, e.g. that they are loading or could not be read.
//
// The slots hold what other features build: back, the way back to the
// list at the toolbar's start, where one pane shows at a time; actions,
// the conversation's actions at its end; and messageActions, a message's
// other actions in its header, after Reply.
export function ConversationView(props: ConversationProps): ReactElement {
  return (
    <ReplyProvider onUnauthorized={props.onUnauthorized}>
      <Conversation {...props} />
    </ReplyProvider>
  );
}

type ConversationProps = {
  subject: string;
  account: ConversationAccount;
  messages: readonly ConversationMessage[] | undefined;
  placeholder?: ReactNode;
  onUnauthorized: () => void;
  back?: ReactNode;
  actions?: ReactNode;
  messageActions?: (message: ConversationMessage) => ReactNode;
};

// Conversation is the view inside its reply editor's provider: the toolbar
// and title, then the messages' thread once they are read.
function Conversation({
  subject,
  account,
  messages,
  placeholder,
  onUnauthorized,
  back,
  actions,
  messageActions,
}: ConversationProps): ReactElement {
  const id = useId();
  const title = subjectText(subject);
  const scroller = useRef<HTMLDivElement>(null);
  const toolbar = useRef<HTMLDivElement>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const [titleHidden, setTitleHidden] = useState(false);

  useEffect(() => {
    // The view opens scrolled to its top, so focus moves the eye nowhere.
    heading.current?.focus({ preventScroll: true });
  }, []);

  useEffect(() => {
    const bar = toolbar.current;
    const pane = scroller.current;
    if (!bar || !pane) return undefined;
    // Whatever scrolls into view, by focus or by a link, stops below the
    // pinned toolbar (WCAG 2.4.11); once it no longer pins, nothing covers.
    const observer = new ResizeObserver(() => {
      const pinned = getComputedStyle(bar).position === "sticky";
      pane.style.scrollPaddingTop = pinned ? `${bar.offsetHeight}px` : "";
    });
    observer.observe(bar);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    const target = heading.current;
    if (!target) return undefined;
    // The title counts as gone once it is under the pinned toolbar.
    const observer = new IntersectionObserver(([entry]) => setTitleHidden(!entry.isIntersecting), {
      root: scroller.current,
      rootMargin: `-${toolbar.current?.offsetHeight ?? 0}px 0px 0px 0px`,
    });
    observer.observe(target);
    return () => observer.disconnect();
  }, []);

  return (
    // scroll-pb: what scrolls into view stops above a toast (toast.tsx).
    <div
      ref={scroller}
      className="@container h-full scroll-pb-(--toast-space) overflow-y-auto bg-background"
    >
      <div
        ref={toolbar}
        data-raised={titleHidden || undefined}
        // Wraps only when large text leaves no room; DESIGN.md keeps five
        // buttons and More.
        // Pinned while its buttons fit one row: under 20em, as large text
        // makes a phone's pane (six 44 px buttons and the gutters), it
        // would take a third of the screen, so it scrolls away instead.
        // The query counts in em, so it follows the text size, not pixels.
        className="sticky top-0 z-10 flex min-h-12 flex-wrap items-center gap-1 border-b border-transparent bg-background px-4 data-raised:border-border @max-[20em]:static"
      >
        {back}
        <p
          // The title says it already, for anyone reading in order; and as
          // a copy, faded out until then, it takes no pointer or selection.
          aria-hidden="true"
          // A subject's direction from its first letter, as the list's.
          dir="auto"
          // Under a 512 px pane it may shrink, so the toolbar keeps one
          // row (ADR031, "Mail actions as built", checkpoint 7c); under
          // 28em, e.g. a phone, it would be a few letters between Back and
          // the actions, so it hides. In em, so it follows the text size.
          className={cn(
            "pointer-events-none min-w-0 flex-1 truncate px-2 text-sm font-semibold transition-opacity duration-150 select-none @max-[28em]:hidden @lg:min-w-24",
            titleHidden ? "opacity-100" : "opacity-0",
          )}
        >
          {title}
        </p>
        {actions && (
          // React Aria's toolbar: Tab reaches it once, arrow keys move
          // between its buttons.
          <Toolbar
            aria-label={conversationActions}
            className="ml-auto flex flex-wrap items-center gap-0.5"
          >
            {actions}
          </Toolbar>
        )}
      </div>
      <article aria-labelledby={`${id}-title`} className="flex flex-col px-4 pb-6 @lg:px-6">
        <header className="flex flex-col gap-1 pt-2 pb-4">
          <h2
            ref={heading}
            id={`${id}-title`}
            tabIndex={-1}
            dir="auto"
            className="text-subject font-semibold wrap-break-word outline-none"
          >
            {title}
          </h2>
          <p className="flex min-w-0 items-center gap-2 text-sm text-muted-foreground">
            <AccountMark color={account.color} name={account.name} />
            {messages && (
              <>
                <span aria-hidden="true">·</span>
                <span className="shrink-0">{messageTotal(messages.length)}</span>
              </>
            )}
          </p>
        </header>
        {messages ? (
          <Thread
            account={account}
            messages={messages}
            onUnauthorized={onUnauthorized}
            messageActions={messageActions}
          />
        ) : (
          placeholder
        )}
      </article>
    </div>
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
  onUnauthorized,
  messageActions,
}: {
  account: ConversationAccount;
  messages: readonly ConversationMessage[];
  onUnauthorized: () => void;
  messageActions?: (message: ConversationMessage) => ReactNode;
}): ReactElement {
  const id = useId();
  // The message the open editor answers keeps its row out of "N more
  // messages", so the editor below it stays in sight, folded or not.
  const answeredId = useAnsweredId();
  const newest = messages.at(-1);
  const [open, setOpen] = useState(() => initiallyOpen(messages));
  // The messages the member opened or folded keep their own rows: one
  // folded again never disappears back into "N more messages".
  const [touched, setTouched] = useState<ReadonlySet<string>>(() => new Set());
  const [folding, setFolding] = useState<Folding>("grouped");
  const today = useToday();
  // The control to focus once the rows change: a message's row or its
  // fold button, which replace each other, so focus stays where it was; or
  // a message itself, shown by a link.
  const focusNext = useRef<string | null>(null);
  // Whether that message also scrolls to the pane's top.
  const scrollNext = useRef(false);
  // The message a link just showed, which flashes so the eye finds it.
  const [flashed, setFlashed] = useState<string | null>(null);

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
                  <OpenMessage
                    message={row.message}
                    account={account}
                    foldId={foldId(row.message.id)}
                    onFold={() => foldMessage(row.message.id)}
                    id={articleId(row.message.id)}
                    answers={answersLine(row.message, messageAbove(i))}
                    actions={
                      <>
                        <MessageReplyActions message={answered(row.message, account)} />
                        {messageActions?.(row.message)}
                      </>
                    }
                    today={today}
                    onUnauthorized={onUnauthorized}
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

// OpenMessage is a message shown whole: its header, then its content,
// which it loads. Its avatar folds it, as does a click on its header's
// empty space.
function OpenMessage({
  message,
  account,
  id,
  foldId,
  onFold,
  answers,
  actions,
  today,
  onUnauthorized,
}: {
  message: ConversationMessage;
  account: ConversationAccount;
  id: string;
  foldId: string;
  onFold: () => void;
  answers: { sender: string; date: string; onShow: () => void } | undefined;
  actions: ReactNode;
  today: Date;
  onUnauthorized: () => void;
}): ReactElement {
  const detailsId = useId();
  const [details, setDetails] = useState(false);
  const content = useMessageContent(message.id, onUnauthorized);
  const when = formatReceived(message.date);
  const sender = message.from[0];
  const senderName = message.from.length > 0 ? fullNames(message.from, undefined) : unknownSender;
  const recipients = fullNames([...message.to, ...message.cc], account.email);

  return (
    <article
      id={id}
      // Focused when a link shows it; the flash marks it, and the keyboard
      // gets the ring.
      tabIndex={-1}
      aria-label={`${senderName}, ${when.short}`}
      className="flex flex-col gap-3 px-2 py-3 outline-none focus-visible:focus-ring-inset"
    >
      {/* A click on the header's empty space folds the message, as in
          Gmail; the avatar does the same for the keyboard. */}
      {/* oxlint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-static-element-interactions -- a pointer shortcut; the avatar is the keyboard's button. */}
      <div
        className="flex cursor-pointer gap-3"
        onClick={(event) => {
          if (event.target instanceof Element && event.target.closest("button, a")) return;
          // Selecting the address to copy it is not a click on the header.
          if (window.getSelection()?.toString()) return;
          onFold();
        }}
      >
        <button
          type="button"
          id={foldId}
          aria-expanded={true}
          aria-label={foldLabel(senderName)}
          onClick={onFold}
          // ::before grows its 36 px to 44 px for touch (DESIGN.md
          // "Control height") without moving it.
          className="relative shrink-0 self-start rounded-full outline-none focus-visible:focus-ring pointer-coarse:before:absolute pointer-coarse:before:-inset-1"
        >
          <SenderAvatar sender={sender} />
        </button>
        <div className="flex min-w-0 flex-1 flex-col gap-0.5 text-sm">
          <div className="flex items-start gap-2">
            {/* Name and address share a line while both fit; the address
                then moves to the next line whole, never cut, as its domain
                tells real mail from fake. */}
            {/* Every sender shows: the Reply-To call-out compares with
                each one's domain, so none may hide. */}
            <p className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-1.5">
              {message.from.length > 0 ? (
                message.from.map((entry, i) => (
                  // oxlint-disable-next-line react/no-array-index-key -- a sender listed twice is the sender's doing; the list never reorders.
                  <Fragment key={`${i} ${entry.address}`}>
                    <AddressText
                      entry={entry}
                      nameClass="max-w-full font-semibold wrap-break-word"
                      suffix={i < message.from.length - 1 ? "," : ""}
                    />
                  </Fragment>
                ))
              ) : (
                <span className="font-semibold text-muted-foreground">{unknownSender}</span>
              )}
            </p>
            {/* In a narrow pane the date moves to the "to" line, leaving
                this one to the sender and the actions. */}
            <time
              dateTime={message.date}
              title={when.full}
              className="hidden shrink-0 pt-0.5 text-xs text-muted-foreground tabular-nums @lg:block"
            >
              {when.short}
            </time>
            {/* The actions end the sender's line at every width, so they
                never move between messages. On touch, 12 px apart, so each
                one's 44 px target (reply.tsx) has its own room. */}
            {actions && (
              <div className="flex shrink-0 items-center pointer-coarse:gap-3">{actions}</div>
            )}
          </div>
          {/* The date, in a narrow pane, and the details button follow the
              recipients, which shrink first. */}
          <div className="flex min-w-0 items-center gap-1 text-muted-foreground">
            <span dir="auto" className="min-w-0 truncate">
              {sentTo(recipients || noRecipients)}
            </span>
            <time
              dateTime={message.date}
              title={when.full}
              className="shrink-0 tabular-nums @lg:hidden"
            >
              · {listDate(new Date(message.date), today)}
            </time>
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={detailsLabel}
              aria-expanded={details}
              aria-controls={detailsId}
              onPress={() => setDetails((shown) => !shown)}
              // A 20 px box around a 14 px chevron; ::before grows its
              // target to 24 px (WCAG 2.5.8), 44 px for touch.
              className="relative size-5 rounded-md before:absolute before:-inset-0.5 pointer-coarse:z-10 pointer-coarse:size-5 pointer-coarse:before:-inset-3"
            >
              {/* 1 px down: lowercase text sits below its line's middle,
                    so a chevron centred on the box looks high beside it. */}
              {details ? (
                <ChevronUpIcon className="size-3.5 translate-y-px" />
              ) : (
                <ChevronDownIcon className="size-3.5 translate-y-px" />
              )}
            </Button>
          </div>
          {answers && (
            <p>
              <Button
                variant="link"
                size="sm"
                onPress={answers.onShow}
                // Text-sized, as a link in a line; on touch ::before grows
                // its target to 44 px (DESIGN.md "Control height") without
                // growing the line, reaching up into the "to" line, whose
                // details button stays on top where their targets meet. It
                // wraps, breaking a long address anywhere, rather than run
                // past the pane.
                className="relative min-h-0 max-w-full shrink justify-start p-0 text-left font-normal wrap-anywhere whitespace-normal text-muted-foreground pointer-coarse:min-h-0 pointer-coarse:before:absolute pointer-coarse:before:inset-x-0 pointer-coarse:before:-inset-y-3"
              >
                {/* One run of text, so its underline is one line; bdi: a
                    right-to-left name keeps "on" and the date after it,
                    whatever the date starts with. */}
                <span>
                  {answersLead} <bdi>{answers.sender}</bdi> {answersOn} {answers.date}
                </span>
              </Button>
            </p>
          )}
          {/* A message the account sent is answered to its To, so the
              line would name the wrong place (reply-defaults.ts). */}
          {repliesElsewhere(message.replyTo, message.from) && !message.sent && (
            <p className="flex items-start gap-1.5 text-muted-foreground">
              <ReplyIcon aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
              <span className="min-w-0 wrap-break-word">
                {repliesGoTo}{" "}
                <bdi className="text-foreground">
                  {message.replyTo.map((entry) => entry.address).join(", ")}
                </bdi>
                {notTheSender}
              </span>
            </p>
          )}
        </div>
      </div>
      {details && (
        <div id={detailsId} className="rounded-lg bg-muted/50 p-3 @lg:ml-12">
          <Details message={message} account={account} when={when.full} />
        </div>
      )}
      <MessageContentView content={content} />
    </article>
  );
}

// Details lists a message's header in full: From, Reply-To, To, Cc, the
// date, and the account that received it. Labels sit beside their values
// from 512 px of pane, above them below.
function Details({
  message,
  account,
  when,
}: {
  message: ConversationMessage;
  account: ConversationAccount;
  when: string;
}): ReactElement {
  const elsewhere = repliesElsewhere(message.replyTo, message.from);
  return (
    <dl className="grid grid-cols-1 gap-x-3 text-sm @lg:grid-cols-[auto_minmax(0,1fr)] @lg:gap-y-1 [&>dd]:mb-2 @lg:[&>dd]:mb-0">
      <AddressField label={fieldLabels.from} entries={message.from} />
      <AddressField label={fieldLabels.replyTo} entries={message.replyTo} strong={elsewhere} />
      <AddressField label={fieldLabels.to} entries={message.to} />
      <AddressField label={fieldLabels.cc} entries={message.cc} />
      <dt className="text-muted-foreground">{fieldLabels.date}</dt>
      <dd>{when}</dd>
      <dt className="text-muted-foreground">{receivedBy}</dt>
      <dd className="min-w-0">
        <AccountMark color={account.color} name={`${account.name} (${account.email})`} />
      </dd>
    </dl>
  );
}

// AddressField is one field of addresses in the details, left out when it
// has none. It lists the first shownAddresses, then a link to show all;
// strong marks a Reply-To at another domain.
function AddressField({
  label,
  entries,
  strong = false,
}: {
  label: string;
  entries: readonly MailAddress[];
  strong?: boolean;
}): ReactElement | null {
  const [all, setAll] = useState(false);
  // The first address "Show all" reveals takes focus from the button,
  // which goes away.
  const firstRevealed = useRef<HTMLSpanElement>(null);
  const revealed = useRef(false);
  useEffect(() => {
    if (!all || !revealed.current) return;
    revealed.current = false;
    firstRevealed.current?.focus();
  }, [all]);
  if (entries.length === 0) return null;
  const shown = all ? entries : entries.slice(0, shownAddresses);
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className={cn("wrap-break-word", strong && "font-semibold")}>
        {shown.map((entry, i) => (
          <span
            // oxlint-disable-next-line react/no-array-index-key -- an address listed twice is the sender's doing; the index keeps the keys apart, and the list never reorders.
            key={`${i} ${entry.address}`}
            ref={i === shownAddresses ? firstRevealed : undefined}
            tabIndex={i === shownAddresses ? -1 : undefined}
            className="rounded-sm outline-none focus-visible:focus-ring"
          >
            <AddressText entry={entry} suffix={i < shown.length - 1 ? "," : ""} />{" "}
          </span>
        ))}
        {!all && entries.length > shownAddresses && (
          <Button
            variant="link"
            size="sm"
            // Text-sized, as a link in a line; ::before gives touch its
            // 44 px (DESIGN.md "Control height").
            className="relative min-h-0 p-0 align-baseline pointer-coarse:min-h-0 pointer-coarse:before:absolute pointer-coarse:before:inset-x-0 pointer-coarse:before:-inset-y-3"
            onPress={() => {
              revealed.current = true;
              setAll(true);
            }}
          >
            {showAllAddresses(entries.length)}
          </Button>
        )}
        {strong && (
          <span className="block text-xs font-normal text-muted-foreground">
            {notSendersDomain}
          </span>
        )}
      </dd>
    </>
  );
}

// AddressText shows entry by its name and address: the address alone when
// it has no name or the name is the address; the name alone when it has
// no address, e.g. "Undisclosed recipients". suffix, e.g. a list's comma,
// stays on the line of the address it follows.
function AddressText({
  entry,
  nameClass,
  suffix = "",
}: {
  entry: MailAddress;
  nameClass?: string;
  suffix?: string;
}): ReactElement {
  const name = entry.name.trim();
  if (!entry.address)
    return (
      <>
        <bdi className={nameClass}>{name}</bdi>
        {suffix}
      </>
    );
  if (!name || name.toLowerCase() === entry.address.toLowerCase())
    return (
      <>
        <bdi className={nameClass}>{entry.address}</bdi>
        {suffix}
      </>
    );
  return (
    <>
      {/* The name is the sender's text, so bdi keeps a direction override
          in it from reordering the address after it. */}
      <bdi className={nameClass}>{name}</bdi>{" "}
      {/* Left to right with its brackets, whatever the page's direction. */}
      <bdi dir="ltr" className="inline-block max-w-full wrap-break-word text-muted-foreground">
        &lt;{entry.address}&gt;{suffix}
      </bdi>
    </>
  );
}
