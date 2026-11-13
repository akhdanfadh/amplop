import { useEffect, useRef, useState, type ReactElement } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Link, createFileRoute, useNavigate } from "@tanstack/react-router";
import {
  useGetCurrentSession,
  useGetOutgoingMessage,
  useListConnectedAccounts,
} from "@/api/generated/inbok";
import type { ConnectedAccount, OutgoingMessage } from "@/api/generated/model";
import { userMessage } from "@/api/problem";
import { clearAccountCache } from "@/features/auth/session-cache";
import { ComposeForm } from "@/features/outgoing-messages/compose-form";
import { composeValues } from "@/features/outgoing-messages/compose-schema";
import { ReadError } from "@/components/read-error";
import { composeSearch } from "@/features/outgoing-messages/search";
import { readErrorMessage } from "@/features/outgoing-messages/text";
import { textLink } from "@/components/link-style";

const unauthorized = 401;

export const Route = createFileRoute("/_app/_mail/compose")({
  validateSearch: composeSearch,
  component: Compose,
  head: () => ({ meta: [{ title: "New message - inbok" }] }),
});

// Compose writes a new message, empty or with the content of the message
// the address's edit names, from one of the member's connected accounts.
//
// DESIGN: it is a page linked from the account navigation until the mail
// layout gives it its place beside the mailboxes.
function Compose(): ReactElement {
  const client = useQueryClient();
  const navigate = useNavigate();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const { edit } = Route.useSearch();
  const current = useGetCurrentSession();
  const list = useListConnectedAccounts();
  // The message to edit into a new one, read only when the address names one.
  const original = useGetOutgoingMessage(edit ?? "", { query: { enabled: edit !== undefined } });
  const [writeExpired, setWriteExpired] = useState(false);
  const expired =
    writeExpired ||
    [current.error, list.error, original.error].some((err) => err?.status === unauthorized);
  const accounts = list.data?.data.accounts;
  const usable = accounts?.filter((account) => account.status === "usable");
  const editing = edit === undefined ? undefined : original.data?.data;
  const ready = accounts && (edit === undefined || editing);
  // Once shown, the form stays, whatever a later read of the accounts says,
  // so typed input is never dropped; its From marks an account that can no
  // longer send, and the server refuses it.
  const [formShown, setFormShown] = useState(false);
  const showForm = ready && (formShown || (usable !== undefined && usable.length > 0));
  // Set while rendering, as React advises for state that follows a render's
  // result; React renders again at once, before showing anything.
  // Only while signed in: a session already ended has nothing typed to keep.
  if (showForm && !formShown && !expired) setFormShown(true);
  // A 401 after the form showed keeps it, so typed input can be copied;
  // before, there is nothing to lose, and the page opens sign-in.
  const signedOut = expired && formShown;

  useEffect(() => {
    headingRef.current?.focus();
  }, []);
  useEffect(() => {
    if (!expired || formShown) return;
    clearAccountCache(client);
    void navigate({ to: "/sign-in", replace: true, ignoreBlocker: true });
  }, [expired, formShown, client, navigate]);

  function showSent(message: OutgoingMessage): void {
    // The form's warning would otherwise ask before leaving a sent message.
    void navigate({ to: "/mail/outbox/$id", params: { id: message.id }, ignoreBlocker: true });
  }

  const loading = !expired && (list.isPending || (edit !== undefined && original.isPending));
  const accountsError = !expired && !list.data ? list.error : null;
  const originalError = !expired && edit !== undefined && !original.data ? original.error : null;

  return (
    <main className="mx-auto flex w-full max-w-lg flex-col gap-8 px-5 py-8 wrap-anywhere sm:px-8 sm:py-12">
      <h1 ref={headingRef} tabIndex={-1} className="text-3xl font-bold tracking-tight">
        New message
      </h1>

      {loading && <output>Loading...</output>}
      {expired && !formShown && <output>Opening sign-in...</output>}
      {signedOut && (
        <p role="alert">
          You&apos;re signed out, so this message can&apos;t be sent. Copy what you typed, then{" "}
          <Link to="/sign-in" className={textLink}>
            sign in
          </Link>
          .
        </p>
      )}
      {accountsError && (
        <ReadError
          message={`Could not load your connected accounts. ${userMessage(accountsError)}`}
          error={accountsError}
          retrying={list.isFetching}
          onRetry={() => void list.refetch()}
        />
      )}
      {originalError && (
        <ReadError
          message={readErrorMessage(originalError)}
          error={originalError}
          retrying={original.isFetching}
          onRetry={() => void original.refetch()}
        />
      )}

      {ready && !expired && !showForm && (
        <p>
          {accounts.length === 0
            ? "Connect a Gmail account to send messages from it."
            : "Each of your Gmail accounts needs reconnecting before it can send."}{" "}
          <Link to="/settings/connected-accounts" className={textLink}>
            Open Connected accounts
          </Link>
        </p>
      )}
      {showForm && (formShown || !expired) && (
        <>
          {editing && <EditNote message={editing} usable={usable ?? []} />}
          <ComposeForm
            // A new message, or another one to edit, starts afresh, with
            // its own Idempotency-Key.
            key={edit ?? "new"}
            accounts={accounts}
            initial={composeValues(
              (usable ?? []).map((account) => account.id),
              editing,
            )}
            onSent={showSent}
            onUnauthorized={() => setWriteExpired(true)}
          />
        </>
      )}
    </main>
  );
}

// EditNote says that the form starts with message's content as a new
// message, leaving message as it is, and asks for another account when
// message's own, not among usable, can't send now.
function EditNote({
  message,
  usable,
}: {
  message: OutgoingMessage;
  usable: ConnectedAccount[];
}): ReactElement {
  const own = usable.some((account) => account.id === message.connectedAccountId);
  return (
    <p>
      {message.status === "failed" && message.failure === "refused"
        ? "This is a new message with the content of the one Gmail refused, which stays in the Outbox as it is."
        : "This is a new message with the content of the one you opened, which stays in the Outbox as it is."}
      {!own && (
        <>
          {" "}
          Its account <span translate="no">{message.from}</span> can&apos;t send now, so choose
          another in From.
        </>
      )}
    </p>
  );
}
