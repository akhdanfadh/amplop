import { useGetCurrentSession } from "@/api/generated/amplop";
import type { CurrentSession } from "@/api/generated/model";

// A session read this recent counts as fresh: a page that mounts right
// after its route guard read the session uses that read instead of
// sending another, which would show "Checking your session" for nothing.
export const sessionFreshMs = 5_000;

// useSignedInSession returns the signed-in member's session as the cache
// holds it, or undefined, for parts of the frame that only show who is
// signed in, e.g. the member menu. It never reads the server itself: the
// guard and the pages keep the session current, and a read each time the
// drawer opens would make the page re-check the session.
export function useSignedInSession(): CurrentSession | undefined {
  return useGetCurrentSession({
    query: { refetchOnMount: false, refetchOnWindowFocus: false, refetchOnReconnect: false },
  }).data?.data;
}
