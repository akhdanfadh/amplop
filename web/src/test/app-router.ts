import { QueryClient } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { vi } from "vite-plus/test";
import { routeTree } from "@/routeTree.gen";

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
// read answers with session, or a 401 without one; call
// vi.unstubAllGlobals() afterwards.
export async function appRouter(
  address: string,
  session?: typeof memberSession,
  answer?: (url: string) => Promise<Response> | undefined,
) {
  vi.stubGlobal(
    "fetch",
    vi.fn<(input: RequestInfo | URL) => Promise<Response>>(
      async (input) =>
        answer?.(
          typeof input === "string" ? input : input instanceof URL ? input.href : input.url,
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
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [address] }),
    context: { queryClient: new QueryClient({ defaultOptions: { queries: { retry: false } } }) },
  });
  await router.load();
  return router;
}
