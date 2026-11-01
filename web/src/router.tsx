// First: Zod's settings must apply to every schema the imports below create.
import "./zod-config";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRouter, type Router } from "@tanstack/react-router";
import { retryOptions } from "@/api/retry";
import { captureLinkSecret } from "@/features/email-verification/link-secret";
import { routeTree } from "./routeTree.gen";

// TanStack Start calls getRouter to create the app's router.
export function getRouter(): Router<typeof routeTree> {
  // Before the router reads the address or any request starts, so a failed
  // request cannot leave a verification secret in the address bar.
  captureLinkSecret();

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
