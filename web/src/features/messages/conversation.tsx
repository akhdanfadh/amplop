import {
  Fragment,
  useEffect,
  useId,
  useRef,
  useState,
  type ReactElement,
  type ReactNode,
  type RefObject,
} from "react";
import { ChevronDownIcon, ChevronUpIcon, PaperclipIcon, ReplyIcon, UserIcon } from "lucide-react";
import { cn } from "@/lib/cn";
import { AccountMark, accountRing, type AccountColor } from "@/components/account-mark";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { useMessageContent } from "./content";
import { foldRows, initiallyOpen, type Folding } from "./folding";
import { initials, namesOf, repliesElsewhere, type MailAddress } from "./people";
import {
  conversationActions,
  detailsLabel,
  fieldLabels,
  foldLabel,
  formatReceived,
  hasAttachments,
  listDate,
  messageCount,
  moreMessages,
  noRecipients,
  notSendersDomain,
  notTheSender,
  receivedBy,
  repliesGoTo,
  sentTo,
  showAllAddresses,
  subjectText,
  unknownSender,
} from "./text";
import { MessageContentView } from "./view";

// ConversationMessage is one message of a conversation as the view takes
// it: its header fields, a plain-text preview for its folded row, and
// whether it is unread. date is when the provider received it, as an ISO
// 8601 timestamp. sent says whether the account sent it (it is in the
// account's Sent mail); without it, a message from the account's own
// address counts as sent.
//
// DESIGN: the fields of branch 3's API type of the same name, which is
// not on dev yet; whichever branch merges later passes that type in, with
// the per-message sent flag its conversation detail is to add (ADR031
// follow-ups).
export type ConversationMessage = {
  id: string;
  from: MailAddress[];
  to: MailAddress[];
  cc: MailAddress[];
  replyTo: MailAddress[];
  date: string;
  preview: string;
  unread: boolean;
  sent?: boolean;
  // Whether it is known to have attachments: its content was fetched and
  // has some. Branch 3's detail is to send it (DEBT.md).
  attachments?: boolean;
};

