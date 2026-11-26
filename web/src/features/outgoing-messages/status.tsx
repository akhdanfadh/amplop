import { useEffect, useRef, useState, type ReactElement } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import {
  getGetOutgoingMessageQueryKey,
  getListOutgoingMessagesQueryKey,
  useDismissOutgoingMessage,
  useSendMessageAgain,
} from "@/api/generated/amplop";
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
import { Button, buttonVariants } from "@/components/ui/button";
import { accountDot } from "@/components/account-mark";
import { stepEnter } from "@/components/step-change";
import { formatReceived } from "@/features/messages/text";
import { cn } from "@/lib/cn";
import { useCompose } from "./compose-store";
import { MessageStatusMark } from "./status-badge";
import {
  dismissErrorMessage,
  dismissLabel,
  needsReconnect,
  nextSteps,
  notSentPrefix,
  sendAgainErrorMessage,
  sentSummary,
} from "./text";
import { lineLink } from "@/components/link-style";

const buttonClass = "w-fit whitespace-normal";
// A link in the status, at the 14 px of its text and buttons.
const statusLink = cn(lineLink, "text-sm");

// MessageStatus shows what happened to message and what the member can do
// next, as nextSteps works out from accounts, the member's connected
// accounts (undefined until they load): send it again, edit it into a new
// message, reconnect its account, or open the copy it was sent again as;
// and, while the API allows it, dismiss it.
// onSentAgain receives the copy a Send again saved. onEdit, when given, runs on Edit and send instead of opening the
// compose window with message's content, e.g. so a reply is edited into a
// new reply. look "window" is the compose window's summary after Send (DESIGN.md "Compose
// and reply"): a mark, one line, and the sending account in its colour,
// with the actions in a bar when there are any, and Open in Outbox;
// "pane" is that summary in a card, for the Outbox's reading pane, which
// leaves out Open in Outbox (DESIGN.md "Outbox").
export function MessageStatus({
  message,
  accounts,
  onSentAgain,
  onEdit,
  look,
}: {
  message: OutgoingMessage;
  accounts: ListedAccount[] | undefined;
  onSentAgain: (copy: OutgoingMessage) => void;
  onEdit?: () => void;
  look: "window" | "pane";
}): ReactElement {
  const client = useQueryClient();
  const compose = useCompose().store;
  const sectionRef = useRef<HTMLElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const dismissRef = useRef<HTMLButtonElement>(null);
  // The status is put in the live region after the first render: one that
  // appears with its text is often not announced.
  const [announced, setAnnounced] = useState(false);
  // The Send again error focus last moved for.
  const focusedFor = useRef<unknown>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  // The status the pane opened on, which shows in place: the member opens
  // messages often, and by keyboard, so only a change while open rises.
  const [opened] = useState(message.status);
  const dismissed = message.dismissedAt !== null;
  const [openedDismissed] = useState(dismissed);
  const again = useSendMessageAgain({
    mutation: {
      // A repeat returns the copy the first saved.
      ...repeatableWriteRetry,
      // A refusal can mean the status changed; any outcome may have saved a copy.
      onSettled: () => {
        void client.invalidateQueries({ queryKey: getGetOutgoingMessageQueryKey(message.id) });
        void client.invalidateQueries({ queryKey: getListOutgoingMessagesQueryKey() });
      },
    },
  });
  const dismiss = useDismissOutgoingMessage({
    mutation: {
      // A repeat returns the message unchanged.
      ...repeatableWriteRetry,
      // Returned, so the button stays pending until the reads show the
      // message dismissed; a refusal can mean the status changed.
      onSettled: () =>
        Promise.all([
          client.invalidateQueries({ queryKey: getGetOutgoingMessageQueryKey(message.id) }),
          client.invalidateQueries({ queryKey: getListOutgoingMessagesQueryKey() }),
        ]),
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
  const dismissible = message.allowedActions.includes("dismiss");
  // A Dismiss whose answer was lost but which the reread shows done needs
  // no word: its advice, to try again, has no button left.
  const dismissFailure = dismissed ? null : dismiss.error;
  useEffect(() => {
    // Dismiss refused because the message changed: the reread can remove
    // the button, and focus falls to the page; catch it, so the error
    // after is read next.
    if (dismiss.error && !dismissible && document.activeElement === document.body)
      sectionRef.current?.focus();
  }, [dismiss.error, dismissible]);

  function sendAgain(): void {
    if (again.isPending) return;
    again.mutate({ id: message.id }, { onSuccess: (result) => onSentAgain(result.data) });
  }

  const outboxLink = look === "window";
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
      {dismissible && (
        // Outlined: the way out that sends nothing, beside the way that
        // sends.
        <Button
          ref={dismissRef}
          type="button"
          variant="outline"
          className={buttonClass}
          isPending={dismiss.isPending}
          onPress={() => {
            if (dismiss.isPending) return;
            dismiss.mutate(
              { id: message.id },
              {
                onSuccess: () => {
                  // The button goes once dismissed; focus moves to the
                  // status, which says so, unless the member moved on
                  // while it waited.
                  const active = document.activeElement;
                  if (active === document.body || active === dismissRef.current)
                    sectionRef.current?.focus();
                },
              },
            );
          }}
        >
          {dismiss.isPending ? "Dismissing..." : dismissLabel}
        </Button>
      )}
      {message.sentAgainAs !== null && (
        // The way on from a message sent again, so drawn as its button
        // though it leads elsewhere, as an outcome's ways on are (DESIGN.md
        // "Outcome screens").
        <Link
          to="/mail/outbox/$id"
          params={{ id: message.sentAgainAs }}
          className={cn(buttonVariants(), buttonClass)}
        >
          See how the new try went
        </Link>
      )}
      {reconnect && (
        <Link to="/settings/connected-accounts" className={statusLink}>
          Open Connected accounts
        </Link>
      )}
    </>
  );
  const offersAny =
    sendable ||
    steps.offers.includes("edit") ||
    dismissible ||
    reconnect ||
    message.sentAgainAs !== null;

  return (
    <section
      ref={sectionRef}
      tabIndex={-1}
      className={cn("flex flex-col outline-none", look === "pane" && "rounded-lg border bg-card")}
      aria-label="Status"
    >
      {/* Stays mounted, so each change of status is announced. */}
      <output aria-atomic="true" className="block">
        {announced && (
          // A new status rises into place, as a step does (DESIGN.md "Motion").
          <span
            key={`${message.status}-${dismissed}`}
            className={cn(
              "flex items-start gap-3 p-4",
              (look !== "pane" || message.status !== opened || dismissed !== openedDismissed) &&
                stepEnter,
            )}
          >
            <MessageStatusMark message={message} />
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
        {!offersAny && outboxLink && (
          <p>
            <Link to="/mail/outbox/$id" params={{ id: message.id }} className={statusLink}>
              Open in Outbox
            </Link>
          </p>
        )}
      </div>
      {offersAny && (
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-t px-3 py-2">
          {actions}
          {outboxLink && (
            <Link to="/mail/outbox/$id" params={{ id: message.id }} className={statusLink}>
              Open in Outbox
            </Link>
          )}
        </div>
      )}

      <div className="px-4 pb-4 empty:hidden">
        <RequestError
          errors={[
            ...(again.error ? [{ message: sendAgainErrorMessage(again.error) }] : []),
            ...(dismissFailure ? [{ message: dismissErrorMessage(dismissFailure) }] : []),
          ]}
          requestId={again.error?.requestId ?? dismissFailure?.requestId}
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
