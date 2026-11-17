import type { QueryClient } from "@tanstack/react-query";
import { createMemoryHistory } from "@tanstack/react-router";
import { AppShell } from "@/components/app-shell";
import { buildRouter } from "@/router";
import { routeTree } from "@/routeTree.gen";
import { rememberQueryClient } from "./query-client";
import { fakeServer } from "./server";

// NOTE: a test mounts the app in a <div> inside <body>, not on the
// document as the app's client entry does. The root route's shell would
// render <html> and <body> there, React would take the real <body> as its
// own, and its event dispatch would then loop forever on an event heard
// by a portal's container, e.g. any key press once a toast has shown
// (react/react#35480, #37065). The tests take the shell's behaviour
// without its document. Object.assign, since the generated root's options
// are typed as any route's, which lack shellComponent.
Object.assign(routeTree.options, { shellComponent: AppShell });

// appRouter returns the app's router (buildRouter in router.tsx), for a
// browser test, at address, after it has loaded the page there, its
// guards' redirects followed. server, which it installs, answers the
// app's requests; left out, a visitor's server does, which knows no
// session. prepare gets the router's query client before the first load,
// e.g. to seed the cache. The client is router.options.context.queryClient;
// the setup ends its data after the test (endQueryClients).
//
// It differs from the app's router in three ways: its history is in
// memory, the root renders AppShell without the document (the NOTE
// above), and a failed read is not tried again.
export async function appRouter(
  address: string,
  server = fakeServer(),
  prepare?: (queryClient: QueryClient) => void,
) {
  server.install();
  const router = buildRouter(createMemoryHistory({ initialEntries: [address] }));
  const queryClient = rememberQueryClient(router.options.context.queryClient);
  // The app tries a failed read again after 1 s, then 2 s, which a test
  // of a failure would wait out; api/retry.test.ts holds those rules.
  const options = queryClient.getDefaultOptions();
  queryClient.setDefaultOptions({ ...options, queries: { ...options.queries, retry: false } });
  prepare?.(queryClient);
  await router.load();
  return router;
}
