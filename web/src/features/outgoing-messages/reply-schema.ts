import * as z from "zod";
import { NewReply, newReplyBccMax, newReplyCcMax, newReplyToMax } from "@/api/generated/model";
import { chooseAccount, needsRecipient, recipientsRule } from "./compose-schema";
import type { ReplyValues } from "./reply-defaults";

// The reply editor's fields in page order, as the request names them.
export const replyFields = [
  "connectedAccountId",
  "to",
  "cc",
  "bcc",
  "body",
] as const satisfies readonly (keyof ReplyValues)[];

// ReplySchema checks the reply editor against Reply to Message's limits,
// with the compose form's rules for the account and the recipients. The
// server sets the subject, so there is none to check.
export const ReplySchema = z
  .object({
    connectedAccountId: z.int().min(1, chooseAccount),
    to: z.array(z.string()).superRefine(recipientsRule(newReplyToMax)),
    cc: z.array(z.string()).superRefine(recipientsRule(newReplyCcMax)),
    bcc: z.array(z.string()).superRefine(recipientsRule(newReplyBccMax)),
    body: NewReply.shape.body,
  })
  .superRefine(needsRecipient);

// replyRequest returns the Reply to Message body for values.
export function replyRequest(values: ReplyValues): NewReply {
  return {
    connectedAccountId: values.connectedAccountId,
    to: values.to,
    cc: values.cc,
    bcc: values.bcc,
    body: values.body,
  };
}
