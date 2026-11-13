import { hashKey, type QueryClient } from "@tanstack/react-query";
import { getGetSetupStatusQueryKey } from "@/api/generated/inbok";
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

// The channel to this browser's other inbok tabs, opened on first use.
let channel: BroadcastChannel | undefined;

// otherTabs returns the channel that tells this browser's other inbok tabs
// that one member's data ended here; undefined where the browser lacks
// one, and other tabs then find out from their next 401, as before.
//
// NOTE: opened on first use, never as the module loads: the build renders
// the app's shell in Node, whose open BroadcastChannel would keep the
// build from ever exiting.
function otherTabs(): BroadcastChannel | undefined {
  if (!channel && typeof BroadcastChannel !== "undefined") {
    channel = new BroadcastChannel("inbok-account");
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

// clearAccountCache ends what the app holds for one member: it removes
// account data and mutation variables, keeping the public setup status,
// closes every toast, sends Back to mail to the Inbox, and runs what
// onAccountCacheClear registered; it tells the browser's other inbok tabs.
// Every sign-in, sign-out, and ended session calls it.
export function clearAccountCache(client: QueryClient): void {
  // oxlint-disable-next-line unicorn/require-post-message-target-origin -- a BroadcastChannel takes no origin; it reaches this site's tabs only.
  otherTabs()?.postMessage("ended");
  closeToasts();
  forgetMailHref();
  for (const clear of clearers) clear(client);
  const setupHash = hashKey(getGetSetupStatusQueryKey());
  client.removeQueries({ predicate: (query) => query.queryHash !== setupHash });
  client.getMutationCache().clear();
}
