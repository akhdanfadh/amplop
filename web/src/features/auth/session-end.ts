import { useEffect, useSyncExternalStore } from "react";
import type { QueryClient } from "@tanstack/react-query";
import type { AnyRouter } from "@tanstack/react-router";
import { onUnauthorized } from "@/api/transport";
import { clearAccountCache, onAccountCacheClear } from "./session-cache";

// The routes whose pages need a session: the signed-in app, and the
// administrator's two-factor setup, whose session may do nothing else.
const signedInRoutes = new Set(["/_app", "/_auth/two-factor-setup"]);

// Whether a session ended while the page held work (useKeptSignedOut).
let ended = false;
// The work the page holds now, one entry per useHeldWork that is active.
const holds = new Set<symbol>();
// What useKeptSignedOut's components listen with.
const listeners = new Set<() => void>();
// endNow applies the rule with watchSessionEnd's client and router; unset
// until it runs.
let endNow: (() => void) | undefined;
// Stops the router watchSessionEnd watches now.
let stopWatching: (() => void) | undefined;

// A new member starts with no ended session.
onAccountCacheClear(() => setEnded(false));

// setEnded sets ended to value, telling useKeptSignedOut's components when
// it changes.
function setEnded(value: boolean): void {
  if (ended === value) return;
  ended = value;
  for (const listener of listeners) listener();
}

// watchSessionEnd applies amplop's one rule for a session that ends while a
// signed-in page is open (web/README.md "Routes"), whenever a request gets
// a 401 there: with nothing held (useHeldWork), it clears the member's
// data and opens sign-in, which says the session ended and returns to the
// page afterwards; with work held, the page stays and useKeptSignedOut
// turns true, for the banner that says to copy it. Once the held work is
// gone, e.g. discarded, it opens sign-in the same way. It returns the
// function that stops it.
//
// DESIGN: every request passes through transport, so this one listener
// sees every 401, from reads, writes, mail's actions, and loaders alike,
// and no page handles one itself. A 401 on a signed-out page, e.g. a wrong
// password, is that page's own answer. Only the latest call's router is
// watched: the app has one, and a test makes one per page it renders.
export function watchSessionEnd(client: QueryClient, router: AnyRouter): () => void {
  stopWatching?.();
  endNow = () => end(client, router);
  const stop = onUnauthorized(() => {
    // Only a signed-in page on screen can lose its session: before the
    // first page draws, e.g. a visitor's first load, a 401 is the guard's
    // own answer, and the visitor never had a session to lose.
    const shown = router.state.resolvedLocation;
    if (shown && needsSession(router, shown.pathname)) end(client, router);
  });
  const stopThis = () => {
    stop();
    if (stopWatching !== stopThis) return;
    stopWatching = undefined;
    endNow = undefined;
  };
  stopWatching = stopThis;
  return stopThis;
}

// needsSession reports whether the page at pathname needs a session.
function needsSession(router: AnyRouter, pathname: string): boolean {
  return router.matchRoutes(pathname).some((match) => signedInRoutes.has(match.routeId));
}

// end applies the rule watchSessionEnd describes.
function end(client: QueryClient, router: AnyRouter): void {
  if (holds.size > 0) {
    setEnded(true);
    return;
  }
  if (!needsSession(router, router.latestLocation.pathname)) {
    // Already on the way to sign-in, e.g. by the banner's link: the data
    // the held work needed goes now.
    if (ended) clearAccountCache(client);
    return;
  }
  clearAccountCache(client);
  void router.navigate({
    to: "/sign-in",
    search: { redirect: router.latestLocation.href },
    state: { sessionEnded: true },
    replace: true,
    // Nothing held: no leave warning has anything to ask about.
    ignoreBlocker: true,
  });
}

// useHeldWork tells the session-end rule, while active, that the page holds
// work a sign-in would lose and the member may still copy, e.g. a typed
// reply, so a session that ends keeps the page instead of opening sign-in.
export function useHeldWork(active: boolean): void {
  useEffect(() => {
    if (!active) return undefined;
    const hold = Symbol("held work");
    holds.add(hold);
    return () => {
      holds.delete(hold);
      if (!ended) return;
      // After the render that let it go, so held work that only moved,
      // e.g. a component drawn again, keeps the page.
      queueMicrotask(() => {
        if (ended && holds.size === 0) endNow?.();
      });
    };
  }, [active]);
}

// keptSignedOut reports whether the session ended while the page held
// work, which keeps the page, its data, and the session it read, e.g. for
// a guard that must not take that session as current.
export function keptSignedOut(): boolean {
  return ended;
}

// subscribe adds listener to what setEnded tells, for useSyncExternalStore,
// and returns the function that removes it.
function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

// useKeptSignedOut returns whether the session ended while the page held
// work (keptSignedOut), so the page stays signed out until the member
// signs in.
export function useKeptSignedOut(): boolean {
  return useSyncExternalStore(
    subscribe,
    () => ended,
    () => false,
  );
}
