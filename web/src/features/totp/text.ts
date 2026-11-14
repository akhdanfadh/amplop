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

// Security's two-factor row while it is on: the backup codes left of the
// most a set has, and, at low, a nudge to replace them.
export function codesLeft(left: number, most: number, low: number): string {
  const count = `${left} of ${most} backup codes left.`;
  return left <= low
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
