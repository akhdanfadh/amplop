import type { QueryClient } from "@tanstack/react-query";
import { createOptimisticAction } from "@tanstack/db";
import {
  applyConversationAction,
  changeConversations,
  getGetConversationQueryKey,
  getListConnectedAccountsQueryKey,
  getListPlacesQueryKey,
} from "@/api/generated/inbok";
import type { ConversationActionResultOutput } from "@/api/generated/model/conversationActionResult.zod";
import type { ResponsePlacesOutput } from "@/api/generated/model/responsePlaces.zod";
import {
  currentMail,
  placeKey,
  toConversation,
  type Conversations,
  type PlaceCounts,
} from "./conversations";
import { countChanges, guess, guessChanges, guessedFields, idsOf, type Request } from "./rules";

// ActionResult is the API's answer to an action or changes: the
// conversations it changed, as the provider left them, the ones it
// refused, with why, and the changes that undo it.
export type ActionResult = ConversationActionResultOutput;

// ActionMail is what an action changes: one member's conversations and
// place counts, and the query client their reads go through.
export type ActionMail = {
  client: QueryClient;
  conversations: Conversations;
  placeCounts: PlaceCounts;
};

// isCurrent reports whether mail is still its client's mail: false once
// the member's session ended (clearAccountCache), after which the client
// may hold the next member's mail, or a failed list replaced the
// conversations (restartMail).
export function isCurrent(mail: ActionMail): boolean {
  const now = currentMail(mail.client);
  return now?.conversations === mail.conversations && now.placeCounts === mail.placeCounts;
}

// MailEnded is the error of a request not sent because its mail ended
// while it waited (isCurrent).
export class MailEnded extends Error {
  constructor() {
    super("The member's mail ended before the action was sent.");
    this.name = "MailEnded";
  }
}

// What one run takes: the request, what it waits for before sending, and
// where its answer goes.
type Run = {
  request: Request;
  before: Promise<unknown>;
  onResult: (result: ActionResult) => void;
};

type Act = ReturnType<typeof createOptimisticAction<Run>>;

// One optimistic action per collection, for each member's mail.
const actions = new WeakMap<Conversations, Act>();

// actionOn returns the optimistic action over mail: it shows the guess
// and the unread counts it changes at once, sends the request, and stores
// the rows and counts the API answers with; a refused row has none, so it
// returns to its last synced state, and a failed request returns every row
// and count.
function actionOn(mail: ActionMail) {
  const { conversations, placeCounts } = mail;
  let act = actions.get(conversations);
  if (!act) {
    act = createOptimisticAction<Run>({
      onMutate: ({ request }) => {
        const shown = idsOf(request).flatMap((id) => conversations.get(id) ?? []);
        const guessed = new Map(shown.map((row) => [row.id, guess(row, request)]));
        const by = new Map<string, number>();
        for (const conversation of shown) {
          for (const change of countChanges(
            conversation,
            guessed.get(conversation.id) ?? conversation,
          )) {
            const key = placeKey(change.accountId, change.place);
            by.set(key, (by.get(key) ?? 0) + change.by);
          }
        }
        conversations.update(
          shown.map((conversation) => conversation.id),
          (drafts) => {
            for (const draft of drafts) {
              const after = guessed.get(draft.id);
              if (after) Object.assign(draft, guessedFields(draft, after));
            }
          },
        );
        const counted = [...by].filter(([key, n]) => n !== 0 && placeCounts.has(key));
        if (counted.length === 0) return;
        placeCounts.update(
          counted.map(([key]) => key),
          (drafts) => {
            for (const draft of drafts) {
              const n = by.get(placeKey(draft.accountId, draft.place)) ?? 0;
              draft.unread = Math.max(0, draft.unread + n);
            }
          },
        );
      },
      mutationFn: async ({ request, before, onResult }) => {
        await before;
        const result = await send(mail, request);
        onResult(result);
      },
    });
    actions.set(conversations, act);
  }
  return act;
}

// Requests in flight, per member's counts, and whether two have been in
// flight at once since none was.
const inFlight = new WeakMap<PlaceCounts, { running: number; overlapped: boolean }>();

