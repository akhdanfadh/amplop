import type { ListedSession, ProblemType } from "@/api/generated/model";
import { uncertainMessage, uncertainOutcome, userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";

const notFound = 404;
const sessionNotFoundType: ProblemType = "/api/v0/problems/session-not-found";
// The server records activity at most once an hour.
const activityWindowMs = 60 * 60 * 1000;
const shownTime = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });

// browserName names a session by its browser and operating system, when known.
export function browserName(session: Pick<ListedSession, "browser" | "os">): string {
  const browser = session.browser.trim() || "Unknown browser";
  const os = session.os.trim();
  return os ? `${browser} on ${os}` : browser;
}

// formatTime shows a timestamp in the reader's locale and time zone.
export function formatTime(iso: string): string {
  return shownTime.format(new Date(iso));
}

// lastActive describes when a session was last used, without an exact time
// within the last hour.
export function lastActive(lastActiveAt: string, now: number): string {
  if (now - Date.parse(lastActiveAt) < activityWindowMs) return "Active in the last hour";
  return `Last active ${formatTime(lastActiveAt)}`;
}

// endMessage describes a failed sign-out of one browser, when named, or of all others.
export function endMessage(err: ApiError, name?: string): string {
  // Only the API's own 404 says the session is gone; a missing route says nothing.
  if (name && err.status === notFound && err.problem?.type === sessionNotFoundType)
    return `${name} was already signed out.`;
  if (!uncertainOutcome(err)) return userMessage(err);
  return uncertainMessage(
    err,
    "Could not confirm the sign-out. Check the list before trying again.",
  );
}

// endedOthersMessage reports how many other browsers a sign-out ended.
export function endedOthersMessage(ended: number): string {
  if (ended === 0) return "No other browsers were signed in.";
  return `Signed out of ${ended} other ${ended === 1 ? "browser" : "browsers"}.`;
}

// Security's signed-in browsers section: its heading, what it is for, and
// what signing out cannot do.
export const sessionsTitle = "Signed-in browsers";
export const sessionsAbout = "The browsers signed in to your account.";
export const signOutLimit =
  "Signing out a browser doesn't stop someone who knows your password from signing in.";
