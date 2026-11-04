// First: Zod's settings must apply to every schema the imports below create.
import "./zod-config";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRouter, type Router } from "@tanstack/react-router";
import { retryOptions } from "@/api/retry";
import { verifyEmailPath } from "@/features/email-verification/path";
import { resetPath } from "@/features/password-reset/path";
import { completePath } from "@/features/registration/path";
import { captureLinkSecret } from "@/lib/link-secret";
import { routeTree } from "./routeTree.gen";

// TanStack Start calls getRouter to create the app's router.
export function getRouter(): Router<typeof routeTree> {
  // Before the router reads the address or any request starts, so a failed
  // request cannot leave a link's secret in the address bar.
  captureLinkSecret([verifyEmailPath, completePath, resetPath]);

  const queryClient = new QueryClient({
    defaultOptions: retryOptions,
  });

  return createRouter({
    routeTree,
    // Gives every component the query cache, e.g. for useGetHealth().
    Wrap: ({ children }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    ),
  });
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
  }
}