// send asks the API for request, stores the rows it changed and the
// unread counts it answered with, and returns its answer. Requests in
// flight at once keep no answer's counts: the last to settle reads them
// again. A conversation refused as needs-reconnect reads the accounts
// again, so one that broke since they were read takes no more actions
// (its allowedActions lose change-mail) and the sidebar offers Reconnect.
// It throws MailEnded, sending nothing, once mail has ended, and an answer
// that comes after that stores nothing.
//
// DESIGN: each answer's counts are the whole of List Places as the server
// had them then, and answers arrive in any order, so an older answer's
// would overwrite a newer one's (ADR031, "Mail actions as built",
// checkpoint 7c).
//
// DESIGN: the session can end during any wait, and the query client then
// holds the next member's reads under the same keys, so it checks that
// mail is current (isCurrent) before sending and after every wait, before
// it touches a collection or the query cache.
async function send(mail: ActionMail, request: Request): Promise<ActionResult> {
  const { client, conversations, placeCounts } = mail;
  if (!isCurrent(mail)) throw new MailEnded();
  let flight = inFlight.get(placeCounts);
  if (!flight) {
    flight = { running: 0, overlapped: false };
    inFlight.set(placeCounts, flight);
  }
  flight.running++;
  if (flight.running > 1) flight.overlapped = true;
  let result: ActionResult;
  try {
    result = (
      "action" in request
        ? await applyConversationAction(request)
        : await changeConversations({ changes: request.changes })
    ).data;
  } catch (err) {
    if (settle(flight) === "read" && isCurrent(mail)) {
      void client.refetchQueries({ queryKey: getListPlacesQueryKey(), type: "active" });
    }
    throw err;
  }
  const counts = settle(flight);
  if (!isCurrent(mail)) return result;
  if (result.refused.some((r) => r.reason === "needs-reconnect")) {
    void client.invalidateQueries({ queryKey: getListConnectedAccountsQueryKey() });
  }
  // NOTE: a list's read in flight never lands over these rows: TanStack DB
  // keeps a direct write over a read that started before it.
  if (result.conversations.length > 0) {
    await conversations.utils.writeUpsert(result.conversations.map(toConversation));
    if (!isCurrent(mail)) return result;
  }
  // 1. Drop the counts' reads in flight, which the query cache would let
  // land over the answer's counts, showing them as before the action until
  // the next refresh. A first read carries on: a page waits for it.
  await client.cancelQueries({
    queryKey: getListPlacesQueryKey(),
    predicate: (query) => query.state.data !== undefined,
  });
  if (!isCurrent(mail)) return result;
  // 2. Store the answer's counts, alone in flight; else the last to settle
  // reads them again, as does one whose counting failed, so the guessed
  // ones give way to the server's rather than to the old ones.
  if (counts === "store" && result.places) {
    const places: ResponsePlacesOutput = { data: { places: result.places } };
    client.setQueryData(getListPlacesQueryKey(), places);
  } else if (counts !== "drop") {
    await client.refetchQueries({ queryKey: getListPlacesQueryKey(), type: "active" });
  }
  return result;
}

// Counts is what becomes of an answer's counts: stored; dropped, as
// another request was in flight with it; or read again, by the last of
// those to settle.
type Counts = "store" | "drop" | "read";

// settle ends one of flight's requests, and returns what becomes of its
// answer's counts.
function settle(flight: { running: number; overlapped: boolean }): Counts {
  flight.running--;
  if (!flight.overlapped) return "store";
  if (flight.running > 0) return "drop";
  flight.overlapped = false;
  return "read";
}

// Each conversation's last request not yet answered, per collection.
const pending = new WeakMap<Conversations, Map<string, Promise<unknown>>>();

// runAction applies request, an action or changes, to its conversations in
// mail: every list and unread count shows it at once, and the API's answer
// settles it, sent once after, when given, has settled, e.g. the batch
// before it. It returns the answer, whose refused rows are back as they
// were, and throws, every row back, when the request fails. Once it
// settles, an open conversation's messages are read again, for their own
// flags, while mail is current. A request still waiting when mail ends is
// never sent: it throws MailEnded.
//
// DESIGN: a conversation's requests reach the API one after another, each
// once the one before is answered, so a quick star and unstar end as the
// member left them, and an Undo pressed at once waits for the answer it
// undoes; each guess still shows at once.
export async function runAction(
  mail: ActionMail,
  request: Request,
  after: Promise<unknown> = Promise.resolve(),
): Promise<ActionResult> {
  const { client, conversations } = mail;
  const ids = idsOf(request);
  let queue = pending.get(conversations);
  if (!queue) {
    queue = new Map();
    pending.set(conversations, queue);
  }
  const before = Promise.allSettled([
    after,
    ...ids.map((id) => queue.get(id) ?? Promise.resolve()),
  ]);
  const run = runAfter(mail, request, before);
  for (const id of ids) queue.set(id, run);
  try {
    return await run;
  } finally {
    const reading = isCurrent(mail);
    for (const id of ids) {
      if (queue.get(id) === run) queue.delete(id);
      // Only a conversation open in the reading pane is read now.
      if (reading) void client.invalidateQueries({ queryKey: getGetConversationQueryKey(id) });
    }
  }
}

// runAfter shows request's guess at once, and sends it once before
// settles.
async function runAfter(
  mail: ActionMail,
  request: Request,
  before: Promise<unknown>,
): Promise<ActionResult> {
  // A guess that changes nothing records no change, and TanStack DB then
  // never runs the request (ADR031, TanStack DB gate, rule 5), e.g. a row
  // starred elsewhere since this one drew: send it directly.
  const changing = idsOf(request).some((id) => {
    const conversation = mail.conversations.get(id);
    return conversation !== undefined && guessChanges(conversation, request);
  });
  if (!changing) {
    await before;
    return send(mail, request);
  }

  let answer: ActionResult | undefined;
  const transaction = actionOn(mail)({
    request,
    before,
    onResult: (result) => {
      answer = result;
    },
  });
  // Rejects with the request's error, after TanStack DB put every row back.
  await transaction.when("settled");
  if (!answer) throw new Error("The action ended without an answer.");
  return answer;
}
