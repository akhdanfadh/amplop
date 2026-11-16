import { conversationActionBodyIdsMax } from "@/api/generated/model/conversationActionBody.zod";
import { userMessage } from "@/api/problem";
import { ApiError } from "@/api/transport";
import { closeAbout, showError, type ToastDetail } from "@/components/ui/toast";
import { isCurrent, runAction, type ActionMail, type ActionResult } from "./actions";
import type { Conversation } from "./conversations";
import type { Action, ConversationChange } from "./rules";
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
import { actionWords } from "./words";

const unauthorized = 401;

// Outcome is how an action ended for one conversation: applied; refused,
// back as it was; gone, refused as not found and removed; or failed, the
// request with it.
export type Outcome = "applied" | "refused" | "gone" | "failed";

// Failure is why one conversation was not changed: the API's refusal, or
// its request's failure, with the failure's words when it has any.
type Failure =
  | { conversation: Conversation; reason: Refusal }
  | { conversation: Conversation; reason: "error"; message: string | undefined };

// The actions run so far, which numbers each, and the latest to reach each
// conversation for each thing it changes (actionKind): an answer speaks for
// a conversation only while no later action changed the same thing on it,
// so a slow batch's failure, or its closing of older toasts, never
// overrides a newer result, while a newer Star leaves an older Mark read's
// failure to be said.
let actions = 0;
const latestAction = new Map<string, number>();

// What each action changes on a conversation: where it is, whether it is
// read, or its star.
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

// Run is one request of an action: the conversations it names and its
// answer.
type Run = { batch: readonly Conversation[]; answer: Promise<ActionResult> };

// perform runs action on conversations in mail, and returns how it ended
// for each, by ID. Every guess shows at once; the requests name at most
// the contract's limit each and go one after another, so an account's
// provider calls stay within the server's own limit. An action that moves
// conversations, or acts on several, shows its Undo in the Undo bar at
// once (UndoBar); its Undo waits for the answers and sends their undo,
// putting back what changed. A refusal or failure lowers that Undo's
// count, closing it when nothing changed, and one error toast says why:
// for several, one sentence per reason and the conversations by reason
// under Show details, each with its account's name from accountName. The
// conversations an answer changed no longer keep an older error toast
// open (closeAbout). Feedback about a conversation a later action reached
// is left to that action. A refusal as not found also removes the row; an ended
// session calls onUnauthorized. Nothing shows once the member's mail has
// ended, e.g. after sign-out.
export async function perform({
  mail,
  conversations,
  action,
  accountName,
  onUnauthorized,
}: {
  mail: ActionMail;
  conversations: readonly Conversation[];
  action: Action;
  accountName?: (accountId: number) => string;
  onUnauthorized: () => void;
}): Promise<Map<string, Outcome>> {
  const words = actionWords[action];
  const current = () => isCurrent(mail);
  const many = conversations.length > 1;
  actions++;
  const self = actions;
  const slot = (id: string) => `${id} ${actionKind[action]}`;
  for (const conversation of conversations) latestAction.set(slot(conversation.id), self);
  const latest = (id: string) => latestAction.get(slot(id)) === self;

  const runs: Run[] = [];
  let before: Promise<unknown> = Promise.resolve();
  for (const batch of chunks(conversations, conversationActionBodyIdsMax)) {
    const answer = runAction(mail, { action, ids: batch.map((c) => c.id) }, before);
    runs.push({ batch, answer });
    before = answer;
  }

  // What the Undo bar names, lowered as refusals come in.
  let named = conversations;
  let bar: UndoHandle | undefined;
  if (words.where !== undefined || many) {
    bar = showUndo({
      title: words.done,
      subject: subjectOf(conversations),
      count: conversations.length,
      where: words.where,
      onUndo: () =>
        void undo({ mail, runs, named: () => named, accountName, current, onUnauthorized }),
    });
  }

  const outcomes = new Map<string, Outcome>();
  const failures: Failure[] = [];
  let ended = false;
  // Each answer as it is: the requests already go one after another.
  const answers = await Promise.allSettled(runs.map((run) => run.answer));
  for (const [i, { batch }] of runs.entries()) {
    const answer = answers[i];
    if (answer.status === "rejected") {
      const err: unknown = answer.reason;
      if (err instanceof ApiError && err.status === unauthorized) ended = true;
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
  const ours = failures.filter((f) => latest(f.conversation.id));
  const closes = applied.filter((c) => latest(c.id)).map((c) => c.id);
  for (const conversation of conversations) {
    if (latest(conversation.id)) latestAction.delete(slot(conversation.id));
  }
  if (!current()) return outcomes;
  if (ended) {
    onUnauthorized();
    return outcomes;
  }
  closeAbout(closes);
  if (ours.length > 0) {
    showError(words.failed(many ? ours.length : undefined), ...said(ours, many, accountName));
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
// says so when the undo fails, as perform does. An ended session calls
// onUnauthorized. Nothing shows once current says the member's mail has
// ended, e.g. after sign-out.
async function undo({
  mail,
  runs,
  named,
  accountName,
  current,
  onUnauthorized,
}: {
  mail: ActionMail;
  runs: readonly Run[];
  named: () => readonly Conversation[];
  accountName: ((accountId: number) => string) | undefined;
  current: () => boolean;
  onUnauthorized: () => void;
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
  let ended = false;
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
    if (err instanceof ApiError && err.status === unauthorized) ended = true;
    const message = err instanceof ApiError ? userMessage(err) : undefined;
    for (const conversation of reached) failures.push({ conversation, reason: "error", message });
  }
  if (!current()) return;
  if (ended) onUnauthorized();
  else if (failures.length > 0)
    showError(undoFailed, ...said(failures, shown.length > 1, accountName));
  else sayUndone(subjectOf(shown));
}

// said returns an error toast's description and details for failures: for
// one conversation, its refusal's fix or its request's failure; for many,
// one sentence per reason, and the conversations by reason, each with its
// account's name from accountName when they span several accounts; the
// toast is about the conversations.
function said(
  failures: readonly Failure[],
  many: boolean,
  accountName: ((accountId: number) => string) | undefined,
): [string | undefined, { details?: ToastDetail[]; about: string[] }] {
  const about = failures.map((f) => f.conversation.id);
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
