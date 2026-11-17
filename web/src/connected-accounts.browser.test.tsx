import "@/zod-config";
import { describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  Outlet,
  RouterProvider,
  createMemoryHistory,
  createRootRouteWithContext,
  createRoute,
  createRouter,
} from "@tanstack/react-router";
import type { ListedAccount } from "@/api/generated/model";
import { RouteError } from "@/components/route-states";
import { SyncStopped } from "@/features/connected-accounts/sync-stopped";
import { ComposeStore } from "@/features/outgoing-messages/compose-store";
import { Route as App } from "@/routes/_app";
import {
  Route as ConnectedAccounts,
  connectedAccountsLoader,
} from "@/routes/_app/settings/connected-accounts";
import { listedAccount } from "@/test/mail-api";
import { watchSessionEnd } from "@/features/auth/session-end";

const ok = 200;
const accepted = 202;
const unauthorized = 401;
const notFound = 404;
const tooManyRequests = 429;
const fault = 500;
const listPath = "/api/v0/connected-accounts";

// Server is what the test API holds and how it answers: accounts as the
// list shows them, offered whether Gmail is, role the member's; throttled
// refuses Check now as requested moments ago; gone answers a disconnect
// as if the account had been removed already; hold holds the list's reads
// until it settles, holdSync a Check now's answer, after the server acted,
// and holdProviders the providers' reads; readsFailFrom answers the list's
// reads from the nth on with a fault, providersFailFrom the providers';
// listStatus and providersStatus answer those reads with that status;
// holdDelete holds a disconnect's answer, after the server acted;
// deleteFails answers the next disconnect with a fault, loseDelete drops
// its answer after the server acted; ended answers every request with a
// 401; sent lists every request.
type Server = {
  accounts: ListedAccount[];
  offered?: boolean;
  role?: "member" | "admin";
  syncStopped?: boolean;
  throttled?: boolean;
  gone?: boolean;
  hold?: Promise<void>;
  holdSync?: Promise<void>;
  holdProviders?: Promise<void>;
  readsFailFrom?: number;
  providersFailFrom?: number;
  listStatus?: number;
  providersStatus?: number;
  deleteFails?: boolean;
  holdDelete?: Promise<void>;
  loseDelete?: boolean;
  ended?: boolean;
  sent: string[];
};

function json(data: unknown, status = ok, headers: Record<string, string> = {}): Response {
  return Response.json(data, {
    status,
    headers: {
      "Content-Type": status < 300 ? "application/json" : "application/problem+json",
      ...headers,
    },
  });
}

function problem(status: number, type: string, detail: string, headers = {}): Response {
  return json({ type: `/api/v0/problems/${type}`, status, title: "", detail }, status, headers);
}

// answer answers one request as the API would, changing server.
function answer(server: Server, method: string, path: string): Response {
  if (server.ended) return problem(unauthorized, "unauthorized", "");
  if (path === "/api/v0/sessions/current")
    return json({
      data: {
        email: "ana@example.com",
        emailVerified: true,
        role: server.role ?? "member",
        totpRequired: false,
      },
    });
  if (path === listPath && server.listStatus)
    return problem(server.listStatus, "refused", "The list couldn't be read.");
  if (path === `${listPath}/providers` && server.providersStatus)
    return problem(server.providersStatus, "refused", "The providers couldn't be read.");
  if (path === `${listPath}/providers`) {
    const reads = server.sent.filter((sent) => sent === `GET ${listPath}/providers`).length;
    if (server.providersFailFrom !== undefined && reads >= server.providersFailFrom)
      return problem(fault, "internal-error", "Something went wrong on our side.");
  }
  if (path === listPath) {
    const reads = server.sent.filter((sent) => sent === `GET ${listPath}`).length;
    if (server.readsFailFrom !== undefined && reads >= server.readsFailFrom)
      return problem(fault, "internal-error", "Something went wrong on our side.");
    return json({ data: { accounts: server.accounts, syncStopped: server.syncStopped ?? false } });
  }
  if (path === `${listPath}/providers`)
    return json({ data: { offered: server.offered === false ? [] : ["gmail"] } });
  const sync = /^\/api\/v0\/connected-accounts\/(\d+)\/sync$/.exec(path);
  if (sync) {
    if (server.throttled)
      return problem(tooManyRequests, "sync-throttled", "Requested moments ago.", {
        "Retry-After": "12",
      });
    const id = Number(sync[1]);
    const before = server.accounts.find((a) => a.id === id);
    // The check lands at once, a minute after the last: the next read
    // shows it.
    const checks = server.sent.filter((sent) => sent.endsWith("/sync")).length;
    const checkedAt = new Date(Date.UTC(2026, 10, 16, 9, checks)).toISOString();
    server.accounts = server.accounts.map((a) =>
      a.id === id ? { ...a, sync: { ...a.sync, lastSyncedAt: checkedAt } } : a,
    );
    return json({ data: before?.sync }, accepted);
  }
  const one = /^\/api\/v0\/connected-accounts\/(\d+)$/.exec(path);
  if (one && method === "DELETE") {
    if (server.deleteFails) {
      server.deleteFails = false;
      return problem(fault, "internal-error", "Something went wrong on our side.");
    }
    const id = Number(one[1]);
    const removed = server.accounts.find((a) => a.id === id);
    server.accounts = server.accounts.filter((a) => a.id !== id);
    if (server.gone || !removed)
      return problem(notFound, "connected-account-not-found", "The account does not exist.");
    return json({ data: { account: removed } });
  }
  return problem(notFound, "not-found", "");
}

