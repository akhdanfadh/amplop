import { userMessage } from "@/api/problem";
import { ApiError } from "@/api/transport";
import { showError } from "@/components/ui/toast";
import { isCurrent, runAction, type ActionMail, type ActionResult } from "./actions";
import type { Conversation } from "./conversations";
import type { Action } from "./rules";
import { refusedText, subjectText, undoFailed } from "./text";
import { sayUndone, showUndo } from "./undo";
import { actionWords } from "./words";

const unauthorized = 401;

// Outcome is how an action ended for its conversation: applied; refused,
// back as it was; gone, refused as not found and removed; or failed, the
// request with it.
export type Outcome = "applied" | "refused" | "gone" | "failed";

// perform runs action on conversation in mail and returns how it ended.
// An action that moves the conversation shows its Undo in the Undo bar at
// once (UndoBar); its Undo waits for the action's answer and sends the
// answer's undo, putting the conversation back where it was. A refusal or
// failure closes that Undo and says why; a refusal as not found also
// removes the row; an ended session calls onUnauthorized. Nothing shows
// once the member's mail has ended, e.g. after sign-out.
export async function perform({
  mail,
  conversation,
  action,
  onUnauthorized,
}: {
  mail: ActionMail;
  conversation: Conversation;
  action: Action;
  onUnauthorized: () => void;
}): Promise<Outcome> {
  const words = actionWords[action];
  const current = () => isCurrent(mail);
  const run = runAction(mail, { action, ids: [conversation.id] });

  let closeBar: (() => void) | undefined;
  if (words.done) {
    closeBar = showUndo({
      title: words.done,
      subject: subjectText(conversation.subject),
      where: words.where ?? "",
      onUndo: () => void undo({ mail, conversation, run, current, onUnauthorized }),
    });
  }

  let result: ActionResult;
  try {
    result = await run;
  } catch (err) {
    closeBar?.();
    if (!current()) return "failed";
    if (err instanceof ApiError && err.status === unauthorized) onUnauthorized();
    else showError(words.failed, err instanceof ApiError ? userMessage(err) : undefined);
    return "failed";
  }
  const refused = result.refused.find((r) => r.id === conversation.id);
  if (!refused) return "applied";
  closeBar?.();
  if (!current()) return "refused";
  showError(words.failed, refusedText[refused.reason]);
  if (refused.reason !== "not-found") return "refused";
  // Gone at the provider: the next sync removes it too.
  if (mail.conversations.has(conversation.id)) {
    await mail.conversations.utils.writeDelete(conversation.id);
  }
  return "gone";
}

// undo puts conversation back as it was before run, by the changes run's
// answer names for it, once it has answered, and tells screen readers it
// did; it does nothing for an action that did not change the
// conversation, and says so when the undo fails. An ended session calls
// onUnauthorized. Nothing shows once current says the member's mail has
// ended, e.g. after sign-out.
async function undo({
  mail,
  conversation,
  run,
  current,
  onUnauthorized,
}: {
  mail: ActionMail;
  conversation: Conversation;
  run: Promise<ActionResult>;
  current: () => boolean;
  onUnauthorized: () => void;
}): Promise<void> {
  const { id } = conversation;
  const result = await run.catch(() => undefined);
  // Only this conversation's: the answer may group others with it.
  const changes = [];
  for (const change of result?.undo ?? []) {
    if (change.ids.includes(id)) changes.push({ ...change, ids: [id] });
  }
  if (changes.length === 0 || !current()) return;
  let undone: ActionResult;
  try {
    undone = await runAction(mail, { changes });
  } catch (err) {
    if (!current()) return;
    if (err instanceof ApiError && err.status === unauthorized) onUnauthorized();
    else showError(undoFailed, err instanceof ApiError ? userMessage(err) : undefined);
    return;
  }
  if (!current()) return;
  const refused = undone.refused.find((r) => r.id === id);
  if (refused) showError(undoFailed, refusedText[refused.reason]);
  else sayUndone(subjectText(conversation.subject));
}
