import { userMessage } from "@/api/problem";
import { ApiError } from "@/api/transport";
import { showError } from "@/components/ui/toast";
import { isCurrent, runAction, type ActionMail, type ActionResult } from "./actions";
import type { Conversation } from "./conversations";
import type { Action } from "./rules";
import { refusedText } from "./text";
import { actionWords } from "./words";

const unauthorized = 401;

// Outcome is how an action ended for its conversation: applied; refused,
// back as it was; gone, refused as not found and removed; or failed, the
// request with it.
export type Outcome = "applied" | "refused" | "gone" | "failed";

// perform runs action on conversation in mail and returns how it ended.
// A refusal or failure says why; a refusal as not found also removes the
// row; an ended session calls onUnauthorized. Nothing shows once the
// member's mail has ended, e.g. after sign-out.
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
  let result: ActionResult;
  try {
    result = await runAction(mail, { action, ids: [conversation.id] });
  } catch (err) {
    if (!current()) return "failed";
    if (err instanceof ApiError && err.status === unauthorized) onUnauthorized();
    else showError(words.failed, err instanceof ApiError ? userMessage(err) : undefined);
    return "failed";
  }
  const refused = result.refused.find((r) => r.id === conversation.id);
  if (!refused) return "applied";
  if (!current()) return "refused";
  showError(words.failed, refusedText[refused.reason]);
  if (refused.reason !== "not-found") return "refused";
  // Gone at the provider: the next sync removes it too.
  if (mail.conversations.has(conversation.id)) {
    await mail.conversations.utils.writeDelete(conversation.id);
  }
  return "gone";
}
