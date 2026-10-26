import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRouter, type Router } from "@tanstack/react-router";
import * as z from "zod";
import { retryOptions } from "@/api/retry";
import { validationMessage } from "@/api/validation";
import { routeTree } from "./routeTree.gen";

// TanStack Start calls getRouter to create the app's router.
export function getRouter(): Router<typeof routeTree> {
  // Every Zod check in the app, e.g. a form's, reports in the app's words.
  z.config({ customError: validationMessage });

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