// open renders Connected accounts at path in a router of its own, under
// the real signed-in guard, its requests answered by server, and the sync
// banner the frame shows above it, as on every signed-in page, e.g. the
// bare /settings/other. It waits for the page unless loaded is false, and
// returns the router and its query client.
async function open(server: Server, path = "/settings/connected-accounts", loaded = true) {
  vi.stubGlobal(
    "fetch",
    vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>(
      async (input, init) => {
        const method = (init?.method ?? "GET").toUpperCase();
        const address = input instanceof Request ? input.url : input.toString();
        const requestPath = new URL(address, location.href).pathname;
        if (server.hold && method === "GET" && requestPath === listPath) await server.hold;
        if (server.holdProviders && requestPath === `${listPath}/providers`)
          await server.holdProviders;
        const answered = answer(server, method, requestPath);
        server.sent.push(`${method} ${requestPath}`);
        if (server.holdSync && requestPath.endsWith("/sync")) await server.holdSync;
        if (server.holdDelete && method === "DELETE") await server.holdDelete;
        if (server.loseDelete && method === "DELETE") {
          server.loseDelete = false;
          throw new TypeError("Failed to fetch");
        }
        return answered;
      },
    ),
  );
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const root = createRootRouteWithContext<{ queryClient: QueryClient; compose: ComposeStore }>()({
    component: () => (
      <>
        <SyncStopped />
        <Outlet />
      </>
    ),
  });
  const app = createRoute({
    getParentRoute: () => root,
    id: "_app",
    component: Outlet,
    beforeLoad: App.options.beforeLoad,
  });
  const settings = createRoute({ getParentRoute: () => app, path: "settings", component: Outlet });
  const router = createRouter({
    routeTree: root.addChildren([
      app.addChildren([
        settings.addChildren([
          createRoute({
            getParentRoute: () => settings,
            path: "connected-accounts",
            validateSearch: ConnectedAccounts.options.validateSearch,
            loader: connectedAccountsLoader,
            component: ConnectedAccounts.options.component,
          }),
          createRoute({
            getParentRoute: () => settings,
            path: "other",
            component: () => "Another settings page",
          }),
        ]),
      ]),
      createRoute({ getParentRoute: () => root, path: "/sign-in", component: () => "Sign in" }),
    ]),
    history: createMemoryHistory({ initialEntries: [path] }),
    defaultErrorComponent: RouteError,
    defaultStaleReloadMode: "blocking",
    defaultPreloadStaleTime: 0,
    context: { queryClient, compose: new ComposeStore() },
  });
  // As the app's router does (router.tsx).
  watchSessionEnd(queryClient, router);
  await render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  const opened = { router, client: queryClient };
  if (!loaded) return opened;
  await expect.element(page.getByRole("heading", { name: "Connected accounts" })).toBeVisible();
  return opened;
}

// held returns a promise and the function that settles it.
function held(): { until: Promise<void>; release: () => void } {
  const settle: { done?: () => void } = {};
  const until = new Promise<void>((done) => {
    settle.done = done;
  });
  return { until, release: () => settle.done?.() };
}

