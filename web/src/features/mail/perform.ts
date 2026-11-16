import { conversationActionBodyIdsMax } from "@/api/generated/model/conversationActionBody.zod";
import { userMessage } from "@/api/problem";
import { ApiError } from "@/api/transport";
import { closeAbout, showError, type ToastDetail } from "@/components/ui/toast";
import { isCurrent, runAction, type ActionMail, type ActionResult } from "./actions";
import type { Conversation } from "./conversations";
import { requestOf, type Action, type ConversationChange } from "./rules";
import {
  conversationCount,
  detailItem,
  refusalHeading,
  refusalSentences,
  refusedText,
  subjectText,
  undoFailed,
  type Refusal,
} from "./text";
import { sayUndone, showUndo, type UndoHandle } from "./undo";
import { actionWords, type ActionWords } from "./words";

// Outcome is how an action ended for one conversation: applied; refused,
// back as it was; gone, refused as not found and removed; or failed, the
// request with it.
export type Outcome = "applied" | "refused" | "gone" | "failed";

// Failure is why one conversation was not changed: the API's refusal, or
// its request's failure, with the failure's words when it has any.
type Failure =
  | { conversation: Conversation; reason: Refusal }
  | { conversation: Conversation; reason: "error"; message: string | undefined };

// From is where an action comes from: one conversation's own controls, or
// the picking bar, whose action is on several though it may change one.
export type From = "one" | "picked";

// The actions run so far, which numbers each, and the latest to reach each
// conversation for each thing it changes (actionKind): an answer speaks for
// a conversation only while no later action changed the same thing on it,
// so a slow batch's failure, or its closing of older toasts, never
// overrides a newer result, while a newer Star leaves an older Mark read's
// failure to be said.
let actions = 0;
const latestAction = new Map<string, number>();

// What each action changes on a conversation: where it is, whether it is
// read, or its star. Changes name what they change: each place they add
// or remove, a label among them, and each mark they set, so a second
// label's change leaves the first one's failure to be said. An action's
// "place" covers each one place, e.g. "place inbox" (closeAbout): Move to
// Inbox working closes an Undo's failure to put it back there.
const actionKind: Record<Action, "place" | "read" | "star"> = {
  archive: "place",
  unarchive: "place",
  trash: "place",
  untrash: "place",
  spam: "place",
  unspam: "place",
  read: "read",
  unread: "read",
  star: "star",
  unstar: "star",
};

// Performed is what perform runs: an action, by the provider's rules, or
// changes as they are, e.g. a move to a label, with the words that name
// them. Changes change where conversations are.
export type Performed = Action | { changes: readonly ConversationChange[]; words: ActionWords };

// kindsOf returns what changes change on a conversation: each place they
// add or remove, and each mark they set or clear.
function kindsOf(changes: readonly ConversationChange[]): string[] {
  const kinds = new Set<string>();
  for (const change of changes) {
    for (const place of [...(change.add ?? []), ...(change.remove ?? [])])
      kinds.add(`place ${place}`);
    if (change.starred !== undefined) kinds.add("star");
    if (change.unread !== undefined) kinds.add("read");
  }
  return [...kinds];
}

// slotsFor returns the keys of kinds on the conversation id: what an
// action is the latest to change on it, and what an error toast about it
// is about, so a newer answer closes only the failures it changed again.
function slotsFor(id: string, kinds: readonly string[]): string[] {
  return kinds.map((kind) => `${id} ${kind}`);
}

// Run is one request of an action: the conversations it names and its
// answer.
type Run = { batch: readonly Conversation[]; answer: Promise<ActionResult> };

