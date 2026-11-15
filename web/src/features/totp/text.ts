import { totpStatusBackupCodesLeftMax, type TotpStatus } from "@/api/generated/model";
import type { LeaveWarningText } from "@/components/leave-warning";

// Two-factor authentication's name: Security's section and its own page.
export const twoFactorTitle = "Two-factor authentication";

// What two-factor authentication is, under its heading on Security and as
// the two-factor page's lead.
export const twoFactorAbout =
  "A code from an authenticator app at each sign-in, after your password.";

// Security's two-factor row while it is off.
export const offState = "Off. Signing in needs only your password.";

// Why an administrator must set it up, under the off state.
export const adminReason = "Administrators need it to manage this inbok.";

// Prompt replacement before the last backup codes run out.
const lowBackupCodes = 2;

// codesLeft words the backup codes left of the most a set has. New codes
// made but not saved say so, since only the page that made them can show
// them; otherwise few or none left nudge to replace them.
export function codesLeft({
  backupCodesLeft: left,
  backupCodesWaiting: waiting,
}: Pick<TotpStatus, "backupCodesLeft" | "backupCodesWaiting">): string {
  const count =
    left === 0
      ? "No backup codes left."
      : `${left} of ${totpStatusBackupCodesLeftMax} backup codes left.`;
  if (waiting)
    return left === 0
      ? `${count} New codes were made but not saved yet.`
      : `${count} New codes were made but not saved yet; your old codes still work.`;
  if (left === 0) return `${count} Make new ones so a lost authenticator doesn't lock you out.`;
  return left <= lowBackupCodes
    ? `${count} Replace them so a lost authenticator doesn't lock you out.`
    : count;
}

// The badge for two-factor authentication on.
export const onBadge = "Two-factor on";

// Security's two-factor row while its status is read, and before the reason
// a read failed.
export const checkingStatus = "Checking two-factor authentication...";
export const statusReadFailed = "Could not check two-factor authentication.";

// Security's two-factor row's way in: to manage it while on, to set it up
// while off.
export const manageLink = "Manage two-factor authentication";
export const setUpLink = "Set up two-factor authentication";

// What Security reports after the two-factor page changed something.
export const changeNotice = {
  off: "Two-factor authentication is off. Remove the inbok entry from your authenticator app.",
} as const;

// Leaving with new backup codes shown keeps the old ones working, and the
// new ones never show again: only the answer that made them held them.
export const replaceLeaveText: LeaveWarningText = {
  title: "Leave without activating the new codes?",
  description: "These codes won't show again. Your old codes keep working.",
  stay: "Stay",
  leave: "Leave anyway",
  leaveVariant: "default",
};

// A step's buttons: moving on, and what they say while they check.
export const continueLabel = "Continue";
export const checkingLabel = "Checking...";

// The codes step: its heading and lead, copying and downloading, the
// downloaded file's opening lines, and the saved button with its working
// words.
export const codesText = {
  replaceHeading: "Save your new backup codes",
  replaceLead: "Your old codes keep working until you confirm you've saved these.",
  list: "Backup codes",
  copy: "Copy codes",
  copied: "Backup codes copied.",
  copyFailed: "Could not copy. Download the codes or select and copy them yourself.",
  download: "Download codes",
  fileHeader: "inbok backup codes\nEach code signs you in once. Keep them private.\n\n",
  saved: "I've saved these codes",
  replacing: "Replacing...",
} as const;

// downloadingMessage reports the codes handed to the browser to save; it
// may still ask where, or refuse.
export function downloadingMessage(file: string): string {
  return `Downloading ${file}.`;
}

// The backup codes section while two-factor is on: its heading and what it
// is for, the replacement's lead and form names, the ways in and out, and
// what the page reports once replaced.
export const backupText = {
  title: "Backup codes",
  about: "Each one signs you in once if you lose your authenticator.",
  replace: "Replace backup codes",
  replaceLead:
    "Enter a fresh code from your authenticator app; a backup code can't do this. Your old codes keep working until you've saved the new ones.",
  keep: "Keep the current codes",
  replaced: "New backup codes are active. The old ones no longer work.",
} as const;

// The turn-off section: its heading and what it does for a member, or, for
// an administrator, why it stays on, in place of what it does; the red button, the form's lead and
// working words, the switch between code kinds, the way back, and what a
// lost answer means once the status reads on.
export const turnOffText = {
  title: "Turn off",
  about: "Removes your authenticator and every backup code; your browsers stay signed in.",
  adminReason: "Administrators must keep two-factor authentication on.",
  action: "Turn off two-factor authentication",
  lead: "Signing in will need only your password.",
  working: "Turning off...",
  useBackup: "Use a backup code",
  useAuthenticator: "Use authenticator app",
  keep: "Keep two-factor authentication",
  stillOn: "Two-factor authentication is still on. Try again with a new code.",
} as const;
