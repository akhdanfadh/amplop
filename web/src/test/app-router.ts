import { createElement } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { vi } from "vite-plus/test";
import { AppShell } from "@/components/app-shell";
import { ComposeStore } from "@/features/outgoing-messages/compose-store";
import { routeTree } from "@/routeTree.gen";

// NOTE: a test mounts the app in a <div> inside <body>, not on the
// document as the app's client entry does. The root route's shell would
// render <html> and <body> there, React would take the real <body> as its
// own, and its event dispatch would then loop forever on an event heard
// by a portal's container, e.g. any key press once a toast has shown
// (react/react#35480, #37065). The tests take the shell's behaviour
// without its document. Object.assign, since the generated root's options
// are typed as any route's, which lack shellComponent.
Object.assign(routeTree.options, { shellComponent: AppShell });

const ok = 200;
const unauthorized = 401;

// A signed-in member's session, as the session read answers it.
export const memberSession = {
  email: "ana@example.com",
  emailVerified: true,
  role: "member",
  totpRequired: false,
};

// appRouter returns the app's router, for a browser test, at address, on a memory history, after
// it has loaded the page there, its guards' redirects followed. answer
// answers the requests it knows, e.g. a mail page's reads; every other
// read answers with session, or a 401 without one. answer also receives
// the request's init, e.g. its method. Call
// vi.unstubAllGlobals() afterwards.
export async function appRouter(
  address: string,
  session?: typeof memberSession,
  answer?: (url: string, init?: RequestInit) => Promise<Response> | undefined,
) {
  vi.stubGlobal(
    "fetch",
    vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>(
      async (input, init) =>
        answer?.(
          typeof input === "string" ? input : input instanceof URL ? input.href : input.url,
          init,
        ) ??
        (session
          ? Response.json({ data: session }, { status: ok })
          : Response.json(
              {
                type: "/api/v0/problems/unauthorized",
                status: unauthorized,
                title: "",
                detail: "",
              },
              { status: unauthorized, headers: { "Content-Type": "application/problem+json" } },
            )),
    ),
  );
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [address] }),
    context: { queryClient, compose: new ComposeStore() },
    // As the app's router does (router.tsx): a visit, a preloaded one too,
    // waits for a read marked out of date.
    defaultStaleReloadMode: "blocking",
    defaultPreloadStaleTime: 0,
    // As the app's router does (router.tsx), so a rendered page reaches the
    // query client.
    Wrap: ({ children }) => createElement(QueryClientProvider, { client: queryClient }, children),
  });
  await router.load();
  return router;
}
