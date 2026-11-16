import { onlineManager, type QueryClient, type QueryExecuteOptions } from "@tanstack/react-query";
import type { ApiError } from "./transport";

// cachedRead returns what options read, for a route's guard or loader: the
// cached answer, unless a write marked it out of date, and then a fresh
// read, joining one already running. Offline when it starts, it returns
// the cached answer even when marked. It throws the read's failure.
//
// DESIGN: every guard and loader reads this one way (web/README.md
// "Routes"). A staleTime of Infinity, not "static": TanStack Query serves a
// "static" answer even once it is marked out of date, which would draw a
// page from what the member's own write just changed. Offline, though, a
// marked read would wait for the connection, so the page shows what it
// has under the offline banner instead of loading until then. A
// connection lost during the read still waits for its return (accepted,
// web/README.md "Routes").
export function cachedRead<T>(
  client: QueryClient,
  options: QueryExecuteOptions<T, ApiError>,
): Promise<T> {
  return client.query({
    ...options,
    staleTime: onlineManager.isOnline() ? Infinity : "static",
  });
}

// loadedFreshMs is how long a page counts what its loader just read as
// fresh, so it does not send the same read again as it opens; anything
// older it reads again then.
export const loadedFreshMs = 5_000;

// cachedLoader returns the loader of a page that cannot draw without the
// read options describe (web/README.md "Routes"): it reads through
// cachedRead before the page draws, and throws a failure, a 401 included,
// for the route's error view.
export function cachedLoader<T>(
  options: () => QueryExecuteOptions<T, ApiError>,
): (match: { context: { queryClient: QueryClient } }) => Promise<T> {
  return ({ context }) => cachedRead(context.queryClient, options());
}
