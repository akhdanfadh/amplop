import {
  Fragment,
  useEffect,
  useId,
  useRef,
  useState,
  type ReactElement,
  type ReactNode,
} from "react";
import { ChevronDownIcon, ChevronUpIcon, ReplyIcon } from "lucide-react";
import { cn } from "@/lib/cn";
import { AccountMark, type AccountColor } from "@/components/account-mark";
import { inlineLinkButton } from "@/components/link-style";
import { Button } from "@/components/ui/button";
import type { MailAddress } from "@/features/outgoing-messages/reply-defaults";
import { listDate } from "@/features/mail/dates";
import { fullNames, repliesElsewhere } from "@/features/mail/people";
import { SenderAvatar } from "@/features/mail/sender-avatar";
import { unknownSender } from "@/features/mail/text";
import {
  answersLead,
  answersOn,
  detailsLabel,
  fieldLabels,
  foldLabel,
  formatReceived,
  noRecipients,
  notSendersDomain,
  notTheSender,
  receivedBy,
  repliesGoTo,
  sentTo,
  showAllAddresses,
} from "./text";
import { MessageContentView, type MessageContent } from "./view";

// How many addresses a details field lists before "Show all".
const shownAddresses = 10;

// MessageAccount is the connected account an open message is shown in:
// its name as the app shows it, its address, and its colour.
export type MessageAccount = { name: string; email: string; color: AccountColor };

// OpenMessageHead is what an open message shows of its header: its
// senders, Reply-To, recipients, when it was received or sent, and
// whether its account sent it.
export type OpenMessageHead = {
  from: readonly MailAddress[];
  replyTo: readonly MailAddress[];
  to: readonly MailAddress[];
  cc: readonly MailAddress[];
  bcc?: readonly MailAddress[];
  date: string;
  sent: boolean;
};

// OpenMessage is a message shown whole (DESIGN.md "Reading pane"): head,
// the header of a message in account, then content. accountLabel names
// the account in the details ("Received by" by default), which leave it
// out when account is undefined, e.g. one disconnected since. id names the
// article, e.g. for a link to it; today is the day its narrow date is
// shown at. fold, when given, folds it from its avatar, a button named
// fold.id, and from a click on its header's empty space. answers, when
// given, names the message it answers; actions end the sender's line.
export function OpenMessage({
  head,
  account,
  accountLabel = receivedBy,
  id,
  fold,
  answers,
  actions,
  today,
  content,
}: {
  head: OpenMessageHead;
  account: MessageAccount | undefined;
  accountLabel?: string;
  id?: string;
  fold?: { id: string; onFold: () => void };
  answers?: { sender: string; date: string; onShow: () => void };
  actions?: ReactNode;
  today: Date;
  content: MessageContent;
}): ReactElement {
  const message = head;
  const detailsId = useId();
  const [details, setDetails] = useState(false);
  const when = formatReceived(message.date);
  const sender = message.from[0];
  const senderName = message.from.length > 0 ? fullNames(message.from, undefined) : unknownSender;
  const recipients = fullNames(
    [...message.to, ...message.cc, ...(message.bcc ?? [])],
    account?.email,
  );
  const avatar = <SenderAvatar sender={sender} />;

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
        className={cn("flex gap-3", fold && "cursor-pointer")}
        onClick={(event) => {
          if (!fold) return;
          if (event.target instanceof Element && event.target.closest("button, a")) return;
          // Selecting the address to copy it is not a click on the header.
          if (window.getSelection()?.toString()) return;
          fold.onFold();
        }}
      >
        {fold ? (
          <button
            type="button"
            id={fold.id}
            aria-expanded={true}
            aria-label={foldLabel(senderName)}
            onClick={fold.onFold}
            // ::before grows its 36 px to 44 px for touch (DESIGN.md
            // "Control height") without moving it.
            className="relative shrink-0 self-start rounded-full outline-none focus-visible:focus-ring pointer-coarse:before:absolute pointer-coarse:before:-inset-1"
          >
            {avatar}
          </button>
        ) : (
          <span className="shrink-0 self-start">{avatar}</span>
        )}
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
          <Details
            message={message}
            account={account}
            accountLabel={accountLabel}
            when={when.full}
          />
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
  accountLabel,
  when,
}: {
  message: OpenMessageHead;
  account: MessageAccount | undefined;
  accountLabel: string;
  when: string;
}): ReactElement {
  const elsewhere = repliesElsewhere(message.replyTo, message.from);
  return (
    <dl className="grid grid-cols-1 gap-x-3 text-sm @lg:grid-cols-[auto_minmax(0,1fr)] @lg:gap-y-1 [&>dd]:mb-2 @lg:[&>dd]:mb-0">
      <AddressField label={fieldLabels.from} entries={message.from} />
      <AddressField label={fieldLabels.replyTo} entries={message.replyTo} strong={elsewhere} />
      <AddressField label={fieldLabels.to} entries={message.to} />
      <AddressField label={fieldLabels.cc} entries={message.cc} />
      <AddressField label={fieldLabels.bcc} entries={message.bcc ?? []} />
      <dt className="text-muted-foreground">{fieldLabels.date}</dt>
      <dd>{when}</dd>
      {account && (
        <>
          <dt className="text-muted-foreground">{accountLabel}</dt>
          <dd className="min-w-0">
            <AccountMark color={account.color} name={`${account.name} (${account.email})`} />
          </dd>
        </>
      )}
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
            className={inlineLinkButton}
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
