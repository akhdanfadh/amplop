import type { RegistrationSettings } from "@/api/generated/model";
import { uncertainMessage, uncertainOutcome, userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";

// RegistrationAction is a change an administrator can ask of registration,
// as the API names it in allowedActions.
export type RegistrationAction = RegistrationSettings["allowedActions"][number];

// Words is how the page names one action.
type Words = {
  // The button and the dialog's confirming button.
  action: string;
  // The verb, for a failure: "Couldn't open registration."
  verb: string;
  // The confirming button while the change is saved.
  working: string;
  // The dialog's question and what the change means.
  title: string;
  description: string;
  // Its result, announced once saved, and its past participle.
  saved: string;
  done: string;
};

// Every action the contract lists has its words, so a new one fails the
// build until the page can offer it.
const words: Record<RegistrationAction, Words> = {
  open: {
    action: "Open registration",
    verb: "open",
    working: "Opening...",
    title: "Open registration?",
    description:
      "Anyone who can reach this inbok can create a member account. Each member adds storage and sync work to this server.",
    saved: "Registration opened.",
    done: "opened",
  },
  close: {
    action: "Close registration",
    verb: "close",
    working: "Closing...",
    title: "Close registration?",
    description:
      "Sign-up links that haven't been used stop working. Existing members keep their accounts.",
    saved: "Registration closed.",
    done: "closed",
  },
};

// What each status means for visitors.
const statuses: Record<RegistrationSettings["status"], string> = {
  closed: "Closed. Visitors can't create accounts.",
  open: "Open. Anyone who can reach this inbok can create a member account.",
  // A save or a failed test clears the tested time, so "changed" would be
  // wrong after a failed test.
  paused:
    "Paused. Registration is open, but visitors can't create accounts until a test message passes with the system email settings in use.",
};

// The page's lead, its section, and its badge words.
export const registrationLead =
  "Whether visitors can create their own member account on this inbok.";
export const signUpTitle = "Sign-up";
export const signUpAbout =
  "Visitors give an email address, then choose a password from a link emailed to them.";
export const badgeText: Record<RegistrationSettings["status"], string> = {
  closed: "Closed",
  open: "Open",
  paused: "Paused",
};
export const systemEmailLink = "Go to System email";
export const readFailed = "Couldn't refresh registration.";

// Before the first save there is nothing to test, so this names both steps.
// It explains why Open is missing; the server's allowedActions decides that.
export const untestedNote =
  "Opening registration needs system email that passed a test: visitors get their sign-up link by email.";

// statusMessage says what status means for visitors.
export function statusMessage(status: RegistrationSettings["status"]): string {
  return statuses[status];
}

// actionWords returns how the page names action.
export function actionWords(action: RegistrationAction): Words {
  return words[action];
}

// offeredAction returns the action that changes status, when the server
// allows it, else none.
export function offeredAction(settings: RegistrationSettings): RegistrationAction | undefined {
  const next: RegistrationAction = settings.status === "closed" ? "open" : "close";
  return settings.allowedActions.includes(next) ? next : undefined;
}

// saveErrorMessage words a failed request to take action. Repeating an
// action changes nothing the second time, so a lost answer asks only to try
// again.
export function saveErrorMessage(err: ApiError, action: RegistrationAction): string {
  if (!uncertainOutcome(err))
    return `Couldn't ${words[action].verb} registration. ${userMessage(err)}`;
  return uncertainMessage(
    err,
    `Couldn't confirm whether registration was ${words[action].done}. Trying again is safe.`,
  );
}
