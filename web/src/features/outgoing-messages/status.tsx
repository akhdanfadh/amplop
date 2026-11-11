import { useEffect, useRef, useState, type ReactElement } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import {
  getGetOutgoingMessageQueryKey,
  getListOutgoingMessagesQueryKey,
  useSendMessageAgain,
} from "@/api/generated/inbok";
import type { OutgoingMessage } from "@/api/generated/model";
import { repeatableWriteRetry } from "@/api/retry";
import { RequestError } from "@/components/request-error";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
import { formatReceived } from "@/features/messages/text";
import {
  editable,
  needsReconnect,
  reconnectAdvised,
  sendAgainErrorMessage,
  sendableAgain,
  statusLabel,
  statusText,
  statusVariant,
} from "./text";

const unauthorized = 401;
const focusClass =
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid";
const buttonClass = `h-auto min-h-11 w-fit whitespace-normal ${focusClass}`;
const linkClass = `inline-flex min-h-11 items-center underline underline-offset-4 ${focusClass}`;

// MessageStatus shows what happened to message and what the member can do
// next: send it again, edit it into a new message, or open the copy it was
// sent again as. current is the sending account's address now, when known.
// onSentAgain receives the copy a Send again saved; onUnauthorized runs on
// a 401.
export function MessageStatus({
  message,
  current,
  onSentAgain,
  onUnauthorized,
}: {
  message: OutgoingMessage;
  current: string | undefined;
  onSentAgain: (copy: OutgoingMessage) => void;
  onUnauthorized: () => void;
}): ReactElement {
  const client = useQueryClient();
  const sectionRef = useRef<HTMLElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  // The status is put in the live region after the first render: one that
  // appears with its text is often not announced.
  const [announced, setAnnounced] = useState(false);
  // The Send again error focus last moved for.
  const focusedFor = useRef<unknown>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  // Set by the dialog's confirm button; the send starts once it has closed,
  // so focus returns to a button that is not yet disabled.
  const confirmed = useRef(false);
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
  const reconnect = reconnectAdvised(message) || needsReconnect(again.error);

  useEffect(() => {
    // After the first paint, when the empty region is in place.
    const frame = requestAnimationFrame(() => setAnnounced(true));
    return () => cancelAnimationFrame(frame);
  }, []);
  const sendable = sendableAgain(message);
  useEffect(() => {
    if (!again.error || again.isPending) return;
    // 1. The button lost focus while disabled; give it back once.
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

  return (
    <section
      ref={sectionRef}
      tabIndex={-1}
      className="flex flex-col gap-4 outline-none"
      aria-label="Status"
    >
      <div className="flex flex-wrap items-center gap-3">
        <Badge variant={statusVariant(message.status)}>{statusLabel(message.status)}</Badge>
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
            statusText(message, current)
          )}
        </output>
      </div>

      {message.sentAgainAs !== null && (
        <p>
          This message was sent again as a new one.{" "}
          <Link
            to="/outbox/$id"
            params={{ id: message.sentAgainAs }}
            className={`underline underline-offset-4 ${focusClass}`}
          >
            Open the new message
          </Link>
        </p>
      )}

      <div className="flex flex-wrap items-center gap-x-6 gap-y-3">
        {sendable && (
          <Button
            ref={buttonRef}
            type="button"
            className={buttonClass}
            disabled={again.isPending}
            onClick={() => {
              // An unknown message may already have gone out, so ask first.
              if (message.status !== "unknown") return sendAgain();
              confirmed.current = false;
              setDialogOpen(true);
            }}
          >
            {again.isPending ? "Sending again..." : "Send again"}
          </Button>
        )}
        {editable(message) && (
          <Link
            to="/compose"
            search={{ edit: message.id }}
            className={buttonVariants({ className: buttonClass })}
          >
            Edit and send
          </Link>
        )}
        {reconnect && (
          <Link to="/connected-accounts" className={linkClass}>
            Open Connected accounts
          </Link>
        )}
      </div>

      <RequestError
        errors={again.error ? [{ message: sendAgainErrorMessage(again.error) }] : []}
        requestId={again.error?.requestId}
      />

      <AlertDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        onOpenChangeComplete={(open) => {
          if (open || !confirmed.current) return;
          confirmed.current = false;
          sendAgain();
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Send this message again?</AlertDialogTitle>
            <AlertDialogDescription className="wrap-anywhere">
              It may already have been sent. Check the Sent folder of{" "}
              <span translate="no">{message.from}</span> in Gmail first: if it&apos;s there, sending
              it again sends it twice.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel className={`min-h-11 border-muted-foreground ${focusClass}`}>
              Cancel
            </AlertDialogCancel>
            <AlertDialogAction
              type="button"
              className={`min-h-11 ${focusClass}`}
              onClick={() => {
                confirmed.current = true;
                setDialogOpen(false);
              }}
            >
              Send again
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  );
}
