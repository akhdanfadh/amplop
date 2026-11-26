import { hashKey, type QueryClient } from "@tanstack/react-query";
import {
  getGetCurrentSessionQueryKey,
  getGetSetupStatusQueryKey,
  getGetTOTPStatusQueryKey,
  getListSessionsQueryKey,
} from "@/api/generated/amplop";
import { closeToasts } from "@/components/ui/toast";
import { forgetMailHref } from "@/features/mail/places";

// What else holds a member's data, each cleared with the query cache.
const clearers = new Set<(client: QueryClient) => void>();

// onAccountCacheClear has clear run whenever clearAccountCache does, with
// its client, e.g. to drop mail's TanStack DB collections, and returns the
// function that stops it.
//
// DESIGN: a module that holds a member's data registers itself, so this
// module, which sign-in loads, never imports the mail pages' code.
export function onAccountCacheClear(clear: (client: QueryClient) => void): () => void {
  clearers.add(clear);
  return () => clearers.delete(clear);
}

// The channel to this browser's other amplop tabs, opened on first use.
let channel: BroadcastChannel | undefined;

// otherTabs returns the channel that tells this browser's other amplop tabs
// that one member's data ended here; undefined where the browser lacks
// one, and other tabs then find out from their next 401, as before.
//
// NOTE: opened on first use, never as the module loads: the build renders
// the app's shell in Node, whose open BroadcastChannel would keep the
// build from ever exiting.
function otherTabs(): BroadcastChannel | undefined {
  if (!channel && typeof BroadcastChannel !== "undefined") {
    channel = new BroadcastChannel("amplop-account");
  }
  return channel;
}

// onAccountEndedElsewhere runs ended whenever another tab of this browser
// clears its account data, e.g. as a member signs out or another signs
// in there; it returns the function that stops it.
export function onAccountEndedElsewhere(ended: () => void): () => void {
  const tabs = otherTabs();
  if (!tabs) return () => undefined;
  tabs.addEventListener("message", ended);
  return () => tabs.removeEventListener("message", ended);
}

// clearAccountCache ends what the app holds for one member, as
// clearTabAccountData does, and tells the browser's other amplop tabs.
// Every sign-in, sign-out, and ended session calls it.
export function clearAccountCache(client: QueryClient): void {
  // oxlint-disable-next-line unicorn/require-post-message-target-origin -- a BroadcastChannel takes no origin; it reaches this site's tabs only.
  otherTabs()?.postMessage("ended");
  clearTabAccountData(client);
}

// endTabAccount runs when another tab ended its member's session: it clears
// what this tab holds for the member, as clearTabAccountData does, then
// reload loads the page again, whose guard reads whose session the
// browser holds.
export function endTabAccount(client: QueryClient, reload: () => void): void {
  // 1. Clear it first: a page holding typed text, e.g. a reply being
  // written, asks before the reload, and staying must keep nothing of the
  // member's.
  clearTabAccountData(client);
  reload();
}

// clearTabAccountData ends what this tab holds for one member, without
// telling other tabs, e.g. when another tab ended it: it removes account
// data and mutation variables, keeping the public setup status, closes
// every toast, sends Back to mail to the Inbox, and runs what
// onAccountCacheClear registered.
export function clearTabAccountData(client: QueryClient): void {
  closeToasts();
  forgetMailHref();
  for (const clear of clearers) clear(client);
  const setupHash = hashKey(getGetSetupStatusQueryKey());
  client.removeQueries({ predicate: (query) => query.queryHash !== setupHash });
  client.getMutationCache().clear();
}

// The reads that describe a session rather than the member's data: the
// session itself, its two-factor status, and the browsers signed in.
const sessionKeys = [
  getGetCurrentSessionQueryKey(),
  getGetTOTPStatusQueryKey(),
  getListSessionsQueryKey(),
];

// resetSession drops what the app read about a session whose cookie was just
// replaced for the same member, e.g. as two-factor authentication turns on,
// and reads it again where a page shows it; the guards then read the new
// session. It returns once those reads are done.
//
// DESIGN: the member is the same, so the rest of their data stays, mail's
// collections included, which refresh only a list's newest page, on their own
// timer. A read the old session was refused, e.g. an administrator's before
// two-factor authentication, is read again as its page mounts (TanStack
// Query's retryOnMount). A new read that describes the session joins
// sessionKeys.
export async function resetSession(client: QueryClient): Promise<void> {
  await Promise.all(sessionKeys.map((queryKey) => client.resetQueries({ queryKey, exact: true })));
}
