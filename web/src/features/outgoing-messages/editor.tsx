import { useRef, useState, type KeyboardEvent, type ReactElement, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import {
  getListConnectedAccountsQueryKey,
  getListOutgoingMessagesQueryKey,
} from "@/api/generated/inbok";
import { repeatableWriteRetry } from "@/api/retry";
import type { ApiError } from "@/api/transport";
import { withFieldGroup } from "@/components/form/app-form";
import { lineLink } from "@/components/link-style";
import type { Typing } from "@/components/recipient-input";
import { RequestError } from "@/components/request-error";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/cn";
import { isAddress } from "./compose-schema";
import { keyReused, mayHaveSaved, needsReconnect, sendTooltip, type LeaveState } from "./text";

const unauthorized = 401;

// The options a generated send hook takes, e.g. useSendMessage's.
type SendOptions = {
  mutation: typeof repeatableWriteRetry & {
    onError: (err: ApiError) => void;
    onSettled: () => Promise<void>;
  };
  request: { headers: { "Idempotency-Key": string } };
};

// What the message editors share about sending one message.
type EditorSend = {
  // options for the send's generated hook, e.g. useSendMessage(options).
  options: SendOptions;
  // refused records what a refused send means for the next try.
  refused: (err: ApiError) => void;
  // leaveState is what leaving asks while sending is whether a send runs.
  leaveState: (sending: boolean) => LeaveState;
  // unconfirmed is whether a try may have saved the message.
  unconfirmed: boolean;
};

// useEditorSend returns how a message editor sends one message, new or a
// reply: one Idempotency-Key per message the member means to send, so a
// repeated request, automatic or by hand, returns what the first saved;
// whether a try may have saved it; and onUnauthorized, run on a 401.
export function useEditorSend(onUnauthorized: () => void): EditorSend {
  const client = useQueryClient();
  // crypto.randomUUID needs a secure context, which signing in already
  // does: the session cookie is Secure.
  const [key, setKey] = useState(() => crypto.randomUUID());
  // Set once a try may have saved the message, and kept through later
  // edits and sends, which reset the request's error: only the Outbox can
  // tell whether it was saved.
  const [unconfirmed, setUnconfirmed] = useState(false);

  return {
    options: {
      mutation: {
        ...repeatableWriteRetry,
        onError: (err) => {
          if (err.status === unauthorized) onUnauthorized();
          // The account's status has changed since the list was read.
          if (needsReconnect(err))
            void client.invalidateQueries({ queryKey: getListConnectedAccountsQueryKey() });
        },
        // Here, not after each send, so the Outbox learns of a message
        // saved after the editor closed.
        onSettled: () => client.invalidateQueries({ queryKey: getListOutgoingMessagesQueryKey() }),
      },
      // Each attempt reads the key of the latest render, so the key changes
      // only after a send has settled, never while one runs. A quoted
      // string, as the IETF Idempotency-Key draft's Structured Field.
      request: { headers: { "Idempotency-Key": `"${key}"` } },
    },
    refused: (err) => {
      // The key saved another message, an earlier try's before the member
      // edited this one: what is sent next is a new message.
      if (keyReused(err)) setKey(crypto.randomUUID());
      if (mayHaveSaved(err)) setUnconfirmed(true);
    },
    leaveState: (sending) => (sending ? "sending" : unconfirmed ? "unconfirmed" : "typed"),
    unconfirmed,
  };
}

// EditorCard is the message editor (DESIGN.md "Compose and reply"): one
// card holding children, the editor's rows, then a bar with Send, its
// tooltip naming the shortcut, and after, e.g. Discard. Ctrl+Enter, or
// Cmd+Enter on a Mac, sends from any of its fields. look "card" draws its
// edge; "window" fills the compose window, which is the edge, its message
// taking the height left; "full-screen" also leaves Send to the window's
// top bar (send null), where a phone's keyboard can't cover it.
export function EditorCard({
  look = "card",
  send,
  after,
  children,
}: {
  look?: "card" | "window" | "full-screen";
  send: ReactNode;
  after?: ReactNode;
  children: ReactNode;
}): ReactElement {
  return (
    // Its outline, not each field's ring, marks the editor in use: a text
    // field's caret already shows where typing goes (DESIGN.md "Focus").
    // A React handler, so it runs after the field's own, e.g. a recipient
    // row keeping its typed text as chips, which React runs first as the
    // key bubbles.
    // oxlint-disable-next-line jsx-a11y/no-static-element-interactions -- keys from the fields bubble here; the fields are the controls.
    <div
      onKeyDown={sendOnShortcut}
      data-look={look}
      className={cn(
        look === "card"
          ? "overflow-hidden rounded-xl border bg-card transition-colors focus-within:border-input"
          : "flex min-h-0 flex-1 flex-col",
      )}
    >
      {look === "card" ? (
        children
      ) : (
        <div className="flex min-h-0 flex-1 flex-col overflow-y-auto">{children}</div>
      )}
      {(send || after) && (
        <div className="flex shrink-0 flex-wrap items-center gap-x-2 gap-y-1 border-t px-3 py-2">
          {/* NOTE: React Aria's Button drops aria-keyshortcuts, so the
              tooltip, its description, is what tells assistive technology
              the shortcut. */}
          {send && <SendTooltip>{send}</SendTooltip>}
          {after}
        </div>
      )}
    </div>
  );
}

// SendTooltip gives send, the editor's Send button, its tooltip naming the
// shortcut.
export function SendTooltip({ children }: { children: ReactNode }): ReactElement {
  return (
    <TooltipTrigger>
      {children}
      <Tooltip>{sendTooltip()}</Tooltip>
    </TooltipTrigger>
  );
}

// sendOnShortcut submits the form around the editor on Ctrl+Enter, or
// Cmd+Enter on a Mac, as Send would, so the form's own handling of a
// submit applies.
function sendOnShortcut(event: KeyboardEvent<HTMLDivElement>): void {
  if (event.key !== "Enter" || !(event.ctrlKey || event.metaKey)) return;
  event.preventDefault();
  event.currentTarget.closest("form")?.requestSubmit();
}

// The recipient lists an editor holds.
type Lists = { to: string[]; cc: string[]; bcc: string[] };
const noLists: Lists = { to: [], cc: [], bcc: [] };

// RecipientRows is the editor's To row, then the Cc and Bcc rows that are
// filled or that the member adds with the buttons at the end of To. It
// reports to onTyping which lists hold typed text not yet a chip.
export const RecipientRows = withFieldGroup({
  defaultValues: noLists,
  props: { onTyping: ignoreTyping },
  render: function Render({ group, onTyping }) {
    const rows = useRef<HTMLDivElement>(null);
    // The Cc and Bcc rows shown: those filled when the editor opened, then
    // those the member adds.
    const [shown, setShown] = useState<("cc" | "bcc")[]>(() =>
      (["cc", "bcc"] as const).filter((list) => group.getFieldValue(list).length > 0),
    );
    const typing = useRef<ReadonlySet<string>>(new Set());

    // report records whether list holds typed text not yet a chip.
    function report(list: string): (state: Typing) => void {
      return (state) => {
        const next = new Set(typing.current);
        if (state === "typing") next.add(list);
        else next.delete(list);
        typing.current = next;
        onTyping(next);
      };
    }

    // show shows the Cc or Bcc row and moves focus into it.
    function show(list: "cc" | "bcc"): void {
      setShown((lists) => [...lists, list]);
      // After React has drawn the row.
      requestAnimationFrame(() =>
        rows.current?.querySelector<HTMLElement>(`[data-list="${list}"] input`)?.focus(),
      );
    }

    const hidden = (["cc", "bcc"] as const).filter((list) => !shown.includes(list));
    return (
      <div ref={rows}>
        <group.AppField name="to">
          {(field) => (
            <field.RecipientsField
              label="To"
              isAddress={isAddress}
              onTyping={report("to")}
              after={
                hidden.length > 0 && (
                  <div className="flex shrink-0 gap-1">
                    {hidden.map((list) => (
                      <Button
                        key={list}
                        type="button"
                        variant="ghost"
                        size="sm"
                        aria-label={`Add ${listLabels[list]} recipients`}
                        onPress={() => show(list)}
                      >
                        {listLabels[list]}
                      </Button>
                    ))}
                  </div>
                )
              }
            />
          )}
        </group.AppField>
        {shown.map((list) => (
          <div key={list} data-list={list}>
            <group.AppField name={list}>
              {(field) => (
                <field.RecipientsField
                  label={listLabels[list]}
                  isAddress={isAddress}
                  onTyping={report(list)}
                />
              )}
            </group.AppField>
          </div>
        ))}
      </div>
    );
  },
});

const listLabels = { cc: "Cc", bcc: "Bcc" } as const;

// ignoreTyping is RecipientRows' onTyping until a form gives its own.
function ignoreTyping(_lists: ReadonlySet<string>): void {}

// The editor's recipient lists, by the same names in every editor's form.
export const recipientLists = { to: "to", cc: "cc", bcc: "bcc" } as const;

// SendFailure shows why a send failed, under the editor: errors, from the
// form's request summary, with requestId for a fault, then a way to check
// the Outbox when an earlier try may have saved the message, and a way to
// reconnect an account the server refused.
export function SendFailure({
  errors,
  requestId,
  error,
}: {
  errors: readonly { message: string }[];
  requestId: string | undefined;
  error: ApiError | null;
}): ReactElement {
  return (
    <RequestError errors={errors} requestId={requestId}>
      {/* A new tab lets the member check without losing the text here. */}
      {keyReused(error) && (
        <p>
          <Link to="/mail/outbox" target="_blank" className={lineLink}>
            Open the Outbox in a new tab
          </Link>
        </p>
      )}
      {needsReconnect(error) && (
        <p>
          <Link to="/settings/connected-accounts" className={lineLink}>
            Open Connected accounts
          </Link>
        </p>
      )}
    </RequestError>
  );
}
