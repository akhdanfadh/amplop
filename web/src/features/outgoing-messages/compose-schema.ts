import * as z from "zod";
import {
  NewOutgoingMessage,
  newOutgoingMessageBccMax,
  newOutgoingMessageCcMax,
  newOutgoingMessageToMax,
  type OutgoingMessage,
} from "@/api/generated/model";

// What the compose form holds. Each recipient list is the text typed into
// its field; connectedAccountId is 0 until an account is chosen.
export type ComposeValues = {
  connectedAccountId: number;
  to: string;
  cc: string;
  bcc: string;
  subject: string;
  body: string;
};

// The form's fields in page order, as the request names them.
export const composeFields = [
  "connectedAccountId",
  "to",
  "cc",
  "bcc",
  "subject",
  "body",
] as const satisfies readonly (keyof ComposeValues)[];

// Addresses in a list are separated by commas, semicolons, or spaces, none
// of which a bare address holds.
const separators = /[\s,;]+/;
// The control characters Go's unicode.IsControl finds, which the server
// refuses in a subject: one header line cannot hold them (RFC 5322).
const controlCharacter = /\p{Cc}/u;
const noRecipients = "Add at least one recipient.";
const chooseAccount = "Choose the account to send from.";
const controlInSubject = "Enter a subject without line breaks or other control characters.";

// The contract's rule for one address, the same in every list.
const address = NewOutgoingMessage.shape.to.element;

// recipientsRule checks the text of a list of at most max addresses. It
// names the first address that is not one, since the field holds many.
function recipientsRule(max: number) {
  return (text: string, ctx: z.RefinementCtx) => {
    const addresses = parseRecipients(text);
    const invalid = addresses.find((value) => !address.safeParse(value).success);
    if (invalid !== undefined)
      ctx.addIssue({
        code: "custom",
        message: `Enter email addresses separated by commas. "${invalid}" isn't one.`,
      });
    else if (addresses.length > max)
      ctx.addIssue({ code: "custom", message: `Enter at most ${max} addresses.` });
  };
}

// ComposeSchema checks the compose form against the contract's limits, plus
// the rules the contract cannot state: at least one recipient across the
// three lists, and a subject without control characters.
export const ComposeSchema = z
  .object({
    connectedAccountId: z.int().min(1, chooseAccount),
    to: z.string().superRefine(recipientsRule(newOutgoingMessageToMax)),
    cc: z.string().superRefine(recipientsRule(newOutgoingMessageCcMax)),
    bcc: z.string().superRefine(recipientsRule(newOutgoingMessageBccMax)),
    subject: NewOutgoingMessage.shape.subject.refine(
      (subject) => !controlCharacter.test(subject),
      controlInSubject,
    ),
    body: NewOutgoingMessage.shape.body,
  })
  .superRefine((values, ctx) => {
    const lists = [values.to, values.cc, values.bcc];
    if (lists.every((text) => parseRecipients(text).length === 0))
      ctx.addIssue({ code: "custom", path: ["to"], message: noRecipients });
  });

// parseRecipients returns the addresses in the text of a recipient list,
// in order, without the separators between them.
export function parseRecipients(text: string): string[] {
  return text.split(separators).filter(Boolean);
}

// composeRequest returns the Send Message body for values.
export function composeRequest(values: ComposeValues): NewOutgoingMessage {
  return {
    connectedAccountId: values.connectedAccountId,
    to: parseRecipients(values.to),
    cc: parseRecipients(values.cc),
    bcc: parseRecipients(values.bcc),
    subject: values.subject,
    body: values.body,
  };
}

// composeValues returns the form's values for a new message with the
// content of message, from message's own account while it is one of
// accountIds, the member's usable accounts. Otherwise no account is chosen,
// and without message, the lists, subject, and body start empty too.
//
// DESIGN: the member chooses the sender, since recipients see it; a default
// would send from an account they never looked at.
export function composeValues(
  accountIds: readonly number[],
  message?: OutgoingMessage,
): ComposeValues {
  const own = message && accountIds.includes(message.connectedAccountId);
  return {
    connectedAccountId: own ? message.connectedAccountId : 0,
    to: message?.to.join(", ") ?? "",
    cc: message?.cc.join(", ") ?? "",
    bcc: message?.bcc.join(", ") ?? "",
    subject: message?.subject ?? "",
    body: message?.body ?? "",
  };
}
