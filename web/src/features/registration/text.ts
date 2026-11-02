import type { RegistrationSettings } from "@/api/generated/model";
import { uncertainMessage, uncertainOutcome, userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";

// ShownStatus is the status read from the API, or "unknown" when a change
// may have been saved but the status could not be read since.
export type ShownStatus = RegistrationSettings["status"] | "unknown";

// RegistrationChange is what the administrator asks for: "open" or "close".
export type RegistrationChange = "open" | "close";

// Confirmation is the dialog that asks before a change.
export type Confirmation = { title: string; description: string; action: string };

// What each status means for visitors.
const statuses: Record<ShownStatus, string> = {
  closed: "Registration is closed. Visitors can't create accounts.",
  open: "Registration is open. Visitors can create accounts.",
  // A save or a failed test clears the tested time, so "changed" would be
  // wrong after a failed test.
  paused:
    "Registration is paused: the system email settings in use haven't passed a test, so visitors can't create accounts. It resumes once a test message passes.",
  unknown: "We couldn't confirm whether registration is open. Reload the page to check.",
};

// Before the first save there is nothing to test, so this names both steps.
const untested =
  "To open registration, save the system email settings and send a test message that passes. Visitors get their sign-up link by email.";

const confirmations: Record<RegistrationChange, Confirmation> = {
  open: {
    title: "Open registration?",
    description:
      "Anyone who can reach this inbok can create a member account. Each member adds storage and sync work to this server.",
    action: "Open registration",
  },
  close: {
    title: "Close registration?",
    description:
      "Sign-up links that haven't been used stop working. Existing members keep their accounts.",
    action: "Close registration",
  },
};

const pending: Record<RegistrationChange, string> = {
  open: "Opening registration...",
  close: "Closing registration...",
};

const saved: Record<RegistrationChange, string> = {
  open: "Registration opened.",
  close: "Registration closed.",
};

const changed: Record<RegistrationChange, string> = {
  open: "opened",
  close: "closed",
};

// statusMessage says what status means for visitors.
export function statusMessage(status: ShownStatus): string {
  return statuses[status];
}

// untestedMessage says what opening registration waits for.
export function untestedMessage(): string {
  return untested;
}

// confirmation returns the dialog that asks before change.
export function confirmation(change: RegistrationChange): Confirmation {
  return confirmations[change];
}

// pendingMessage says change is being saved.
export function pendingMessage(change: RegistrationChange): string {
  return pending[change];
}

// savedMessage announces a save that made change.
export function savedMessage(change: RegistrationChange): string {
  return saved[change];
}

// saveErrorMessage words a failed request to make change.
export function saveErrorMessage(err: ApiError, change: RegistrationChange): string {
  if (!uncertainOutcome(err)) return userMessage(err);
  // Saving the same choice twice changes nothing, so trying again is safe.
  return uncertainMessage(
    err,
    `We couldn't confirm whether registration was ${changed[change]}. Try again.`,
  );
}