// ConversationAccount is the connected account a conversation belongs to:
// its name as the app shows it, its address, and its colour.
export type ConversationAccount = {
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
// thread folds into "N more messages". The view scrolls itself, so give
// it a height, and a key per conversation, so the next starts afresh.
// onUnauthorized runs when a read finds the session gone.
//
// The slots hold what other features build: actions, the conversation's
// actions in the toolbar (and Back, where one pane shows at a time); and
// messageActions, a message's own actions in its header.
export function ConversationView({
  subject,
  account,
  messages,
  onUnauthorized,
  actions,
  messageActions,
}: {
  subject: string;
  account: ConversationAccount;
  messages: readonly ConversationMessage[];
  onUnauthorized: () => void;
  actions?: ReactNode;
  messageActions?: (message: ConversationMessage) => ReactNode;
}): ReactElement {
  const id = useId();
  const title = subjectText(subject);
  const scroller = useRef<HTMLDivElement>(null);
  const toolbar = useRef<HTMLDivElement>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const [open, setOpen] = useState(() => initiallyOpen(messages));
  // The messages the member opened or folded keep their own rows: one
  // folded again never disappears back into "N more messages".
  const [touched, setTouched] = useState<ReadonlySet<string>>(() => new Set());
  const [folding, setFolding] = useState<Folding>("grouped");
  const [titleHidden, setTitleHidden] = useState(false);
  const today = useToday();
  // The control to focus once the rows change: a message's row or its
  // fold button, which replace each other, so focus stays where it was.
  const focusNext = useRef<string | null>(null);

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

  useEffect(() => {
    if (focusNext.current === null) return;
    document.getElementById(focusNext.current)?.focus();
    focusNext.current = null;
  });

  // Open or touched: each keeps its own row.
  const shown = new Set([...open, ...touched]);

  const rowId = (messageId: string) => `${id}-row-${messageId}`;
  const foldId = (messageId: string) => `${id}-fold-${messageId}`;

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

  return (
    <div ref={scroller} className="@container h-full overflow-y-auto bg-background">
      <div
        ref={toolbar}
        data-raised={titleHidden || undefined}
        // Wraps only when large text leaves no room; DESIGN.md keeps five
        // buttons and More.
        className="sticky top-0 z-10 flex min-h-12 flex-wrap items-center gap-1 border-b border-transparent bg-background px-4 data-raised:border-border"
      >
        <p
          // The title says it already, for anyone reading in order.
          aria-hidden="true"
          className={cn(
            "min-w-24 flex-1 truncate px-2 text-sm font-semibold transition-opacity duration-150",
            titleHidden ? "opacity-100" : "opacity-0",
          )}
        >
          {title}
        </p>
        {actions && (
          <div
            role="toolbar"
            aria-label={conversationActions}
            className="ml-auto flex flex-wrap items-center gap-0.5"
          >
            {actions}
          </div>
        )}
      </div>
      <article aria-labelledby={`${id}-title`} className="flex flex-col px-4 pb-6 @lg:px-6">
        <header className="flex flex-col gap-1 pt-2 pb-4">
          <h2
            ref={heading}
            id={`${id}-title`}
            tabIndex={-1}
            className="text-subject font-semibold wrap-break-word outline-none"
          >
            {title}
          </h2>
          <p className="flex min-w-0 items-center gap-2 text-sm text-muted-foreground">
            <AccountMark color={account.color} name={account.name} />
            <span aria-hidden="true">·</span>
            <span className="shrink-0">{messageCount(messages.length)}</span>
          </p>
        </header>
        <ol className="-mx-2 flex flex-col border-t">
          {foldRows(messages, shown, folding).map((row) =>
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
              <li key={row.message.id} className="border-b">
                {open.has(row.message.id) ? (
                  <OpenMessage
                    message={row.message}
                    account={account}
                    foldId={foldId(row.message.id)}
                    onFold={() => foldMessage(row.message.id)}
                    actions={messageActions?.(row.message)}
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
              </li>
            ),
          )}
        </ol>
      </article>
    </div>
  );
}

// countsAsSent reports whether account sent message: as the conversation
// says, or else whether one of its senders is the account's own address,
// which misses an alias and believes a forged From (ADR031 follow-ups).
function countsAsSent(message: ConversationMessage, account: ConversationAccount): boolean {
  const own = account.email.toLowerCase();
  return message.sent ?? message.from.some((entry) => entry.address.toLowerCase() === own);
}

// useFocusFollowsCopy keeps keyboard focus on the visible one of two
// copies of the same controls, a and b, which the pane's width shows one at
// a time: when the pane crosses the width, e.g. as the window narrows or
// text grows, focus on the copy now hidden moves to the same control in the
// other.
function useFocusFollowsCopy(
  a: RefObject<HTMLElement | null>,
  b: RefObject<HTMLElement | null>,
): void {
  useEffect(() => {
    const first = a.current;
    const second = b.current;
    if (!first || !second) return undefined;
    // Where focus last was in either copy, as a hidden control may have
    // lost it before the resize is seen.
    let last: { copy: HTMLElement; index: number } | null = null;
    const controls = (copy: HTMLElement) => [
      ...copy.querySelectorAll<HTMLElement>("button, a[href]"),
    ];
    const listeners = [first, second].map((copy) => ({
      copy,
      focusin: (event: FocusEvent) => {
        last = { copy, index: controls(copy).findIndex((control) => control === event.target) };
      },
      focusout: (event: FocusEvent) => {
        // Focus moving on, or leaving a copy that is still drawn (a click
        // on blank space), is the member's doing; only hiding keeps it.
        if (event.relatedTarget !== null || copy.offsetParent !== null) last = null;
      },
    }));
    const observer = new ResizeObserver(() => {
      if (!last || last.copy.offsetParent !== null) return;
      const active = document.activeElement;
      if (active !== document.body && !last.copy.contains(active)) return;
      const other = last.copy === first ? second : first;
      const target = controls(other)[last.index];
      // Cleared first: focusing records the new copy, for the next crossing.
      last = null;
      target?.focus();
    });
    for (const { copy, focusin, focusout } of listeners) {
      copy.addEventListener("focusin", focusin);
      copy.addEventListener("focusout", focusout);
      observer.observe(copy);
    }
    return () => {
      observer.disconnect();
      for (const { copy, focusin, focusout } of listeners) {
        copy.removeEventListener("focusin", focusin);
        copy.removeEventListener("focusout", focusout);
      }
    };
  }, [a, b]);
}

// useToday returns the time now, moving on only when the day changes:
// that is all a folded row's date depends on ("Yesterday", a weekday).
//
// DESIGN: branch 3's features/mail/list-state.ts has the same hook, not on
// dev yet; whichever branch merges later keeps one.
function useToday(): Date {
  const [today, setToday] = useState(() => new Date());
  useEffect(() => {
    const next = new Date(today);
    next.setHours(24, 0, 1, 0);
    const timer = setTimeout(() => setToday(new Date()), next.getTime() - Date.now());
    return () => clearTimeout(timer);
  }, [today]);
  return today;
}

// SenderAvatar shows sender's initials in a 36 px circle ringed in the
// account's colour, or a person icon when it has none.
function SenderAvatar({
  sender,
  color,
}: {
  sender: MailAddress | undefined;
  color: AccountColor;
}): ReactElement {
  const letters = initials(sender);
  return (
    <Avatar className={cn("size-9 after:border-2 after:mix-blend-normal", accountRing(color))}>
      <AvatarFallback className="text-xs font-semibold">
        {letters || <UserIcon aria-hidden="true" className="size-4" />}
      </AvatarFallback>
    </Avatar>
  );
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
      <SenderAvatar sender={message.from[0]} color={account.color} />
      <span className="flex min-w-0 flex-1 flex-col @lg:flex-row @lg:items-center @lg:gap-3">
        <span
          dir="auto"
          className={cn("truncate @lg:w-40 @lg:shrink-0", message.unread && "font-semibold")}
        >
          {namesOf(message.from, account.email) || unknownSender}
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
        {listDate(message.date, today)}
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
  foldId,
  onFold,
  actions,
  today,
  onUnauthorized,
}: {
  message: ConversationMessage;
  account: ConversationAccount;
  foldId: string;
  onFold: () => void;
  actions: ReactNode;
  today: Date;
  onUnauthorized: () => void;
}): ReactElement {
  const detailsId = useId();
  const [details, setDetails] = useState(false);
  const content = useMessageContent(message.id, onUnauthorized);
  const when = formatReceived(message.date);
  const sender = message.from[0];
  const senderName = message.from.length > 0 ? namesOf(message.from, "") : unknownSender;
  const wideActions = useRef<HTMLDivElement>(null);
  const narrowActions = useRef<HTMLDivElement>(null);
  useFocusFollowsCopy(wideActions, narrowActions);
  const recipients = namesOf([...message.to, ...message.cc], account.email);

  return (
    <article aria-label={`${senderName}, ${when.short}`} className="flex flex-col gap-3 px-2 py-3">
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
          <SenderAvatar sender={sender} color={account.color} />
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
                this one to the sender. */}
            <time
              dateTime={message.date}
              title={when.full}
              className="hidden shrink-0 pt-0.5 text-xs text-muted-foreground tabular-nums @lg:block"
            >
              {when.short}
            </time>
            {actions && (
              <div ref={wideActions} className="hidden shrink-0 items-center @lg:flex">
                {actions}
              </div>
            )}
          </div>
          {/* The date and details button follow the recipients, which
              shrink first. In a narrow pane the actions end this line, and
              wrap to their own when not even 10rem of the rest would fit
              beside them. */}
          <div className="flex min-w-0 flex-wrap items-center gap-1 text-muted-foreground @lg:flex-nowrap">
            <div className="flex min-w-0 flex-1 basis-40 items-center gap-1">
              <span dir="auto" className="min-w-0 truncate">
                {sentTo(recipients || noRecipients)}
              </span>
              <time
                dateTime={message.date}
                title={when.full}
                className="shrink-0 tabular-nums @lg:hidden"
              >
                · {listDate(message.date, today)}
              </time>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={detailsLabel}
                aria-expanded={details}
                aria-controls={detailsId}
                onPress={() => setDetails((shown) => !shown)}
                // A 20 px box around a 14 px chevron; ::before grows its
                // target to 24 px (WCAG 2.5.8), 44 px for touch, with room
                // after it so that target stops short of the actions.
                className="relative size-5 rounded-md before:absolute before:-inset-0.5 pointer-coarse:mr-2 pointer-coarse:size-5 pointer-coarse:before:-inset-3"
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
            {actions && (
              <div ref={narrowActions} className="ml-auto flex shrink-0 items-center @lg:hidden">
                {actions}
              </div>
            )}
          </div>
          {/* A message the account sent is answered to its To, so the
              line would name the wrong place (reply-defaults.ts). */}
          {repliesElsewhere(message.replyTo, message.from) && !countsAsSent(message, account) && (
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
