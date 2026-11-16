import { Fragment, useEffect, useRef, useState, type ReactElement } from "react";
import { sentence, userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";
import { ReadError } from "@/components/read-error";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { textLink } from "@/components/link-style";
import { Attachments, type Attachment } from "./attachments";
import { HtmlBody } from "./html-body";
import { MessageLoading } from "./loading";
import { TextBody } from "./text-body";
import {
  contentFailed,
  downloadText,
  imagesHidden,
  imagesShown,
  noText,
  showImages,
  textClipped,
  textFilename,
} from "./text";

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

// MessageContentView shows a message's content as it loads: its bodies,
// with the notice that loads remote images, then its attachments; while
// pending, "Loading message..."; on failure, a ReadError with Try again.
// The conversation view puts it under each open message's own header.
export function MessageContentView({ content }: { content: MessageContent }): ReactElement {
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
