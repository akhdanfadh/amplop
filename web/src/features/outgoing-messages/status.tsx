import { useEffect, useRef, useState, type ReactElement } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import {
  getGetOutgoingMessageQueryKey,
  getListOutgoingMessagesQueryKey,
  useSendMessageAgain,
} from "@/api/generated/inbok";
import type { ListedAccount, OutgoingMessage } from "@/api/generated/model";
import { repeatableWriteRetry } from "@/api/retry";
import { RequestError } from "@/components/request-error";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { accountDot } from "@/components/account-mark";
import { stepEnter } from "@/components/step-change";
import { formatReceived } from "@/features/messages/text";
import { cn } from "@/lib/cn";
import { useCompose } from "./compose-store";
import { MessageStatusBadge, MessageStatusMark } from "./status-badge";
import {
  needsReconnect,
  nextSteps,
  notSentPrefix,
  sendAgainErrorMessage,
  sentSummary,
} from "./text";
import { lineLink } from "@/components/link-style";

const unauthorized = 401;
const buttonClass = "w-fit whitespace-normal";

// MessageStatus shows what happened to message and what the member can do
// next, as nextSteps works out from accounts, the member's connected
// accounts (undefined until they load): send it again, edit it into a new
// message, reconnect its account, or open the copy it was sent again as.
// onSentAgain receives the copy a Send again saved; onUnauthorized runs on
// a 401. onEdit, when given, runs on Edit and send instead of opening the
// compose window with message's content, e.g. so a reply is edited into a
// new reply. look "page" names the status in a badge above its text;
// "window" is the compose window's summary after Send (DESIGN.md "Compose
// and reply"): a mark, one line, and the sending account in its colour,
// with the actions in a bar when there are any.
export function MessageStatus({
  message,
  accounts,
  onSentAgain,
  onUnauthorized,
  onEdit,
  look = "page",
}: {
  message: OutgoingMessage;
  accounts: ListedAccount[] | undefined;
  onSentAgain: (copy: OutgoingMessage) => void;
  onUnauthorized: () => void;
  onEdit?: () => void;
  look?: "page" | "window";
}): ReactElement {
  const client = useQueryClient();
  const compose = useCompose().store;
  const sectionRef = useRef<HTMLElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  // The status is put in the live region after the first render: one that
  // appears with its text is often not announced.
  const [announced, setAnnounced] = useState(false);
  // The Send again error focus last moved for.
  const focusedFor = useRef<unknown>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  const again = useSendMessageAgain({
    mutation: {
      // A repeat returns the copy the first saved.
      ...repeatableWriteRetry,
      onError: (err) => {
        if (err.status === unauthorized) onUnauthorized();
      },
      // A refusal can mean the status changed; any outcome may have saved a copy.
      onSettled: () => {
        void client.invalidateQueries({ queryKey: getGetOutgoingMessageQueryKey(message.id) });
        void client.invalidateQueries({ queryKey: getListOutgoingMessagesQueryKey() });
      },
    },
  });
  const sent = message.sentAt === null ? undefined : formatReceived(message.sentAt);
  const steps = nextSteps(message, accounts);
  const color = accounts?.find((account) => account.id === message.connectedAccountId)?.color;
  const reconnect = steps.offers.includes("reconnect") || needsReconnect(again.error);

  useEffect(() => {
    // After the first paint, when the empty region is in place.
    const frame = requestAnimationFrame(() => setAnnounced(true));
    return () => cancelAnimationFrame(frame);
  }, []);
  const sendable = steps.offers.includes("send-again");
  useEffect(() => {
    if (!again.error || again.isPending) return;
    // 1. Move focus to the button once per failure, so the error after it
    // is read next.
    if (focusedFor.current !== again.error) {
      focusedFor.current = again.error;
      (buttonRef.current ?? sectionRef.current)?.focus();
      return;
    }
    // 2. A reread after the failure, e.g. one finding the copy saved, can
    // remove the button; focus then falls to the page, so catch it.
    if (document.activeElement === document.body) sectionRef.current?.focus();
  }, [again.error, again.isPending, sendable]);

  function sendAgain(): void {
    if (again.isPending) return;
    again.mutate({ id: message.id }, { onSuccess: (result) => onSentAgain(result.data) });
  }

  const window = look === "window";
  const actions = (
    <>
      {sendable && (
        <Button
          ref={buttonRef}
          type="button"
          className={buttonClass}
          // Pending, not disabled: focus stays here while it sends, and
          // comes back here when the dialog closes.
          isPending={again.isPending}
          onPress={() => {
            // An unknown message may already have gone out, so ask first.
            if (message.status !== "unknown") return sendAgain();
            setDialogOpen(true);
          }}
        >
          {again.isPending ? "Sending again..." : "Send again"}
        </Button>
      )}
      {steps.offers.includes("edit") && (
        <Button
          type="button"
          className={buttonClass}
          onPress={onEdit ?? (() => compose.open(message))}
        >
          Edit and send
        </Button>
      )}
      {reconnect && (
        <Link to="/settings/connected-accounts" className={lineLink}>
          Open Connected accounts
        </Link>
      )}
    </>
  );
  const offersAny = sendable || steps.offers.includes("edit") || reconnect;
  const sentAgainLink = message.sentAgainAs !== null && (
    <p>
      <Link to="/mail/outbox/$id" params={{ id: message.sentAgainAs }} className={lineLink}>
        See how the new try went
      </Link>
    </p>
  );

  return (
    <section
      ref={sectionRef}
      tabIndex={-1}
      className={cn("flex flex-col outline-none", !window && "gap-4")}
      aria-label="Status"
    >
      {window ? (
        <>
          {/* Stays mounted, so each change of status is announced. */}
          <output aria-atomic="true" className="block">
            {announced && (
              // A new status rises into place, as a step does (DESIGN.md "Motion").
              <span key={message.status} className={cn("flex items-start gap-3 p-4", stepEnter)}>
                <MessageStatusMark status={message.status} />
                <span className="flex min-w-0 flex-1 flex-col gap-0.5 pt-1">
                  <span className="font-medium wrap-anywhere">{sentSummary(message)}</span>
                  <span className="text-sm text-muted-foreground">
                    {message.status === "failed" || message.status === "unknown" ? (
                      steps.text.replace(notSentPrefix, "")
                    ) : (
                      <span className="flex min-w-0 items-center gap-1.5">
                        {color !== undefined && (
                          <span
                            aria-hidden="true"
                            className={cn("size-2 shrink-0 rounded-full", accountDot(color))}
                          />
                        )}
                        <span className="truncate" translate="no">
                          From {message.from}
                        </span>
                        {message.sentAt !== null && sent && (
                          <>
                            <span aria-hidden="true">·</span>
                            <time dateTime={message.sentAt} title={sent.full} className="shrink-0">
                              {sent.short}
                            </time>
                          </>
                        )}
                      </span>
                    )}
                  </span>
                </span>
              </span>
            )}
          </output>
          <div className="flex flex-col gap-3 px-4 pb-4 pl-15 empty:hidden">
            {sentAgainLink}
            {!offersAny && (
              <p>
                <Link to="/mail/outbox/$id" params={{ id: message.id }} className={lineLink}>
                  Open in Outbox
                </Link>
              </p>
            )}
          </div>
          {offersAny && (
            <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-t px-3 py-2">
              {actions}
              <Link to="/mail/outbox/$id" params={{ id: message.id }} className={lineLink}>
                Open in Outbox
              </Link>
            </div>
          )}
        </>
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-3">
            <MessageStatusBadge status={message.status} />
            {/* Stays mounted, so a status the page reads again is announced. */}
            <output aria-atomic="true" className="basis-full">
              {!announced ? null : message.status === "sent" && message.sentAt !== null && sent ? (
                <>
                  Sent{" "}
                  <time dateTime={message.sentAt} title={sent.full}>
                    {sent.short}
                  </time>
                  .
                </>
              ) : (
                steps.text
              )}
            </output>
          </div>
          {sentAgainLink}
          <div className="flex flex-wrap items-center gap-x-6 gap-y-3">{actions}</div>
        </>
      )}

      <div className={cn(window && "px-4 pb-4 empty:hidden")}>
        <RequestError
          errors={again.error ? [{ message: sendAgainErrorMessage(again.error) }] : []}
          requestId={again.error?.requestId}
        />
      </div>

      <AlertDialog isOpen={dialogOpen} onOpenChange={setDialogOpen}>
        <AlertDialogHeader>
          <AlertDialogTitle>Send this message again?</AlertDialogTitle>
          <AlertDialogDescription className="wrap-anywhere">
            It may already have been sent. Check the Sent folder of{" "}
            <span translate="no">{message.from}</span> in Gmail first: if it&apos;s there, sending
            it again sends it twice.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction type="button" onPress={sendAgain}>
            Send again
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialog>
    </section>
  );
}