// perform runs act on conversations in mail, and returns how it ended
// for each, by ID. Every guess shows at once; the requests name at most
// the contract's limit each and go one after another, so an account's
// provider calls stay within the server's own limit. An action that moves
// conversations, or acts on several, shows its Undo in the Undo bar at
// once (UndoBar), as does every action from the picking bar (from) and
// one whose words ask for it always (alwaysUndo); its Undo waits for the
// answers and sends their undo, putting back what
// changed. A refusal or failure lowers that Undo's count, closing it when
// nothing changed, and one error toast says why: for several, one
// sentence per reason and the conversations by reason under Show
// details, each with its account's name from accountName. An older error
// toast closes once newer answers changed again what failed on each
// conversation it names, e.g. that label (closeAbout). Feedback about a
// conversation a later action reached is left to that action. A refusal
// as not found also removes the row. Nothing shows once the member's mail
// has ended, e.g. after sign-out.
export async function perform({
  mail,
  conversations,
  act,
  from = "one",
  accountName,
}: {
  mail: ActionMail;
  conversations: readonly Conversation[];
  act: Performed;
  from?: From;
  accountName?: (accountId: number) => string;
}): Promise<Map<string, Outcome>> {
  const words = typeof act === "string" ? actionWords[act] : act.words;
  const current = () => isCurrent(mail);
  const many = from === "picked" || conversations.length > 1;
  actions++;
  const self = actions;
  // What act changes on each conversation, by the changes naming it: one
  // account's labels are not another's.
  const slotsOf = new Map(
    conversations.map(({ id }) => [
      id,
      slotsFor(
        id,
        typeof act === "string"
          ? [actionKind[act]]
          : kindsOf(act.changes.filter((change) => change.ids.includes(id))),
      ),
    ]),
  );
  const slots = (id: string) => slotsOf.get(id) ?? [];
  for (const conversation of conversations) {
    for (const slot of slots(conversation.id)) latestAction.set(slot, self);
  }
  const runs: Run[] = [];
  let before: Promise<unknown> = Promise.resolve();
  for (const batch of chunks(conversations, conversationActionBodyIdsMax)) {
    const request = requestOf(
      act,
      batch.map((c) => c.id),
    );
    const answer = runAction(mail, request, before);
    runs.push({ batch, answer });
    before = answer;
  }

  // What the Undo bar names, lowered as refusals come in.
  let named = conversations;
  let bar: UndoHandle | undefined;
  if (words.where !== undefined || many || words.alwaysUndo) {
    bar = showUndo({
      title: words.done,
      subject: subjectOf(conversations),
      count: conversations.length,
      where: words.where,
      onUndo: () => void undo({ mail, runs, named: () => named, accountName, current }),
    });
  }

  const outcomes = new Map<string, Outcome>();
  const failures: Failure[] = [];
  // Each answer as it is: the requests already go one after another.
  const answers = await Promise.allSettled(runs.map((run) => run.answer));
  for (const [i, { batch }] of runs.entries()) {
    const answer = answers[i];
    if (answer.status === "rejected") {
      const err: unknown = answer.reason;
      const message = err instanceof ApiError ? userMessage(err) : undefined;
      for (const conversation of batch) {
        outcomes.set(conversation.id, "failed");
        failures.push({ conversation, reason: "error", message });
      }
      continue;
    }
    const refused = new Map(answer.value.refused.map((r) => [r.id, r.reason]));
    for (const conversation of batch) {
      const reason = refused.get(conversation.id);
      if (reason === undefined) {
        outcomes.set(conversation.id, "applied");
        continue;
      }
      outcomes.set(conversation.id, reason === "not-found" ? "gone" : "refused");
      failures.push({ conversation, reason });
    }
  }

  const applied = conversations.filter((c) => outcomes.get(c.id) === "applied");
  named = applied;
  if (applied.length === 0) bar?.close();
  else if (applied.length < conversations.length) bar?.change(subjectOf(applied), applied.length);
  // What this answer still speaks for on each conversation: a later
  // action's answer speaks for what it changed again.
  const own = new Map(
    conversations.map(({ id }) => [
      id,
      slots(id).filter((slot) => latestAction.get(slot) === self),
    ]),
  );
  const ownOf = (c: Conversation) => own.get(c.id) ?? [];
  const ours = failures.filter((f) => ownOf(f.conversation).length > 0);
  const closes = applied.flatMap(ownOf);
  for (const conversation of conversations) {
    for (const slot of slots(conversation.id)) {
      if (latestAction.get(slot) === self) latestAction.delete(slot);
    }
  }
  if (!current()) return outcomes;
  closeAbout(closes);
  if (ours.length > 0) {
    showError(
      words.failed(many ? ours.length : undefined),
      ...said(ours, many, accountName, ownOf),
    );
  }
  if (failures.length === 0) return outcomes;

  // Gone at the provider: the next sync removes them too.
  const gone = failures
    .filter((f) => f.reason === "not-found" && mail.conversations.has(f.conversation.id))
    .map((f) => f.conversation.id);
  if (gone.length > 0) await mail.conversations.utils.writeDelete(gone);
  return outcomes;
}

