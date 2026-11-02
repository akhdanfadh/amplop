import type { Member } from "@/api/generated/model";
import { uncertainMessage, uncertainOutcome, userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";

// MemberChange names a write on the member list.
export type MemberChange = "deactivate" | "reactivate";

const shownDate = new Intl.DateTimeFormat(undefined, { dateStyle: "medium" });
const pastTense: Record<MemberChange, string> = {
  deactivate: "deactivated",
  reactivate: "reactivated",
};

// formatDate shows the calendar date of iso in the reader's locale and time zone.
export function formatDate(iso: string): string {
  return shownDate.format(new Date(iso));
}

// canChange reports whether the list offers to deactivate or reactivate
// member: only members, and never the account signed in as yourEmail.
export function canChange(member: Pick<Member, "role" | "email">, yourEmail: string): boolean {
  return member.role === "member" && member.email !== yourEmail;
}

// roleName returns the role as the member list shows it.
export function roleName(role: Member["role"]): string {
  return role === "admin" ? "Administrator" : "Member";
}

// changedMessage reports a change the server confirmed for email.
export function changedMessage(change: MemberChange, email: string): string {
  return `${email} is ${pastTense[change]}.`;
}

// changeErrorMessage describes a failed change for email. An unknown outcome
// asks for a look at the list, which reloads after every change.
export function changeErrorMessage(err: ApiError, change: MemberChange, email: string): string {
  if (!uncertainOutcome(err)) return `Could not ${change} ${email}. ${userMessage(err)}`;
  return uncertainMessage(
    err,
    `Could not confirm whether ${email} is ${pastTense[change]}. Check the list before trying again.`,
  );
}
