import type { Member } from "@/api/generated/model";
import { uncertainMessage, uncertainOutcome, userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";
import { dayDate } from "@/lib/datetime";

// MemberAction is a change an administrator can ask of an account, as the
// API names it in a member's allowedActions.
export type MemberAction = Member["allowedActions"][number];

// Words is how the page names one action.
type Words = {
  // The button, the dialog's title verb, and its confirming button.
  action: string;
  // The verb, for a failure: "Couldn't deactivate jo@example.com."
  verb: string;
  // The confirming button while the change is saved.
  working: string;
  // The past tense, for its result.
  done: string;
  // What the change means for the member, in the dialog.
  consequence: string;
};

// Every action the contract lists has its words, so a new one fails the
// build until the page can offer it.
const words: Record<MemberAction, Words> = {
  deactivate: {
    action: "Deactivate",
    verb: "deactivate",
    working: "Deactivating...",
    done: "deactivated",
    consequence:
      "They are signed out of every browser now and can't sign in until you reactivate them. Their account and sign-in details are kept.",
  },
  reactivate: {
    action: "Reactivate",
    verb: "reactivate",
    working: "Reactivating...",
    done: "reactivated",
    consequence: "They can sign in again as before.",
  },
};

// The page's lead and its section.
export const membersLead = "Everyone with an account on this inbok.";
export const accountsTitle = "Accounts";
export const accountsAbout =
  "Deactivating a member signs them out and stops them signing in. Administrators can't be deactivated.";
export const listTitle = "Member list";
export const listReadFailed = "Couldn't refresh the member list.";

// formatDate shows the calendar date of iso as inbok writes one, in the
// reader's time zone.
export function formatDate(iso: string): string {
  return dayDate.format(new Date(iso));
}

// actionWords returns how the page names action.
export function actionWords(action: MemberAction): Words {
  return words[action];
}

// offeredAction returns the action member's row offers: the one that
// changes its status, when the server allows it, else none.
export function offeredAction(member: Member): MemberAction | undefined {
  const next: MemberAction = member.deactivatedAt === null ? "deactivate" : "reactivate";
  return member.allowedActions.includes(next) ? next : undefined;
}

// changedMessage reports a change the server confirmed for email.
export function changedMessage(action: MemberAction, email: string): string {
  return `${email} is ${words[action].done}.`;
}

// changeErrorMessage describes a failed action for email. Repeating an
// action changes nothing the second time, so a lost answer asks only to try
// again.
export function changeErrorMessage(err: ApiError, action: MemberAction, email: string): string {
  if (!uncertainOutcome(err)) return `Couldn't ${words[action].verb} ${email}. ${userMessage(err)}`;
  return uncertainMessage(
    err,
    `Couldn't confirm whether ${email} was ${words[action].done}. Trying again is safe.`,
  );
}
