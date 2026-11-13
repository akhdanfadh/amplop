import { useEffect, useRef, useState, type ReactElement } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Link, createFileRoute, useNavigate } from "@tanstack/react-router";
import {
  useGetCurrentSession,
  useGetOutgoingMessage,
  useListConnectedAccounts,
} from "@/api/generated/inbok";
import * as z from "zod";
import { userMessage } from "@/api/problem";
import { ReadError } from "@/components/read-error";
import { clearAccountCache } from "@/features/auth/session-cache";
import { MessageView } from "@/features/messages/view";
import { messageContent, messageHeader } from "@/features/outgoing-messages/message";
import { pollWhile } from "@/features/outgoing-messages/poll";
import { MessageStatus } from "@/features/outgoing-messages/status";
import { inProgress, messageGone, readErrorMessage } from "@/features/outgoing-messages/text";
import { lineLink } from "@/components/link-style";

const unauthorized = 401;
const notFound = 404;
// How often the message is read again while it is still being sent: most
// sends end within seconds.
const pollMs = 2_000;

// A message's ID, which the client puts in the API's path as is.
const messageId = z.uuid();

export const Route = createFileRoute("/_app/_mail/mail/outbox/$id")({
  component: OutgoingMessagePage,
  head: () => ({ meta: [{ title: "Message - inbok" }] }),
});

function OutgoingMessagePage(): ReactElement {
  const client = useQueryClient();
  const navigate = useNavigate();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const { id } = Route.useParams();
  // Anything else in the address, e.g. ../ segments, would reach another
  // path of the API, so it is never read.
  const valid = messageId.safeParse(id).success;
  const current = useGetCurrentSession();
  const read = useGetOutgoingMessage(id, {
    query: {
      enabled: valid,
      refetchInterval: pollWhile(pollMs, (data) => inProgress(data.data.status)),
    },
  });
  // The sending account's address now, which Send again sends from.
  const accounts = useListConnectedAccounts();
  const [writeExpired, setWriteExpired] = useState(false);
  const expired =
    writeExpired ||
    [current.error, read.error, accounts.error].some((err) => err?.status === unauthorized);
  const message = read.data?.data;
  const account = accounts.data?.data.accounts.find((a) => a.id === message?.connectedAccountId);

  useEffect(() => {
    headingRef.current?.focus();
  }, [id]);
  useEffect(() => {
    if (!expired) return;
    clearAccountCache(client);
    void navigate({ to: "/sign-in", replace: true });
  }, [expired, client, navigate]);

  // A failed read without data hides the message; a failed reread keeps it,
  // unless the message is gone.
  const readError =
    !expired && read.error && (!message || read.error.status === notFound) ? read.error : null;
  // A kept message may show a status that has moved on since.
  const staleError = !expired && !readError && message ? read.error : null;

  return (
    <main className="mx-auto flex w-full max-w-lg flex-col gap-8 px-5 py-8 wrap-anywhere sm:px-8 sm:py-12">
      <Link to="/mail/outbox" className={lineLink}>
        Back to Outbox
      </Link>
      <h1 ref={headingRef} tabIndex={-1} className="text-3xl font-bold tracking-tight">
        Message
      </h1>

      {!valid && <p role="alert">{messageGone}</p>}
      {valid && read.isPending && !expired && <output>Loading message...</output>}
      {expired && <output>Opening sign-in...</output>}
      {readError && (
        <ReadError
          message={readErrorMessage(readError)}
          error={readError}
          retrying={read.isFetching}
          onRetry={() => void read.refetch()}
        />
      )}
      {staleError && (
        <ReadError
          message={`Could not reload this message, so its status may be out of date. ${userMessage(staleError)}`}
          error={staleError}
          retrying={read.isFetching}
          onRetry={() => void read.refetch()}
        />
      )}
      {message && !readError && !expired && (
        <>
          <MessageStatus
            // The next message starts with no earlier Send again's result.
            key={`status-${message.id}`}
            message={message}
            current={account?.email}
            onSentAgain={(copy) =>
              void navigate({ to: "/mail/outbox/$id", params: { id: copy.id } })
            }
            onUnauthorized={() => setWriteExpired(true)}
          />
          <MessageView
            key={`view-${message.id}`}
            header={messageHeader(message)}
            content={messageContent(message)}
          />
        </>
      )}
    </main>
  );
}
