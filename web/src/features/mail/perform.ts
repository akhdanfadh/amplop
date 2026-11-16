import { conversationActionBodyIdsMax } from "@/api/generated/model/conversationActionBody.zod";
import { userMessage } from "@/api/problem";
import { ApiError } from "@/api/transport";
import { closeAbout, showError, shownAbout, type ToastDetail } from "@/components/ui/toast";
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
// failure to be said. A later action is also the latest for each earlier
// key its own cover (covers): Delete's "<id> place" takes an older Move
// to's "<id> place inbox" and "<id> place <label>", whose late failure no
// longer holds once the conversation moved.
let actions = 0;
const latestAction = new Map<string, number>();

// The action that said each failure still shown, by its key: a success
// closes only failures an older action said, so an older batch's late
// success never closes a newer failure under a key it covers. A key no
// toast shows any more, closed by the member or at sign-out, is let go at
// the next close (closeOlder).
const saidBy = new Map<string, number>();

// What each action changes on a conversation: where it is, whether it is
// read, or its star. Changes name what they change: each place they add
// or remove, a label among them, and each mark they set, so a second
// label's change leaves the first one's failure to be said. Archive and
// unarchive (Move to Inbox of an archived conversation) change the Inbox
// alone, so a Move to, which leaves it too, is the latest for it, while a
// label added leaves Archive's failure to be said. The others change more
// than the Inbox: their "place" covers each one place, e.g. "place inbox"
// (covers), so untrash (Move to Inbox from Trash) working closes an Undo's
// failure to put it back there.
const actionKind: Record<Action, "place" | "place inbox" | "read" | "star"> = {
  archive: "place inbox",
  unarchive: "place inbox",
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

// covers reports whether the key cover covers key: itself, or a key under
// it, one it starts with a space after, e.g. "c1 place" covers "c1 place
// inbox".
function covers(cover: string, key: string): boolean {
  return key === cover || key.startsWith(`${cover} `);
}

// slotsFor returns the keys of kinds on the conversation id: what an
// action is the latest to change on it, and what an error toast about it
// is about, so a newer answer closes only the failures it changed again.
function slotsFor(id: string, kinds: readonly string[]): string[] {
  return kinds.map((kind) => `${id} ${kind}`);
}

// Turn is one action's, or Undo's, place in the order: its number, and the
// keys it is the latest for on each conversation, by ID.
type Turn = { self: number; marked: Map<string, string[]> };

// beginTurn numbers the next action, whose own keys on each conversation
// slotsOf gives, and makes it the latest for them and for the earlier keys
// they cover.
function beginTurn(slotsOf: ReadonlyMap<string, readonly string[]>): Turn {
  const self = ++actions;
  const marked = new Map<string, string[]>();
  for (const [id, own] of slotsOf) {
    const earlier = [...latestAction.keys()].filter((key) => own.some((slot) => covers(slot, key)));
    marked.set(id, [...new Set([...own, ...earlier])]);
  }
  for (const keys of marked.values()) for (const key of keys) latestAction.set(key, self);
  return { self, marked };
}

// ownKeys returns, of turn's own keys on each conversation in slotsOf,
// those no later action took since: what its answer still speaks for.
function ownKeys(
  turn: Turn,
  slotsOf: ReadonlyMap<string, readonly string[]>,
): Map<string, string[]> {
  return new Map(
    [...slotsOf].map(([id, slots]) => [
      id,
      slots.filter((slot) => latestAction.get(slot) === turn.self),
    ]),
  );
}

// endTurn lets go of the keys turn is still the latest for.
function endTurn(turn: Turn): void {
  for (const keys of turn.marked.values()) {
    for (const key of keys) if (latestAction.get(key) === turn.self) latestAction.delete(key);
  }
}

// closeOlder closes the failures still shown that keys cover and an
// action older than turn said, each by its exact key (closeAbout).
function closeOlder(turn: Turn, keys: readonly string[]): void {
  const shown = shownAbout();
  for (const key of saidBy.keys()) if (!shown.has(key)) saidBy.delete(key);
  const older = [...saidBy].filter(
    ([key, by]) => by < turn.self && keys.some((cover) => covers(cover, key)),
  );
  for (const [key] of older) saidBy.delete(key);
  closeAbout(older.map(([key]) => key));
}

// sayFailed shows turn's error toast, title over what said returns, and
// records which keys turn said.
function sayFailed(turn: Turn, title: string, words: ReturnType<typeof said>): void {
  for (const key of words[1].about) saidBy.set(key, turn.self);
  showError(title, ...words);
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
// conversation it names, e.g. that label (closeOlder). Feedback about a
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
  // What act changes on each conversation, by the changes naming it: one
  // account's labels are not another's.
  const slotsOf = new Map<string, string[]>(
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
  const turn = beginTurn(slotsOf);
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
  const own = ownKeys(turn, slotsOf);
  const ownOf = (c: Conversation) => own.get(c.id) ?? [];
  const ours = failures.filter((f) => ownOf(f.conversation).length > 0);
  endTurn(turn);
  if (!current()) return outcomes;
  closeOlder(turn, applied.flatMap(ownOf));
  if (ours.length > 0) {
    sayFailed(
      turn,
      words.failed(many ? ours.length : undefined),
      said(ours, many, accountName, ownOf),
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
// says so when the undo fails, as perform does: it takes its turn as its
// requests are queued, and speaks only for what no later action changed
// again. Nothing shows once current says the member's mail has ended, e.g.
// after sign-out.
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
  // What each request's changes change on its conversations.
  const slotsOf = new Map<string, string[]>();
  for (const { changes } of requests) {
    for (const id of new Set(changes.flatMap((change) => change.ids))) {
      slotsOf.set(id, slotsFor(id, kindsOf(changes.filter((change) => change.ids.includes(id)))));
    }
  }
  const turn = beginTurn(slotsOf);

  let before: Promise<unknown> = Promise.resolve();
  const answers = requests.map(({ changes }) => {
    const answer = runAction(mail, { changes }, before);
    before = answer;
    return answer;
  });
  const settled = await Promise.allSettled(answers);
  const failures: Failure[] = [];
  const undone: Conversation[] = [];
  for (const [i, { batch, changes }] of requests.entries()) {
    const changed = new Set(changes.flatMap((change) => change.ids));
    const reached = batch.filter((c) => changed.has(c.id));
    const answer = settled[i];
    if (answer.status === "fulfilled") {
      const refused = new Map(answer.value.refused.map((r) => [r.id, r.reason]));
      for (const conversation of reached) {
        const reason = refused.get(conversation.id);
        if (reason === undefined) undone.push(conversation);
        else failures.push({ conversation, reason });
      }
      continue;
    }
    const err: unknown = answer.reason;
    const message = err instanceof ApiError ? userMessage(err) : undefined;
    for (const conversation of reached) failures.push({ conversation, reason: "error", message });
  }
  const own = ownKeys(turn, slotsOf);
  const ownOf = (c: Conversation) => own.get(c.id) ?? [];
  const ours = failures.filter((f) => ownOf(f.conversation).length > 0);
  endTurn(turn);
  if (!current()) return;
  closeOlder(turn, undone.flatMap(ownOf));
  if (ours.length > 0)
    sayFailed(turn, undoFailed, said(ours, shown.length > 1, accountName, ownOf));
  else if (failures.length === 0) sayUndone(subjectOf(shown));
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
