import type { ReactElement } from "react";
import { createFileRoute, linkOptions, notFound } from "@tanstack/react-router";
import * as z from "zod";
import { cachedRead } from "@/api/cached-read";
import { getGetConversationQueryOptions } from "@/api/generated/inbok";
import { ApiError } from "@/api/transport";
import { conversationIdSchema } from "@/features/mail/address";
import { isSessionEnded, sessionEnded } from "@/features/auth/guard";
import { currentMail } from "@/features/mail/conversations";
import { listKey } from "@/features/mail/focus-request";
import { searchPlace } from "@/features/mail/places";
import { Reader } from "@/features/mail/reader";
import { searchPlaces } from "@/features/mail/search";

const notFoundStatus = 404;

// A conversation open in the reading pane, /mail/<place>/<conversation id>;
// from a search, /mail/search/<conversation id>?message=<message id>, at
// the message the search matched.
export const Route = createFileRoute("/_app/_mail/mail/$place/$conversationId")({
  params: {
    parse: ({ conversationId }) => {
      if (!conversationIdSchema.safeParse(conversationId).success) throw notFound();
      return { conversationId };
    },
  },
  // A message that is no ID, e.g. from an edited link, is dropped: the
  // conversation opens as usual.
  validateSearch: z.object({ message: z.uuid().optional().catch(undefined) }),
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
      // Not cachedRead, and not awaited: this read only refreshes the
      // messages, which the pane shows in place once they come (web/README.md
      // "Routes"). A failed read shows in the reading pane, which reads again.
      void queryClient.query(options).catch(() => undefined);
      return;
    }
    try {
      await cachedRead(queryClient, options);
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
  const { message, ...search } = Route.useSearch();
  const { account } = search;
  // A search's list keeps a conversation in any of the places it searches,
  // and its Back returns to the whole search.
  const searched = place === searchPlace;
  return (
    <Reader
      conversationId={conversationId}
      list={{
        places: searched ? searchPlaces(search) : [place],
        account,
        key: listKey(place, account),
        back: linkOptions({
          to: "/mail/$place",
          params: { place },
          search: searched ? search : { account },
        }),
      }}
      startAt={message}
    />
  );
}
