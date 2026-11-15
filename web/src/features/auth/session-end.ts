import { useCallback } from "react";
import { useQueryClient, type QueryExecuteOptions, type QueryClient } from "@tanstack/react-query";
import { useNavigate, useRouter } from "@tanstack/react-router";
import type { ApiError } from "@/api/transport";
import { isSessionEnded, sessionEnded } from "./guard";
import { clearAccountCache } from "./session-cache";

// loadedFreshMs is how long a page counts what its loader just read as
// fresh, so it does not send the same read again as it opens; anything
// older it reads again then.
export const loadedFreshMs = 5_000;

// SignedInLoader is a route loader for a signed-in page.
type SignedInLoader<T> = {
  handler: (match: {
    context: { queryClient: QueryClient };
    location: { href: string };
  }) => Promise<T>;
  staleReloadMode: "blocking";
};

// loadSignedIn returns what read resolves to, for a route's loader at
// href. A 401, a session that ended since the guard read it, e.g. signed
// out in another tab, clears the account cache and redirects to sign-in,
// which returns to href afterwards. Any other failure is thrown, for the
// route's error view.
export async function loadSignedIn<T>(
  client: QueryClient,
  href: string,
  read: () => Promise<T>,
): Promise<T> {
  try {
    return await read();
  } catch (err) {
    if (!isSessionEnded(err)) throw err;
    throw sessionEnded(client, href);
  }
}

// useSessionEnd returns a function that ends the page when err is a 401,
// a session that ended after the page loaded: it clears the account cache
// and opens sign-in, which returns to this page afterwards, without asking
// to leave unsaved typing, which can no longer be saved. It returns whether
// err ended the page; any other error is the caller's to show.
export function useSessionEnd(): (err: ApiError | null | undefined) => boolean {
  const client = useQueryClient();
  const navigate = useNavigate();
  const router = useRouter();

  return useCallback(
    (err) => {
      if (!isSessionEnded(err)) return false;
      clearAccountCache(client);
      void navigate({
        to: "/sign-in",
        search: { redirect: router.state.location.href },
        replace: true,
        ignoreBlocker: true,
      });
      return true;
    },
    [client, navigate, router],
  );
}

// signedInLoader returns the loader of a page that cannot draw without the
// read options describe (web/README.md "Routes"): it reads through
// loadSignedIn before the page draws, from the cache unless something
// marked the read out of date ("static" would ignore that mark), and a
// visit back waits for that reread instead of showing the page as it was.
export function signedInLoader<T>(
  options: () => QueryExecuteOptions<T, ApiError>,
): SignedInLoader<T> {
  return {
    handler: ({ context, location }) =>
      loadSignedIn(context.queryClient, location.href, () =>
        context.queryClient.query({ ...options(), staleTime: Infinity }),
      ),
    staleReloadMode: "blocking",
  };
}