// undo puts back what runs changed, by the changes their answers name for
// their own conversations, once each has answered, in requests that go
// one after another, and tells screen readers it did, naming the
// conversations named returns; it does nothing when nothing changed, and
// says so when the undo fails, as perform does. Nothing shows once current
// says the member's mail has ended, e.g. after sign-out.
async function undo({
  mail,
  runs,
  named,
  accountName,
  current,
}: {
  mail: ActionMail;
  runs: readonly Run[];
  named: () => readonly Conversation[];
  accountName: ((accountId: number) => string) | undefined;
  current: () => boolean;
}): Promise<void> {
  // Each answer's undo, for its own conversations only: an answer may group
  // others with them. One request each: an answer names at most as many
  // conversations as Change Conversations takes.
  const requests: { batch: readonly Conversation[]; changes: ConversationChange[] }[] = [];
  const results = await Promise.all(runs.map((run) => run.answer.catch(() => undefined)));
  for (const [i, { batch }] of runs.entries()) {
    const own = new Set(batch.map((c) => c.id));
    const changes: ConversationChange[] = [];
    for (const change of results[i]?.undo ?? []) {
      const ids = change.ids.filter((id) => own.has(id));
      if (ids.length > 0) changes.push({ ...change, ids });
    }
    if (changes.length > 0) requests.push({ batch, changes });
  }
  if (requests.length === 0 || !current()) return;
  const shown = named();

  let before: Promise<unknown> = Promise.resolve();
  const answers = requests.map(({ changes }) => {
    const answer = runAction(mail, { changes }, before);
    before = answer;
    return answer;
  });
  const settled = await Promise.allSettled(answers);
  const failures: Failure[] = [];
  for (const [i, { batch, changes }] of requests.entries()) {
    const changed = new Set(changes.flatMap((change) => change.ids));
    const reached = batch.filter((c) => changed.has(c.id));
    const answer = settled[i];
    if (answer.status === "fulfilled") {
      const refused = new Map(answer.value.refused.map((r) => [r.id, r.reason]));
      for (const conversation of reached) {
        const reason = refused.get(conversation.id);
        if (reason !== undefined) failures.push({ conversation, reason });
      }
      continue;
    }
    const err: unknown = answer.reason;
    const message = err instanceof ApiError ? userMessage(err) : undefined;
    for (const conversation of reached) failures.push({ conversation, reason: "error", message });
  }
  if (!current()) return;
  if (failures.length > 0)
    showError(
      undoFailed,
      ...said(failures, shown.length > 1, accountName, ({ id }) =>
        slotsFor(
          id,
          kindsOf(requests.flatMap((r) => r.changes).filter((change) => change.ids.includes(id))),
        ),
      ),
    );
  else sayUndone(subjectOf(shown));
}

// said returns an error toast's description and details for failures: for
// one conversation, its refusal's fix or its request's failure; for many,
// one sentence per reason, and the conversations by reason, each with its
// account's name from accountName when they span several accounts; the
// toast is about what failed on each conversation, by aboutOf.
function said(
  failures: readonly Failure[],
  many: boolean,
  accountName: ((accountId: number) => string) | undefined,
  aboutOf: (conversation: Conversation) => readonly string[],
): [string | undefined, { details?: ToastDetail[]; about: string[] }] {
  const about = failures.flatMap((f) => aboutOf(f.conversation));
  if (!many) {
    const [failure] = failures;
    const description = failure.reason === "error" ? failure.message : refusedText[failure.reason];
    return [description, { about }];
  }
  const nameOf = (c: Conversation) => accountName?.(c.accountId) ?? "";
  const of = (reason: Failure["reason"]) => failures.filter((f) => f.reason === reason);
  const description = refusalSentences({
    reconnect: [...new Set(of("needs-reconnect").map((f) => nameOf(f.conversation)))],
    gone: of("not-found").length,
    failed: of("failed").length,
    errors: failures.flatMap((f) => (f.reason === "error" && f.message ? [f.message] : [])),
  });
  const reasons = ["needs-reconnect", "not-found", "failed", "error"] as const;
  // Each conversation names its account only when they span several, as
  // the list rings rows only then: one account's name on every line says
  // nothing the reconnect sentence doesn't.
  const mixed = new Set(failures.map((f) => f.conversation.accountId)).size > 1;
  const details = reasons.flatMap((reason) => {
    const held = of(reason);
    if (held.length === 0) return [];
    return [
      {
        heading: refusalHeading[reason],
        items: held.map((f) =>
          detailItem(subjectText(f.conversation.subject), mixed ? nameOf(f.conversation) : ""),
        ),
      },
    ];
  });
  return [description, { details, about }];
}

// subjectOf returns how the Undo bar names conversations: one's subject,
// or how many, e.g. "12 conversations".
function subjectOf(conversations: readonly Conversation[]): string {
  return conversations.length === 1
    ? subjectText(conversations[0].subject)
    : conversationCount(conversations.length);
}

// chunks returns items in runs of at most size, in order.
function chunks<T>(items: readonly T[], size: number): T[][] {
  const runs: T[][] = [];
  for (let i = 0; i < items.length; i += size) runs.push(items.slice(i, i + size));
  return runs;
}
