// First: Zod's settings must apply to every schema the imports below create.
import "./zod-config";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRouter, type Router } from "@tanstack/react-router";
import { getGetCurrentSessionQueryKey } from "@/api/generated/inbok";
import { retryOptions } from "@/api/retry";
import { NotFound, RouteError, RoutePending, pendingAfterMs } from "@/components/route-states";
import { sessionFreshMs } from "@/features/auth/session";
import { ComposeStore } from "@/features/outgoing-messages/compose-store";
import { verifyEmailPath } from "@/features/email-verification/path";
import { resetPath } from "@/features/password-reset/path";
import { completePath } from "@/features/registration/path";
import { captureLinkSecret } from "@/lib/link-secret";
import { routeTree } from "./routeTree.gen";

// DESIGN.md "States": a quick load shows nothing; a slow one shows the
// pending view after pendingAfterMs and keeps it at least 500 ms, so it
// never flashes. The first load's delay is RoutePending's own.
const pendingAtLeastMs = 500;

// TanStack Start calls getRouter to create the app's router.
export function getRouter(): Router<typeof routeTree> {
  // Before the router reads the address or any request starts, so a failed
  // request cannot leave a link's secret in the address bar.
  captureLinkSecret([verifyEmailPath, completePath, resetPath]);

  const queryClient = new QueryClient({
    defaultOptions: retryOptions,
  });
  // A page that mounts just after its route guard read the session uses
  // that read instead of sending another.
  queryClient.setQueryDefaults(getGetCurrentSessionQueryKey(), { staleTime: sessionFreshMs });

  const router = createRouter({
    routeTree,
    context: { queryClient, compose: new ComposeStore() },
    defaultPendingMs: pendingAfterMs,
    // NOTE: on the first load TanStack shows a pending page's view at once,
    // without defaultPendingMs, and holds it defaultPendingMinMs from then
    // (router-core, offerPending), which would hold a spinner on every
    // visit. The first load is one loading period with the setup check's:
    // nothing holds it, and RoutePending hides itself for 300 ms then.
    defaultPendingMinMs: 0,
    defaultPendingComponent: RoutePending,
    // A visit back waits for the page's loader, so a read marked out of date
    // (cachedRead) is read again before the page draws; a read not marked
    // answers from the cache at once. The default, "background", would draw
    // the page from the old answer meanwhile.
    defaultStaleReloadMode: "blocking",
    // A visit after a preload, e.g. a conversation's on hover, runs the
    // loaders again instead of taking the preload's result for 30 s, so the
    // query cache decides what is fresh (TanStack Router's advice when an
    // external cache holds the data).
    defaultPreloadStaleTime: 0,
    defaultErrorComponent: RouteError,
    defaultNotFoundComponent: NotFound,
    // Gives every component the query cache, e.g. for useGetHealth().
    Wrap: ({ children }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    ),
  });

  // After the first load, a slow page keeps its view at least 500 ms.
  const unsubscribe = router.subscribe("onResolved", () => {
    unsubscribe();
    router.update({ ...router.options, defaultPendingMinMs: pendingAtLeastMs });
  });
  return router;
}

declare module "@tanstack/react-router" {
  // HistoryState is what one page hands the next without putting it in the
  // address, where it would reach history, copied links, and server logs.
  interface HistoryState {
    // The email of an account just created, for sign-in to fill in.
    registeredEmail?: string;
    // The email of an account whose password was just reset, for sign-in
    // to fill in.
    passwordResetEmail?: string;
    // A conversation opened from its list's row, so the entry before it
    // is that list.
    fromList?: boolean;
    // An entry added as the member started picking conversations, which
    // Back stops (features/mail/picking.ts): each its own number, so a
    // new one is always a new entry.
    picking?: number;
    // What the two-factor page just changed, for Security to report.
    twoFactorChange?: "on" | "off" | "canceled";
    // The entry the compose window added on a phone, by its message's id,
    // so Back closes the window (DESIGN.md "Compose and reply").
    composeWindow?: string;
  }
}