const work = { ...listedAccount(1, "ana@work.example"), name: "Work" };
const home = listedAccount(2, "ana@home.example");
const refused: ListedAccount = {
  ...listedAccount(3, "ana@old.example"),
  status: "needs-reconnect",
  needsReconnectSince: "2026-11-10T09:00:00Z",
  allowedActions: ["reconnect", "rename", "disconnect"],
};

// fakeClock makes the timers the page and its reads use fake, while still
// running on real time, so a test can jump ahead a minute.
function fakeClock(): void {
  vi.useFakeTimers({
    shouldAdvanceTime: true,
    toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval"],
  });
}

describe("Connected accounts", () => {
  it("offers each account the actions the server allows it", async () => {
    await open({ accounts: [work, refused], sent: [] });

    await expect
      .element(page.getByRole("button", { name: "Check now ana@work.example" }))
      .toBeVisible();
    await expect
      .element(page.getByRole("button", { name: "Reconnect ana@old.example" }))
      .toBeVisible();
    expect(page.getByRole("button", { name: "Check now ana@old.example" }).elements()).toHaveLength(
      0,
    );
    await expect.element(page.getByText("Needs reconnecting")).toBeVisible();
    await expect.element(page.getByRole("button", { name: "Connect Gmail" })).toBeVisible();
  });

  it("says why nothing can be connected while Gmail isn't offered", async () => {
    const unoffered: ListedAccount = { ...work, allowedActions: ["rename", "disconnect"] };
    await open({ accounts: [unoffered], offered: false, sent: [] });

    await expect
      .element(page.getByText(/^Your administrator hasn't set up Gmail on this inbok/))
      .toBeVisible();
    expect(page.getByRole("button", { name: "Connect Gmail" }).elements()).toHaveLength(0);
    expect(page.getByRole("button", { name: /^Check now/ }).elements()).toHaveLength(0);
  });

  it("tells an administrator where Gmail is set up when nothing can be connected", async () => {
    await open({ accounts: [], offered: false, role: "admin", sent: [] });

    await expect
      .element(page.getByRole("heading", { name: "Gmail can't be connected yet" }))
      .toBeVisible();
    await expect
      .element(page.getByText(/^Add a Google client to this inbok, as the self-hosting guide/))
      .toBeVisible();
    expect(page.getByRole("button", { name: "Connect Gmail" }).elements()).toHaveLength(0);
  });

  it("says how to start when no account is connected", async () => {
    await open({ accounts: [], sent: [] });

    await expect
      .element(page.getByRole("heading", { name: "Connect your first account" }))
      .toBeVisible();
    await expect.element(page.getByRole("button", { name: "Connect Gmail" })).toBeVisible();
  });

  it("keeps Check now turning until the list shows the check", async () => {
    let release: (() => void) | undefined;
    const server: Server = { accounts: [home], sent: [] };
    const { client } = await open(server);
    server.hold = new Promise((resolve) => {
      release = resolve;
    });

    await userEvent.click(page.getByRole("button", { name: "Check now ana@home.example" }));

    await expect
      .element(page.getByRole("button", { name: "Checking... ana@home.example" }))
      .toBeVisible();
    release?.();
    server.hold = undefined;
    await expect
      .element(page.getByRole("button", { name: "Check now ana@home.example" }))
      .toBeVisible();
    // A screen reader hears the outcome, not only the button's name.
    const said = page.getByRole("status").filter({ hasText: "ana@home.example: Up to date" });
    await expect.element(said).toBeInTheDocument();
    // Once: a full sync starting after it, whose count moves with every
    // read, isn't said again.
    server.accounts = server.accounts.map((a) => ({
      ...a,
      sync: { ...a.sync, status: "running" as const, messagesSoFar: 120 },
    }));
    await client.refetchQueries();
    await expect.element(page.getByText(/120 messages so far/)).toBeInTheDocument();
    await expect.element(said).toBeInTheDocument();
    expect(said.element().textContent).not.toMatch(/Copying/);
    expect(server.sent).toContain("POST /api/v0/connected-accounts/2/sync");
  });

  it("says each check's own outcome when two follow each other", async () => {
    await open({ accounts: [home], sent: [] });
    const said = page.getByRole("status").filter({ hasText: "ana@home.example:" });
    const check = page.getByRole("button", { name: "Check now ana@home.example" });

    await userEvent.click(check);
    await expect.element(said).toBeInTheDocument();
    const first = said.element().textContent;
    // Within the first check's 30 s.
    await userEvent.click(check);

    await expect.poll(() => said.element().textContent).not.toBe(first);
  });

  it("says a check started moments ago in words when it is refused for that", async () => {
    await open({ accounts: [home], throttled: true, sent: [] });

    await userEvent.click(page.getByRole("button", { name: "Check now ana@home.example" }));

    await expect
      .element(page.getByRole("status").filter({ hasText: "checked moments ago" }))
      .toHaveTextContent("ana@home.example was checked moments ago. Check again in a few seconds.");
  });

  it("renames in place, then gives focus back to the pencil", async () => {
    await open({ accounts: [work], sent: [] });
    const pencil = page.getByRole("button", { name: "Rename ana@work.example" });

    await userEvent.click(pencil);
    await expect.element(page.getByRole("textbox", { name: /^Name/ })).toHaveFocus();
    // The row still says which account it is, by the address a rename
    // never changes.
    await expect
      .element(page.getByRole("listitem").getByText("ana@work.example").first())
      .toBeInTheDocument();
    expect(page.getByRole("listitem").getByText("Work", { exact: true }).elements()).toHaveLength(
      0,
    );
    await userEvent.keyboard("{Escape}");

    await expect.element(pencil).toHaveFocus();
    expect(page.getByRole("textbox").elements()).toHaveLength(0);
  });

  it("disconnects after asking, then moves focus to the next account", async () => {
    const server: Server = { accounts: [work, home], sent: [] };
    await open(server);

    await userEvent.click(page.getByRole("button", { name: "Disconnect ana@work.example" }));
    const dialog = page.getByRole("alertdialog");
    await expect.element(dialog).toHaveAccessibleName("Disconnect Work?");
    await expect.element(dialog.getByRole("button", { name: "Cancel" })).toHaveFocus();
    // A keyboard press counts at once, unlike a click within a
    // double-click of the dialog opening.
    await userEvent.keyboard("{Tab}{Enter}");

    await expect.poll(() => server.sent).toContain("DELETE /api/v0/connected-accounts/1");
    await expect.element(page.getByText("Work is disconnected.")).toBeVisible();
    await expect
      .element(page.getByRole("button", { name: "Rename ana@home.example" }))
      .toHaveFocus();
    expect(server.accounts.map((a) => a.id)).toEqual([2]);
  });

  it("says an account was already disconnected when the server no longer has it", async () => {
    await open({ accounts: [work], gone: true, sent: [] });

    await userEvent.click(page.getByRole("button", { name: "Disconnect ana@work.example" }));
    await expect.element(page.getByRole("alertdialog")).toBeVisible();
    await userEvent.keyboard("{Tab}{Enter}");

    await expect.element(page.getByText("Work was already disconnected.")).toBeVisible();
    await expect.element(page.getByRole("alertdialog")).not.toBeInTheDocument();
    await expect.element(page.getByRole("button", { name: "Connect Gmail" })).toHaveFocus();
  });

  it("reports Google's return as a problem to fix, and drops it from the address", async () => {
    const { router } = await open(
      { accounts: [], sent: [] },
      "/settings/connected-accounts?outcome=gmail-not-granted",
    );

    await expect
      .element(page.getByRole("alert").getByText(/access to Gmail was left unticked/))
      .toBeVisible();
    expect(router.state.location.search).toEqual({});
  });

  it("keeps the list and says so when a refresh fails", async () => {
    const server: Server = { accounts: [home], readsFailFrom: 1, sent: [] };
    const { client } = await open(server);

    void client.refetchQueries();

    await expect
      .element(page.getByRole("alert").getByText(/^Couldn't refresh this page/))
      .toBeVisible();
    await expect
      .element(page.getByRole("button", { name: "Check now ana@home.example" }))
      .toBeVisible();
  });

  it("opens sign-in when the session has ended", async () => {
    const server: Server = { accounts: [home], sent: [] };
    await open(server);
    server.ended = true;

    await userEvent.click(page.getByRole("button", { name: "Check now ana@home.example" }));

    await expect.element(page.getByText("Sign in")).toBeVisible();
  });

  it("returns here after sign-in when a refresh finds the session ended", async () => {
    const server: Server = { accounts: [home], sent: [] };
    const { router, client } = await open(server);
    server.ended = true;

    // The page and the frame's banner both read the list, so both see the
    // 401; sign-in must still return to the page.
    void client.refetchQueries();

    await expect.element(page.getByText("Sign in")).toBeVisible();
    expect(router.state.location.pathname).toBe("/sign-in");
    expect(router.state.location.search).toEqual({ redirect: "/settings/connected-accounts" });
  });

  it.each([
    ["the list faults, then the session ends", fault, unauthorized],
    ["the session ends, then the list faults", unauthorized, fault],
  ])("opens sign-in when one read ends the session: %s", async (_, list, providers) => {
    const answered = held();
    const server: Server = {
      accounts: [home],
      listStatus: list,
      providersStatus: providers,
      holdProviders: answered.until,
      sent: [],
    };
    const opening = open(server, "/settings/connected-accounts", false);
    // The providers answer once the list has.
    await expect.poll(() => server.sent).toContain(`GET ${listPath}`);
    answered.release();
    await opening;

    await expect.element(page.getByText("Sign in")).toBeVisible();
  });

  it("keeps Check now's newer list when the request's own answer comes later", async () => {
    const answered = held();
    const server: Server = { accounts: [home], holdSync: answered.until, sent: [] };
    const { client } = await open(server);
    await expect.element(page.getByText(/13 Nov 2026/)).toBeVisible();

    await userEvent.click(page.getByRole("button", { name: "Check now ana@home.example" }));
    await expect.poll(() => server.sent).toContain(`POST ${listPath}/2/sync`);
    // A read after the check lands first; later reads fail, so nothing
    // would put the newer time back.
    await client.refetchQueries();
    await expect.element(page.getByText(/13 Nov 2026/)).not.toBeInTheDocument();
    server.readsFailFrom = 0;
    answered.release();

    await expect
      .element(page.getByRole("button", { name: "Check now ana@home.example" }))
      .toBeVisible();
    expect(page.getByText(/13 Nov 2026/).elements()).toHaveLength(0);
  });

  it("stops waiting for a check after 30 seconds", async () => {
    fakeClock();
    const reads = held();
    const server: Server = { accounts: [home], sent: [] };
    await open(server);
    // The list never shows the check.
    server.hold = reads.until;

    await userEvent.click(page.getByRole("button", { name: "Check now ana@home.example" }));
    await expect
      .element(page.getByRole("button", { name: "Checking... ana@home.example" }))
      .toBeVisible();
    await vi.advanceTimersByTimeAsync(30_000);

    await expect
      .element(page.getByRole("button", { name: "Check now ana@home.example" }))
      .toBeVisible();
    // Nothing landed, so nothing is said.
    expect(
      page.getByRole("status").filter({ hasText: "ana@home.example:" }).elements(),
    ).toHaveLength(0);
    reads.release();
  });

  it("says when the providers can't be refreshed, and reads them again", async () => {
    const server: Server = { accounts: [home], providersFailFrom: 1, sent: [] };
    const { client } = await open(server);

    void client.refetchQueries();
    const failure = page.getByRole("alert");
    await expect.element(failure.getByText(/^Couldn't refresh this page/)).toBeVisible();
    server.providersFailFrom = undefined;
    await userEvent.click(failure.getByRole("button", { name: "Try again" }));

    await expect.element(page.getByRole("alert")).not.toBeInTheDocument();
    expect(server.sent.filter((sent) => sent === `GET ${listPath}/providers`)).toHaveLength(3);
  });

  it("shows a failed disconnect in its dialog, where pressing again retries", async () => {
    await open({ accounts: [work], deleteFails: true, sent: [] });

    await userEvent.click(page.getByRole("button", { name: "Disconnect ana@work.example" }));
    const dialog = page.getByRole("alertdialog");
    await expect.element(dialog).toBeVisible();
    await userEvent.keyboard("{Tab}{Enter}");
    await expect.element(dialog.getByRole("alert")).toHaveTextContent(
      // A fault may have come after the account went, so it is unknown.
      "Something went wrong on our side. Couldn't confirm whether Work is disconnected. Check the list before trying again.",
    );
    await userEvent.keyboard("{Enter}");

    await expect.element(page.getByText("Work is disconnected.")).toBeVisible();
    await expect.element(dialog).not.toBeInTheDocument();
  });

  it("keeps a long account's name inside the disconnect dialog", async () => {
    const long = listedAccount(
      4,
      "bartholomew.fitzgerald-wisniewska@northwind-industries-holdings.example.com",
    );
    // Wide enough for the dialog's buttons to sit side by side, where a
    // long label would push past its edge.
    await page.viewport(1024, 768);
    await open({ accounts: [long], sent: [] });

    await userEvent.click(page.getByRole("button", { name: /^Disconnect bartholomew/ }));
    const dialog = page.getByRole("alertdialog");
    await expect.element(dialog).toBeVisible();

    const box = dialog.element().getBoundingClientRect();
    for (const button of dialog.getByRole("button").elements()) {
      const edge = button.getBoundingClientRect();
      expect(edge.left).toBeGreaterThanOrEqual(box.left);
      expect(edge.right).toBeLessThanOrEqual(box.right);
    }
    // Heard whole, the question mark included.
    await expect.element(dialog).toHaveAccessibleName(`Disconnect ${long.email}?`);
  });

  it("holds the dialog while a disconnect is under way", async () => {
    const answered = held();
    await open({ accounts: [work], holdDelete: answered.until, sent: [] });

    await userEvent.click(page.getByRole("button", { name: "Disconnect ana@work.example" }));
    const dialog = page.getByRole("alertdialog");
    await expect.element(dialog).toBeVisible();
    await userEvent.keyboard("{Tab}{Enter}");

    await expect.element(dialog.getByRole("button", { name: "Cancel" })).toBeDisabled();
    await expect.element(dialog.getByText("Disconnecting...")).toBeVisible();
    await userEvent.keyboard("{Escape}");
    await expect.element(dialog).toBeVisible();
    answered.release();
    await expect.element(dialog).not.toBeInTheDocument();
  });

  it("reads the whole list again after a disconnect", async () => {
    const reads = held();
    const server: Server = { accounts: [work, home], sent: [] };
    await open(server);
    // The read after the disconnect waits; meanwhile another tab names the
    // other account.
    server.hold = reads.until;

    await userEvent.click(page.getByRole("button", { name: "Disconnect ana@work.example" }));
    await expect.element(page.getByRole("alertdialog")).toBeVisible();
    await userEvent.keyboard("{Tab}{Enter}");
    await expect.element(page.getByText("Work is disconnected.")).toBeVisible();
    server.accounts = server.accounts.map((a) => (a.id === home.id ? { ...a, name: "Home" } : a));
    server.hold = undefined;
    reads.release();

    await expect
      .element(page.getByRole("listitem").getByText("Home", { exact: true }))
      .toBeVisible();
  });

  it("asks for a look at the list when a disconnect's answer is lost", async () => {
    await open({ accounts: [work], loseDelete: true, sent: [] });

    await userEvent.click(page.getByRole("button", { name: "Disconnect ana@work.example" }));
    await expect.element(page.getByRole("alertdialog")).toBeVisible();
    await userEvent.keyboard("{Tab}{Enter}");

    await expect
      .element(page.getByRole("alertdialog").getByRole("alert"))
      .toHaveTextContent(
        "Couldn't confirm whether Work is disconnected. Check the list before trying again.",
      );
  });
});

describe("the sync banner", () => {
  it("says syncing stopped, and who can fix it", async () => {
    await open({ accounts: [home], syncStopped: true, sent: [] });

    await expect
      .element(
        page.getByText(/stopped getting new mail for every account\. Ask your administrator\./),
      )
      .toBeVisible();
  });

  it("tells an administrator where to look", async () => {
    await open({ accounts: [home], syncStopped: true, role: "admin", sent: [] });

    await expect.element(page.getByText(/Check that its worker is running\./)).toBeVisible();
  });

  it("follows a stop and a recovery on any signed-in page", async () => {
    fakeClock();
    const server: Server = { accounts: [home], sent: [] };
    await open(server, "/settings/other", false);
    await expect.element(page.getByText("Another settings page")).toBeVisible();
    const banner = page.getByText(/stopped getting new mail/);

    const landmark = page.getByRole("region", { name: "Mail sync stopped" });
    await expect.element(landmark).not.toBeInTheDocument();

    server.syncStopped = true;
    await vi.advanceTimersByTimeAsync(60_000);
    await expect.element(banner).toBeVisible();
    // A landmark only while it shows.
    await expect.element(landmark).toBeVisible();
    server.syncStopped = false;
    await vi.advanceTimersByTimeAsync(60_000);

    await expect.element(banner).not.toBeInTheDocument();
    await expect.element(landmark).not.toBeInTheDocument();
  });
});
