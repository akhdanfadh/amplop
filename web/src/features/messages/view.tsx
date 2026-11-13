import { Fragment, useEffect, useRef, useState, type ReactElement } from "react";
import { sentence, userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";
import { ReadError } from "@/components/read-error";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { textLink } from "@/components/link-style";
import { Attachments, type Attachment } from "./attachments";
import { HtmlBody } from "./html-body";
import { MessageLoading } from "./loading";
import { TextBody } from "./text-body";
import {
  addressName,
  contentFailed,
  downloadText,
  formatReceived,
  imagesHidden,
  imagesShown,
  noText,
  showImages,
  subjectText,
  textClipped,
  textFilename,
  type Address,
} from "./text";

// A message's header fields, which the view shows before its body loads.
export type MessageHeader = {
  from: Address[];
  to: Address[];
  cc: Address[];
  // Only a message the member sent has Bcc recipients to show.
  bcc?: Address[];
  subject: string;
  // When the message arrived, as an ISO 8601 timestamp; for one the member
  // sent, when Gmail took it, or else when inbok saved it.
  receivedAt: string;
};

// One body of a message, shown one after another, by the ID of its part.
// An HTML body is sent by the server at url with remote images blocked,
// and at remoteUrl with them loaded. A long plain-text body's text is only
// its start, and wholeUrl downloads all of it.
export type MessageBody = { partId: string } & (
  | { type: "html"; url: string; remoteUrl: string }
  | { type: "text"; text: string; wholeUrl?: string }
);

// Whether a message's HTML refers to remote images the server left out.
export type RemoteContent = "none" | "blocked";

// A message's bodies as they load. A body not stored yet is fetched from
// the provider first, which takes a while and can fail.
export type MessageContent =
  | { status: "pending" }
  | { status: "error"; error: ApiError; retrying: boolean; onRetry: () => void }
  | {
      status: "success";
      bodies: MessageBody[];
      remote: RemoteContent;
      attachments: Attachment[];
    };

// MessageView shows one message: its header fields, then its bodies, with
// an action that loads remote images for this view only, then its
// attachments. Give it a key per message, so the next one starts afresh;
// remote images stay blocked for another message's bodies even without
// one.
export function MessageView({
  header,
  content,
}: {
  header: MessageHeader;
  content: MessageContent;
}): ReactElement {
  const subject = subjectText(header.subject);
  return (
    <article className="flex flex-col gap-4" aria-label={subject}>
      <Header header={header} subject={subject} />
      <Separator />
      <Content content={content} />
    </article>
  );
}

function Header({ header, subject }: { header: MessageHeader; subject: string }): ReactElement {
  const received = formatReceived(header.receivedAt);
  return (
    <header className="flex flex-col gap-3">
      <h2 className="text-xl font-semibold wrap-break-word">{subject}</h2>
      {/* minmax(0, ...) lets a long address wrap instead of widening the page. */}
      <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-sm">
        <AddressRow label="From" addresses={header.from} />
        <AddressRow label="To" addresses={header.to} />
        <AddressRow label="Cc" addresses={header.cc} />
        <AddressRow label="Bcc" addresses={header.bcc ?? []} />
        <dt className="text-muted-foreground">Date</dt>
        <dd>
          <time dateTime={header.receivedAt} title={received.full}>
            {received.short}
          </time>
        </dd>
      </dl>
    </header>
  );
}

// AddressRow is one header field of addresses, left out when it has none.
function AddressRow({
  label,
  addresses,
}: {
  label: string;
  addresses: readonly Address[];
}): ReactElement | null {
  if (addresses.length === 0) return null;
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="wrap-break-word">
        {addresses.map((address, i) => (
          // An address listed twice is the sender's doing; React only warns.
          <Fragment key={`${address.email} ${address.name ?? ""}`}>
            {i > 0 && ", "}
            <AddressText address={address} />
          </Fragment>
        ))}
      </dd>
    </>
  );
}

// AddressText shows address by its display name and email, or its email
// alone. The name is the sender's text, so bdi keeps a direction override
// in it from reordering the email after it.
function AddressText({ address }: { address: Address }): ReactElement {
  const name = addressName(address);
  if (!name) return <bdi>{address.email}</bdi>;
  return (
    <>
      <bdi>{name}</bdi> &lt;<bdi>{address.email}</bdi>&gt;
    </>
  );
}

function Content({ content }: { content: MessageContent }): ReactElement {
  if (content.status === "pending") return <MessageLoading />;
  if (content.status === "error")
    return (
      <ReadError
        message={`${contentFailed} ${sentence(userMessage(content.error))}`}
        error={content.error}
        retrying={content.retrying}
        onRetry={content.onRetry}
      />
    );
  return (
    <>
      <Bodies bodies={content.bodies} remote={content.remote} />
      <Attachments attachments={content.attachments} />
    </>
  );
}

function Bodies({
  bodies,
  remote,
}: {
  bodies: readonly MessageBody[];
  remote: RemoteContent;
}): ReactElement {
  // The remote URLs the member chose to load. Kept by URL rather than as
  // a flag, so bodies of another message never load with them.
  const [allowed, setAllowed] = useState<readonly string[]>([]);
  const notice = useRef<HTMLDivElement>(null);
  // Set by "Show images", whose button goes away.
  const focusNotice = useRef(false);
  const html = bodies.flatMap((body) => (body.type === "html" ? [body] : []));
  const remoteShown = html.length > 0 && html.every((body) => allowed.includes(body.remoteUrl));

  useEffect(() => {
    if (!remoteShown || !focusNotice.current) return;
    focusNotice.current = false;
    // After the notice's new text is in place, so it is what is read.
    notice.current?.focus();
  }, [remoteShown]);

  function allowRemote(): void {
    focusNotice.current = true;
    setAllowed(html.map((body) => body.remoteUrl));
  }

  const hasText =
    html.length > 0 || bodies.some((body) => body.type === "text" && body.text.trim() !== "");

  return (
    <div className="flex flex-col gap-4">
      {!hasText && <p className="text-muted-foreground">{noText}</p>}
      {remote === "blocked" && (
        // Not an alert: nothing went wrong, and it is read with the message.
        <Alert ref={notice} role="note" tabIndex={-1} className="flex flex-wrap items-center gap-3">
          {remoteShown ? (
            <AlertDescription>{imagesShown}</AlertDescription>
          ) : (
            <>
              <AlertDescription className="flex-1 basis-60">{imagesHidden}</AlertDescription>
              <Button type="button" variant="outline" size="sm" onPress={allowRemote}>
                {showImages}
              </Button>
            </>
          )}
        </Alert>
      )}
      {bodies.map((body, i) => {
        if (body.type === "text")
          return (
            <Fragment key={body.partId}>
              <TextBody text={body.text} />
              {body.wholeUrl && (
                <p className="text-snippet text-muted-foreground">
                  {textClipped}{" "}
                  <a href={body.wholeUrl} download={textFilename} className={textLink}>
                    {downloadText}
                  </a>
                </p>
              )}
            </Fragment>
          );
        const url = allowed.includes(body.remoteUrl) ? body.remoteUrl : body.url;
        return (
          <HtmlBody
            // A new URL starts a new frame, with its own loading state.
            key={url}
            url={url}
            title={html.length > 1 ? `Message body, part ${i + 1}` : "Message body"}
          />
        );
      })}
    </div>
  );
}
