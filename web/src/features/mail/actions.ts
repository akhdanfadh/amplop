import { createOptimisticAction } from "@tanstack/db";
import { applyConversationAction } from "@/api/generated/inbok";
import type { ConversationActionResultOutput } from "@/api/generated/model/conversationActionResult.zod";
import { toConversation, type Conversations } from "./conversations";
import { guess, guessChanges, guessedFields, type Request } from "./rules";

// ActionResult is the API's answer: the conversations it changed, as the
// provider left them, and the ones it refused, with why.
export type ActionResult = ConversationActionResultOutput;

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

// actionOn returns the optimistic action over conversations: it shows the
// guess at once, sends the action, and stores the rows the API answers
// with; a refused row has none, so it returns to its last synced state,
// and a failed request returns every row.
function actionOn(conversations: Conversations) {
  let act = actions.get(conversations);
  if (!act) {
    act = createOptimisticAction<Run>({
      onMutate: ({ request }) => {
        conversations.update(request.ids, (drafts) => {
          for (const draft of drafts)
            Object.assign(draft, guessedFields(draft, guess(draft, request)));
        });
      },
      mutationFn: async ({ request, before, onResult }) => {
        await before;
        const result = await send(conversations, request);
        onResult(result);
      },
    });
    actions.set(conversations, act);
  }
  return act;
}

// send asks the API for request, stores the rows it changed, and returns
// its answer.
async function send(conversations: Conversations, request: Request): Promise<ActionResult> {
  const result = (await applyConversationAction(request)).data;
  if (result.conversations.length > 0) {
    await conversations.utils.writeUpsert(result.conversations.map(toConversation));
  }
  return result;
}

// Each conversation's last action not yet answered, per collection.
const pending = new WeakMap<Conversations, Map<string, Promise<unknown>>>();

// runAction applies request, an action, to its conversations in
// conversations: every list shows it at once, and the API's answer
// settles it. It returns the
// answer, whose refused rows are back as they were, and throws, every row
// back, when the request fails.
//
// DESIGN: a conversation's actions reach the API one after another, each
// once the one before is answered, so a quick star and unstar end as the
// member left them; each guess still shows at once.
export async function runAction(
  conversations: Conversations,
  request: Request,
): Promise<ActionResult> {
  const { ids } = request;
  let queue = pending.get(conversations);
  if (!queue) {
    queue = new Map();
    pending.set(conversations, queue);
  }
  const before = Promise.allSettled(ids.map((id) => queue.get(id) ?? Promise.resolve()));
  const run = runAfter(conversations, request, before);
  for (const id of ids) queue.set(id, run);
  try {
    return await run;
  } finally {
    for (const id of ids) if (queue.get(id) === run) queue.delete(id);
  }
}

// runAfter shows request's guess at once, and sends it once before
// settles.
async function runAfter(
  conversations: Conversations,
  request: Request,
  before: Promise<unknown>,
): Promise<ActionResult> {
  // A guess that changes nothing records no change, and TanStack DB then
  // never runs the request (ADR031, TanStack DB gate, rule 5), e.g. a row
  // starred elsewhere since this one drew: send it directly.
  const changing = request.ids.some((id) => {
    const conversation = conversations.get(id);
    return conversation !== undefined && guessChanges(conversation, request);
  });
  if (!changing) {
    await before;
    return send(conversations, request);
  }

  let answer: ActionResult | undefined;
  const transaction = actionOn(conversations)({
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
