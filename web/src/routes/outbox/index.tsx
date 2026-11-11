import { useEffect, useRef, type ReactElement } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useGetCurrentSession, useListOutgoingMessages } from "@/api/generated/inbok";
import { userMessage } from "@/api/problem";
import { AccountNav } from "@/components/account-nav";
import { clearAccountCache } from "@/features/auth/session-cache";
import { OutgoingMessageList } from "@/features/outgoing-messages/list";
import { ReadError } from "@/components/read-error";
import { inProgress } from "@/features/outgoing-messages/text";

const unauthorized = 401;
// How often the list is read again while a message in it is still being sent.
const pollMs = 5_000;

export const Route = createFileRoute("/outbox/")({
  component: Outbox,
  head: () => ({ meta: [{ title: "Outbox - inbok" }] }),
});

function Outbox(): ReactElement {
  const client = useQueryClient();
  const navigate = useNavigate();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const current = useGetCurrentSession();
  const list = useListOutgoingMessages({
    query: {
      refetchInterval: (query) =>
        query.state.data?.data.messages.some((message) => inProgress(message.status))
          ? pollMs
          : false,
    },
  });
  const expired = [current.error, list.error].some((err) => err?.status === unauthorized);
  const messages = list.data?.data.messages;

  useEffect(() => {
    headingRef.current?.focus();
  }, []);
  useEffect(() => {
    if (!expired) return;
    clearAccountCache(client);
    void navigate({ to: "/sign-in", replace: true });
  }, [expired, client, navigate]);

  // A failed read without data hides the list; a failed reread keeps it.
  const readError = !expired && !list.data ? list.error : null;
  const staleError = !expired && list.data ? list.error : null;

  return (
    <>
      <AccountNav account={current.data?.data} />
      <main className="mx-auto flex w-full max-w-lg flex-col gap-8 px-5 py-8 wrap-anywhere sm:px-8 sm:py-12">
        <h1 ref={headingRef} tabIndex={-1} className="text-3xl font-bold tracking-tight">
          Outbox
        </h1>
        <p>
          The newest 50 messages you sent from inbok, with whether each one was sent. Messages sent
          from Gmail itself aren&apos;t listed.
        </p>

        {list.isPending && !expired && <output>Loading messages...</output>}
        {expired && <output>Opening sign-in...</output>}
        {readError && (
          <ReadError
            message={`Could not load your messages. ${userMessage(readError)}`}
            error={readError}
            retrying={list.isFetching}
            onRetry={() => void list.refetch()}
          />
        )}
        {staleError && (
          <ReadError
            message={`Could not reload your messages, so the list may be out of date. ${userMessage(staleError)}`}
            error={staleError}
            retrying={list.isFetching}
            onRetry={() => void list.refetch()}
          />
        )}
        {messages && !expired && <OutgoingMessageList messages={messages} />}
      </main>
    </>
  );
}
