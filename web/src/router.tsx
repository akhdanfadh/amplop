import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRouter } from "@tanstack/react-router";
import { ApiError } from "@/api/transport";
import { routeTree } from "./routeTree.gen";

// Statuses that may pass on their own: Go restarting behind a proxy (502),
// overloaded (503), or too slow (504).
const transientStatuses = new Set([502, 503, 504]);
const maxRetries = 2;

// TanStack Start calls getRouter to create the app's router.
export function getRouter() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: shouldRetry } },
  });

  return createRouter({
    routeTree,
    // Gives every component the query cache, e.g. for useGetHealth().
    Wrap: ({ children }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    ),
  });
}

// Query retries every failure 3 times by default, so a 404 or a contract
// mismatch would show only after about 7 seconds. Mutations (writes) never
// retry: Query's default, since a write may have landed.
function shouldRetry(failures: number, err: Error) {
  if (failures >= maxRetries || !(err instanceof ApiError)) return false;

  const { status } = err.problem;
  return status !== undefined && transientStatuses.has(status);
}
