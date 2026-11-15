import * as z from "zod";
import {
  NewOutgoingMessage,
  newOutgoingMessageBccMax,
  newOutgoingMessageCcMax,
  newOutgoingMessageToMax,
  type OutgoingMessage,
} from "@/api/generated/model";
import { emailAddress } from "@/api/validation";
import { invalidAddressMessage } from "./text";

// What the compose form holds. Each recipient list holds its addresses as
// the member entered them, valid or not; connectedAccountId is 0 until an
// account is chosen.
export type ComposeValues = {
  connectedAccountId: number;
  to: string[];
  cc: string[];
  bcc: string[];
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

// One entry of a list: entries are separated by commas, semicolons, or
// line breaks, none of which a bare address holds, outside quotes, since a
// quoted display name may hold them (RFC 5322 section 3.4). An unclosed
// quote runs to the end, so its text is kept, not lost.
const entry = /(?:"(?:[^"\\]|\\.)*"?|[^,;\n"])+/g;
// One word of an entry with no <address>: spaces separate addresses there
// too, outside quotes.
const word = /(?:"(?:[^"\\]|\\.)*"?|[^\s"])+/g;
// A display name in quotes, which may hold a < or >.
const quotedName = /"(?:[^"\\]|\\.)*"/g;
// The address in "Name <address>".
const angleAddress = /<([^<>]*)>/;
// The control characters Go's unicode.IsControl finds, which the server
// refuses in a subject: one header line cannot hold them (RFC 5322).
const controlCharacter = /\p{Cc}/u;
const noRecipients = "Add at least one recipient.";
export const chooseAccount = "Choose the account to send from.";
const controlInSubject = "Enter a subject without line breaks or other control characters.";

// The contract's rule for one address, the same in every list.
const address = emailAddress(NewOutgoingMessage.shape.to.element);

// isAddress reports whether value is one address the contract accepts.
export function isAddress(value: string): boolean {
  return address.safeParse(value).success;
}

// recipientsRule checks a list of at most max addresses. It names the first
// that is not one, since the field holds many.
export function recipientsRule(max: number) {
  return (addresses: string[], ctx: z.RefinementCtx) => {
    const invalid = addresses.find((value) => !isAddress(value));
    if (invalid !== undefined)
      ctx.addIssue({
        code: "custom",
        message: invalidAddressMessage(invalid),
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
    to: z.array(z.string()).superRefine(recipientsRule(newOutgoingMessageToMax)),
    cc: z.array(z.string()).superRefine(recipientsRule(newOutgoingMessageCcMax)),
    bcc: z.array(z.string()).superRefine(recipientsRule(newOutgoingMessageBccMax)),
    subject: NewOutgoingMessage.shape.subject.refine(
      (subject) => !controlCharacter.test(subject),
      controlInSubject,
    ),
    body: NewOutgoingMessage.shape.body,
  })
  .superRefine(needsRecipient);

// needsRecipient asks for a recipient on To when the lists of values are
// all empty: the API needs one across them.
export function needsRecipient(
  values: { to: string[]; cc: string[]; bcc: string[] },
  ctx: z.RefinementCtx,
): void {
  const lists = [values.to, values.cc, values.bcc];
  if (lists.every((list) => list.length === 0))
    ctx.addIssue({ code: "custom", path: ["to"], message: noRecipients });
}

// parseRecipients returns the addresses in text typed or pasted into a
// recipient list, in order: bare, or the address of "Name <address>",
// as mail clients copy them. Any other text is kept as typed, so the
// form shows it as not an address instead of dropping it.
export function parseRecipients(text: string): string[] {
  return (text.match(entry) ?? []).flatMap((part) => {
    const named = angleAddress.exec(part.replace(quotedName, ""))?.[1].trim();
    if (named) return [named];
    return part.match(word) ?? [];
  });
}

// composeRequest returns the Send Message body for values.
export function composeRequest(values: ComposeValues): NewOutgoingMessage {
  return {
    connectedAccountId: values.connectedAccountId,
    to: values.to,
    cc: values.cc,
    bcc: values.bcc,
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
    to: message?.to ?? [],
    cc: message?.cc ?? [],
    bcc: message?.bcc ?? [],
    subject: message?.subject ?? "",
    body: message?.body ?? "",
  };
}
