import type { QueryClient } from "@tanstack/react-query";
import { cachedRead } from "@/api/cached-read";
import { getGetCurrentSessionQueryOptions } from "@/api/generated/inbok";
import type { CurrentSession } from "@/api/generated/model";
import { redirect } from "@tanstack/react-router";
import { ApiError } from "@/api/transport";
import { clearAccountCache } from "./session-cache";

const unauthorized = 401;

// sessionEnded returns the redirect for a loader whose read found the
// session ended, a 401 after the guard ran, e.g. from another tab: the
// member's data is cleared and sign-in brings them back to href.
export function sessionEnded(client: QueryClient, href: string) {
  clearAccountCache(client);
  return redirect({ to: "/sign-in", search: { redirect: href }, replace: true });
}

// isSessionEnded reports whether err is a read's 401.
export function isSessionEnded(err: unknown): boolean {
  return err instanceof ApiError && err.status === unauthorized;
}

// readSession returns the signed-in member's session for a route guard,
// read through cachedRead: the cached one when a page has read it and no
// write marked it out of date, else a fresh read. Returns undefined when
// the browser has no session (a 401), and throws any other failure.
//
// DESIGN: a cached session can be older than its cookie, e.g. one another
// tab signed out. The guard lets the page draw; the page's own read then
// gets the 401 and opens sign-in, as before the guards existed.
export async function readSession(client: QueryClient): Promise<CurrentSession | undefined> {
  try {
    const response = await cachedRead(client, getGetCurrentSessionQueryOptions());
    return response.data;
  } catch (err) {
    if (err instanceof ApiError && err.status === unauthorized) return undefined;
    throw err;
  }
}
