import type { ReactElement } from "react";
import { createFileRoute, linkOptions, notFound } from "@tanstack/react-router";
import { getGetConversationQueryOptions } from "@/api/generated/inbok";
import { ApiError } from "@/api/transport";
import { conversationIdSchema } from "@/features/mail/address";
import { isSessionEnded, sessionEnded } from "@/features/auth/guard";
import { currentMail } from "@/features/mail/conversations";
import { Reader } from "@/features/mail/reader";

const notFoundStatus = 404;

// A conversation open in the reading pane, /mail/<place>/<conversation id>.
export const Route = createFileRoute("/_app/_mail/mail/$place/$conversationId")({
  params: {
    parse: ({ conversationId }) => {
      if (!conversationIdSchema.safeParse(conversationId).success) throw notFound();
      return { conversationId };
    },
  },
  // NOTE: TanStack keeps a route's loader in the app's first download
  // unless it is split off, and this one reads mail's TanStack DB
  // collection, which must load only with mail's pages.
  codeSplitGroupings: [["loader"], ["component"]],
  // A conversation the list holds draws from its row at once while its
  // messages load (DESIGN.md "States"); one it doesn't, e.g. from a link,
  // waits for its read, and one that does not exist is Page not found. An
  // ended session opens sign-in.
  loader: async ({ context: { queryClient }, params: { conversationId }, location }) => {
    const options = getGetConversationQueryOptions(conversationId);
    if (currentMail(queryClient)?.conversations.has(conversationId)) {
      // A failed read shows in the reading pane, which reads again.
      void queryClient.query(options).catch(() => undefined);
      return;
    }
    try {
      await queryClient.query({ ...options, staleTime: "static" });
    } catch (err) {
      if (err instanceof ApiError && err.status === notFoundStatus) throw notFound();
      if (isSessionEnded(err)) throw sessionEnded(queryClient, location.href);
      throw err;
    }
  },
  // The reader names the tab after the conversation's subject.
  component: Conversation,
});

function Conversation(): ReactElement {
  const { place, conversationId } = Route.useParams();
  const { account } = Route.useSearch();
  return (
    <Reader
      conversationId={conversationId}
      back={linkOptions({ to: "/mail/$place", params: { place }, search: { account } })}
    />
  );
}
