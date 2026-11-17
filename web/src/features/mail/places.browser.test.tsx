import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  Outlet,
  RouterProvider,
  createMemoryHistory,
  createRootRouteWithContext,
  createRoute,
  createRouter,
  useParams,
} from "@tanstack/react-router";
import { beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { appRouter } from "@/test/app-router";
import { listedAccount, mailApi } from "@/test/mail-api";
import { fakeServer, memberSession } from "@/test/server";
import { Route as appRoot } from "@/routes/__root";
import { MailFrame } from "./frame";
import { forgetMailHref, mailHref, mailPage } from "./places";

// pageOf returns what mailPage makes of the page router shows.
function pageOf(router: Awaited<ReturnType<typeof appRouter>>) {
  return mailPage(router, router.state.matches, router.state.location.pathname);
}

// signedIn answers the frame's reads: the session, and an empty Outbox.
function signedIn(input: RequestInfo | URL): Response {
  const url = input instanceof Request ? input.url : input.toString();
  const body = url.includes("outgoing-messages")
    ? { data: { messages: [] } }
    : { data: memberSession };
  return Response.json(body);
}

// PlaceName is a place's page, named by the place.
function PlaceName() {
  const { place } = useParams({ strict: false });
  return <h1>{place}</h1>;
}

// frameApp returns a router at /mail/starred, inside the app's root, with
// the app's route ids: the mail frame with a place and its reading pane's
// index, and settings beside it.
function frameApp() {
  vi.stubGlobal(
    "fetch",
    vi.fn<typeof fetch>(async (input) => signedIn(input)),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const AppRoot = appRoot.options.component ?? Outlet;
  const root = createRootRouteWithContext<{ queryClient: QueryClient }>()({
    component: () => (
      <QueryClientProvider client={client}>
        <AppRoot />
      </QueryClientProvider>
    ),
  });
  const app = createRoute({ getParentRoute: () => root, id: "_app" });
  const frame = createRoute({
    getParentRoute: () => app,
    id: "_mail",
    component: () => (
      <MailFrame>
        <Outlet />
      </MailFrame>
    ),
  });
  const place = createRoute({
    getParentRoute: () => frame,
    path: "mail/$place",
    component: () => <Outlet />,
  });
  const nothingOpen = createRoute({
    getParentRoute: () => place,
    path: "/",
    component: PlaceName,
  });
  const settings = createRoute({
    getParentRoute: () => app,
    path: "settings",
    component: () => <h1>Settings</h1>,
  });
  return createRouter({
    routeTree: root.addChildren([
      app.addChildren([frame.addChildren([place.addChildren([nothingOpen])]), settings]),
    ]),
    history: createMemoryHistory({ initialEntries: ["/mail/starred"] }),
    context: { queryClient: client },
    defaultNotFoundComponent: () => <h1>Page not found</h1>,
  });
}

beforeEach(() => {
  forgetMailHref();
});

describe("mailPage", () => {
  it("is the place a loaded page in mail shows, with its search", async () => {
    const accounts = mailApi([], [listedAccount(2, "ana@example.com")]);
    const router = await appRouter("/mail/spam?account=2", accounts.server);
    expect(pageOf(router)).toEqual({
      to: "/mail/$place",
      params: { place: "spam" },
      search: { account: 2 },
    });
  });

  it("is a message in the Outbox", async () => {
    const id = "0193c8a0-0000-7000-8000-000000000000";
    const router = await appRouter(`/mail/outbox/${id}`, mailApi([], []).server);
    expect(pageOf(router)).toMatchObject({ to: "/mail/outbox/$id", params: { id } });
  });

  it("is nothing for a page outside mail, which the frame sees as it leaves", async () => {
    const router = await appRouter("/settings/security", fakeServer(memberSession));
    expect(pageOf(router)).toBeUndefined();
  });

  it("is nothing for a place that does not exist", async () => {
    const router = await appRouter("/mail/bogus", mailApi([], []).server);
    expect(pageOf(router)).toBeUndefined();
  });

  it("is nothing for an address deeper than any page in a place", async () => {
    const router = await appRouter("/mail/inbox/nope", mailApi([], []).server);
    expect(pageOf(router)).toBeUndefined();
  });

  it("is nothing for a page still loading", async () => {
    const router = await appRouter("/mail/spam", mailApi([], []).server);
    const loading = router.state.matches.map((match, index, all) =>
      index === all.length - 1 ? { ...match, status: "pending" as const } : match,
    );
    expect(mailPage(router, loading, router.state.location.pathname)).toBeUndefined();
  });
});

describe("MailFrame", () => {
  it("keeps the last loaded page in mail through a page not found and a visit to settings", async () => {
    await page.viewport(1440, 900);
    const router = frameApp();
    await render(<RouterProvider router={router} />);
    await expect.element(page.getByRole("heading", { name: "starred" })).toBeVisible();

    await router.navigate({ href: "/mail/spam" });
    await expect.element(page.getByRole("heading", { name: "spam" })).toBeVisible();
    await router.navigate({ href: "/mail/inbox/nope" });
    await expect.element(page.getByRole("heading", { name: "Page not found" })).toBeVisible();
    await router.navigate({ href: "/settings" });
    await expect.element(page.getByRole("heading", { name: "Settings" })).toBeVisible();

    expect(mailHref()).toEqual({ to: "/mail/$place", params: { place: "spam" }, search: {} });
  });
});
