import "@/zod-config";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
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
import {
  NewRegistrationSettings,
  type Member,
  type SystemEmailSettings,
} from "@/api/generated/model";
import { RouteError } from "@/components/route-states";
import { Route as App } from "@/routes/_app";
import { Route as Members, membersLoader } from "@/routes/_app/settings/_admin/members";
import {
  Route as Registration,
  registrationLoader,
} from "@/routes/_app/settings/_admin/registration";
import {
  Route as SystemEmail,
  systemEmailLoader,
} from "@/routes/_app/settings/_admin/system-email";
import { ComposeStore } from "@/features/outgoing-messages/compose-store";

const ok = 200;
const unauthorized = 401;
const conflict = 409;
const fault = 500;
const you = "ana@example.com";
const registrationPath = "/api/v0/registration/settings";
const membersPath = "/api/v0/members";
const systemEmail: SystemEmailSettings = {
  host: "smtp.example.com",
  port: 587,
  security: "starttls",
  username: "",
  hasPassword: false,
  fromAddress: "inbok@example.com",
  fromName: "inbok",
  testedAt: "2026-11-12T09:38:00Z",
};

// member returns an active member the server lets an administrator change.
function member(id: number, email: string, changes: Partial<Member> = {}): Member {
  return {
    id,
    email,
    role: "member",
    joinedAt: "2026-03-01T09:00:00Z",
    emailVerified: true,
    totpEnabled: false,
    deactivatedAt: null,
    allowedActions: ["deactivate", "reactivate"],
    ...changes,
  };
}

const admin = member(1, you, { role: "admin", totpEnabled: true, allowedActions: [] });

// Server is what the test API holds and how it answers: registration is
// its saved state and whether system email passed a test, members the
// accounts, systemEmail the saved settings; ended answers every request with
// a 401; failOnce answers each listed request, e.g. "PUT
// /api/v0/registration/settings", once with a server fault; lose drops the
// answer to one request after the server acted on it; hold holds the answer
// to one path until it settles; readsFailFrom answers a path's reads from
// the nth on with a fault; writesEnded answers writes with a 401; testFails
// fails a system email test, which clears its tested time; sent lists
// every request.
type Server = {
  registration?: { state: "closed" | "open"; tested: boolean };
  members?: Member[];
  systemEmail?: SystemEmailSettings;
  ended?: boolean;
  failOnce?: string[];
  lose?: string;
  hold?: { path: string; until: Promise<void> };
  readsFailFrom?: { path: string; nth: number };
  writesEnded?: boolean;
  testFails?: boolean;
  sent: string[];
};

function json(data: unknown, status = ok): Response {
  return Response.json(data, {
    status,
    headers: { "Content-Type": status === ok ? "application/json" : "application/problem+json" },
  });
}

function problem(status: number, type: string, detail: string): Response {
  return json({ type: `/api/v0/problems/${type}`, status, title: "", detail }, status);
}

// registrationOf returns the settings the API shows for server's state.
function registrationOf(server: Server) {
  const { state, tested } = server.registration ?? { state: "closed", tested: false };
  return {
    status: state === "closed" ? "closed" : tested ? "open" : "paused",
    systemEmailTested: tested,
    allowedActions: tested ? ["open", "close"] : ["close"],
  };
}

// answer answers one request as the API would, changing server.
function answer(server: Server, method: string, path: string, body: unknown): Response {
  if (server.ended) return problem(unauthorized, "unauthorized", "");
  if (path === "/api/v0/sessions/current")
    return json({ data: { email: you, emailVerified: true, role: "admin", totpRequired: false } });
  const key = `${method} ${path}`;
  if (server.writesEnded && method !== "GET") return problem(unauthorized, "unauthorized", "");
  if (server.failOnce?.includes(key)) {
    server.failOnce = server.failOnce.filter((k) => k !== key);
    return problem(fault, "internal-error", "Something went wrong on our side.");
  }
  if (method === "GET" && server.readsFailFrom?.path === path) {
    const reads = server.sent.filter((sent) => sent === key).length;
    if (reads >= server.readsFailFrom.nth)
      return problem(fault, "internal-error", "Something went wrong on our side.");
  }
  if (path === registrationPath && method === "GET") return json({ data: registrationOf(server) });
  if (path === registrationPath && method === "PUT") {
    const opening = NewRegistrationSettings.parse(body).open;
    const tested = server.registration?.tested ?? false;
    if (opening && !tested)
      return problem(conflict, "system-email-not-tested", "Send a test message that passes first.");
    server.registration = { state: opening ? "open" : "closed", tested };
    return json({ data: registrationOf(server) });
  }
  if (path === membersPath) return json({ data: { members: server.members ?? [] } });
  const change = /^\/api\/v0\/members\/(\d+)\/(deactivate|reactivate)$/.exec(path);
  if (change) {
    const target = server.members?.find((m) => m.id === Number(change[1]));
    if (!target) return problem(404, "member-not-found", "This account doesn't exist.");
    const saved = {
      ...target,
      deactivatedAt: change[2] === "deactivate" ? "2026-11-15T10:00:00Z" : null,
    };
    server.members = server.members?.map((m) => (m.id === saved.id ? saved : m));
    return json({ data: { member: saved } });
  }
  if (path === "/api/v0/system-email" && method === "GET")
    return json({ data: { settings: server.systemEmail } });
  if (path === "/api/v0/system-email/test-message") {
    const passed = !server.testFails;
    server.systemEmail = {
      ...(server.systemEmail ?? systemEmail),
      testedAt: passed ? "2026-11-15T10:00:00Z" : null,
    };
    server.registration = { state: server.registration?.state ?? "closed", tested: passed };
    return json({ data: passed ? { status: "accepted" } : { status: "failed", reason: "auth" } });
  }
  if (path === "/api/v0/system-email" && method === "PUT") {
    // A save clears the tested time, which pauses an open registration.
    server.systemEmail = { ...(server.systemEmail ?? systemEmail), testedAt: null };
    server.registration = { state: server.registration?.state ?? "closed", tested: false };
    return json({ data: { settings: server.systemEmail } });
  }
  return problem(404, "not-found", "");
}

// open renders the administrator's settings pages in a router of their own
// (web/README.md "Checks and tests"), under the real signed-in guard, in a
// box the size of a phone's frame, or width px wide, starting at path,
// their requests answered by server. It returns the router.
async function open(server: Server, path: string, width = 360) {
  vi.stubGlobal(
    "fetch",
    vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>(
      async (input, init) => {
        const method = (init?.method ?? "GET").toUpperCase();
        const address = input instanceof Request ? input.url : input.toString();
        const requestPath = new URL(address, location.href).pathname;
        const body: unknown = typeof init?.body === "string" ? JSON.parse(init.body) : undefined;
        if (server.hold?.path === requestPath) await server.hold.until;
        const answered = answer(server, method, requestPath, body);
        server.sent.push(`${method} ${requestPath}`);
        if (server.lose !== `${method} ${requestPath}`) return answered;
        server.lose = undefined;
        throw new TypeError("Failed to fetch");
      },
    ),
  );
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  opened = queryClient;
  const root = createRootRouteWithContext<{ queryClient: QueryClient; compose: ComposeStore }>()({
    component: Outlet,
  });
  // The routes' ids match the app's, as the pages' own hooks name them.
  const app = createRoute({
    getParentRoute: () => root,
    id: "_app",
    component: Outlet,
    beforeLoad: App.options.beforeLoad,
  });
  const settings = createRoute({ getParentRoute: () => app, path: "settings", component: Outlet });
  // The administrator guard has its own tests; these are about the pages.
  const adminPages = createRoute({
    getParentRoute: () => settings,
    id: "_admin",
    component: Outlet,
  });
  const router = createRouter({
    routeTree: root.addChildren([
      app.addChildren([
        settings.addChildren([
          adminPages.addChildren([
            createRoute({
              getParentRoute: () => adminPages,
              path: "registration",
              loader: registrationLoader,
              component: Registration.options.component,
            }),
            createRoute({
              getParentRoute: () => adminPages,
              path: "members",
              loader: membersLoader,
              component: Members.options.component,
            }),
            createRoute({
              getParentRoute: () => adminPages,
              path: "system-email",
              loader: systemEmailLoader,
              component: SystemEmail.options.component,
            }),
          ]),
        ]),
      ]),
      createRoute({ getParentRoute: () => root, path: "/sign-in", component: () => "Sign in" }),
    ]),
    history: createMemoryHistory({ initialEntries: [path] }),
    defaultErrorComponent: RouteError,
    // As the app's router does (router.tsx): a visit, a preloaded one too,
    // waits for a read marked out of date.
    defaultStaleReloadMode: "blocking",
    defaultPreloadStaleTime: 0,
    context: { queryClient, compose: new ComposeStore() },
  });
  await render(
    <QueryClientProvider client={queryClient}>
      {/* As the app's frame holds a page: a column that scrolls when its
          page overflows. */}
      <div style={{ display: "flex", flexDirection: "column", width, height: 560 }}>
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            flex: 1,
            minHeight: 0,
            overflow: "auto",
          }}
        >
          <RouterProvider router={router} />
        </div>
      </div>
    </QueryClientProvider>,
  );
  return router;
}

// held returns a promise that settles when release is called.
function held(): { until: Promise<void>; release: () => void } {
  const settle: { done?: () => void } = {};
  const until = new Promise<void>((done) => {
    settle.done = done;
  });
  return { until, release: () => settle.done?.() };
}

// The query client of the page open now, whose reads end with the test.
let opened: QueryClient | undefined;

afterEach(async () => {
  // A refresh still running would otherwise reach the real network once
  // the fetch stub is gone.
  await opened?.cancelQueries();
  opened?.clear();
  vi.unstubAllGlobals();
});

const button = (name: string) => page.getByRole("button", { name, exact: true });
const dialog = () => page.getByRole("alertdialog");
const announcement = () => page.getByRole("status").last();

// confirmByKeyboard presses the dialog's confirming button: Cancel has
// focus, the action is next. A keyboard press counts at once, unlike a click
// within a double-click of the dialog opening.
async function confirmByKeyboard(): Promise<void> {
  await expect.element(dialog()).toBeVisible();
  await userEvent.keyboard("{Tab}{Enter}");
}

// fade returns the member list's fade on side, "right" or "below", if shown.
function fade(side: "right" | "below"): Element | null {
  return document.querySelector(`[data-slot=more-${side}]`);
}

// frames waits for the browser to draw twice, e.g. after a scroll.
function frames(): Promise<void> {
  return new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(() => done())));
}

describe("Registration", () => {
  it("holds the change in its dialog until it is saved", async () => {
    const save = held();
    const server: Server = { registration: { state: "closed", tested: true }, sent: [] };
    await open(server, "/settings/registration");
    await expect.element(page.getByText("Closed", { exact: true })).toBeVisible();
    // Held from here: the save, not the reads that drew the page.
    server.hold = { path: registrationPath, until: save.until };

    await userEvent.click(button("Open registration"));
    await confirmByKeyboard();
    await expect.element(button("Opening...")).toBeVisible();
    await expect.element(button("Cancel")).toBeDisabled();
    // A change already sent can't be called back.
    await userEvent.keyboard("{Escape}");
    await expect.element(dialog()).toBeVisible();

    save.release();
    await expect.element(dialog()).not.toBeInTheDocument();
    await expect.element(page.getByText("Open", { exact: true })).toBeVisible();
    await expect.element(button("Close registration")).toHaveFocus();
    await expect.element(announcement()).toHaveTextContent("Registration opened.");
  });

  it("explains a missing Open and leads to System email", async () => {
    await open(
      { registration: { state: "closed", tested: false }, sent: [] },
      "/settings/registration",
    );

    await expect.element(page.getByText(/needs system email that passed a test/)).toBeVisible();
    await expect.element(button("Open registration")).not.toBeInTheDocument();
    await expect
      .element(page.getByRole("link", { name: "Go to System email" }))
      .toHaveAttribute("href", "/settings/system-email");
  });

  it("moves focus to System email when closing leaves no button", async () => {
    await open(
      { registration: { state: "open", tested: false }, sent: [] },
      "/settings/registration",
    );
    await expect.element(page.getByText("Paused", { exact: true })).toBeVisible();

    await userEvent.click(button("Close registration"));
    await confirmByKeyboard();

    await expect.element(dialog()).not.toBeInTheDocument();
    await expect.element(page.getByText("Closed", { exact: true })).toBeVisible();
    await expect.element(page.getByRole("link", { name: "Go to System email" })).toHaveFocus();
  });

  it("shows a failure in the dialog, where pressing again retries", async () => {
    const server: Server = {
      registration: { state: "closed", tested: true },
      failOnce: [`PUT ${registrationPath}`],
      sent: [],
    };
    await open(server, "/settings/registration");

    await userEvent.click(button("Open registration"));
    await confirmByKeyboard();
    // A fault leaves it unknown whether the change was saved.
    await expect
      .element(dialog().getByRole("alert"))
      .toHaveTextContent(
        "Something went wrong on our side. Couldn't confirm whether registration was opened. Trying again is safe.",
      );
    await expect.element(button("Open registration").last()).toHaveFocus();

    await userEvent.keyboard("{Enter}");
    await expect.element(dialog()).not.toBeInTheDocument();
    expect(server.registration?.state).toBe("open");
  });

  it("says trying again is safe when the answer is lost", async () => {
    const server: Server = {
      registration: { state: "closed", tested: true },
      lose: `PUT ${registrationPath}`,
      sent: [],
    };
    await open(server, "/settings/registration");

    await userEvent.click(button("Open registration"));
    await confirmByKeyboard();

    await expect
      .element(dialog().getByRole("alert"))
      .toHaveTextContent("Couldn't confirm whether registration was opened. Trying again is safe.");
  });

  it("keeps the page and says so when a refresh fails", async () => {
    await open(
      {
        registration: { state: "open", tested: true },
        readsFailFrom: { path: registrationPath, nth: 1 },
        sent: [],
      },
      "/settings/registration",
    );

    await expect.element(button("Close registration")).toBeVisible();
    // The page's own reread, e.g. on returning to the window, fails.
    await opened?.invalidateQueries();
    await expect.element(page.getByText(/Couldn't refresh registration/)).toBeVisible();
    await expect.element(button("Close registration")).toBeVisible();
  });

  it("opens sign-in, and comes back, when the session has ended", async () => {
    const router = await open({ ended: true, sent: [] }, "/settings/registration");

    await expect.element(page.getByText("Sign in")).toBeVisible();
    expect(router.state.location.search).toEqual({ redirect: "/settings/registration" });
  });

  it("reads registration again after System email changed it", async () => {
    const reread = held();
    const server: Server = {
      registration: { state: "open", tested: true },
      systemEmail,
      sent: [],
    };
    const router = await open(server, "/settings/registration");
    await expect.element(page.getByText("Open", { exact: true })).toBeVisible();

    await router.navigate({ to: "/settings/system-email" });
    await userEvent.click(button("Edit settings"));
    await userEvent.click(button("Save only"));
    await expect.element(button("Edit settings")).toBeVisible();
    // Back to Registration, whose read now waits: the page must not draw
    // the status it knows is out of date meanwhile.
    server.hold = { path: registrationPath, until: reread.until };
    const back = router.navigate({ to: "/settings/registration" });
    await frames();
    expect(document.body.textContent).not.toMatch(/Anyone who can reach this inbok can create/);

    reread.release();
    await back;
    await expect.element(page.getByText("Paused", { exact: true })).toBeVisible();
  });
});

describe("Registration, when things change elsewhere", () => {
  it("moves focus on when the action went away while its dialog was open", async () => {
    const server: Server = { registration: { state: "closed", tested: true }, sent: [] };
    await open(server, "/settings/registration");

    await userEvent.click(button("Open registration"));
    await expect.element(dialog()).toBeVisible();
    // Meanwhile system email loses its test, e.g. in another tab: the
    // page's next read has no Open.
    server.registration = { state: "closed", tested: false };
    await opened?.invalidateQueries();
    await expect
      .element(page.getByText(/needs system email that passed a test/))
      .toBeInTheDocument();
    await userEvent.keyboard("{Escape}");

    await expect.element(dialog()).not.toBeInTheDocument();
    await expect.element(page.getByRole("link", { name: "Go to System email" })).toHaveFocus();
  });

  it("reads registration again after a failed test on System email", async () => {
    const server: Server = {
      registration: { state: "open", tested: true },
      systemEmail,
      testFails: true,
      sent: [],
    };
    const router = await open(server, "/settings/registration");
    await expect.element(page.getByText("Open", { exact: true })).toBeVisible();

    await router.navigate({ to: "/settings/system-email" });
    await userEvent.click(button("Send test message"));
    await expect.element(page.getByText(/sign-in|failed|Couldn't/i).first()).toBeVisible();
    await router.navigate({ to: "/settings/registration" });

    await expect.element(page.getByText("Paused", { exact: true })).toBeVisible();
  });

  it("shows the router's failure view when the reread on a visit back fails", async () => {
    const server: Server = { registration: { state: "open", tested: true }, systemEmail, sent: [] };
    const router = await open(server, "/settings/registration");
    await expect.element(page.getByText("Open", { exact: true })).toBeVisible();

    await router.navigate({ to: "/settings/system-email" });
    await userEvent.click(button("Edit settings"));
    await userEvent.click(button("Save only"));
    await expect.element(button("Edit settings")).toBeVisible();
    server.failOnce = [`GET ${registrationPath}`];
    await router.navigate({ to: "/settings/registration" });

    // Not the old status: the read it waited for failed, and says so.
    await expect.element(button("Try again")).toBeVisible();
    expect(document.body.textContent).not.toMatch(/Anyone who can reach this inbok can create/);
  });
});

describe("Members", () => {
  const jo = member(2, "jo@example.com");
  const sam = member(3, "sam@example.com", {
    deactivatedAt: "2026-11-02T09:30:00Z",
    emailVerified: false,
  });
  // The server, not the page, decides whom an administrator can change.
  const kept = member(4, "kept@example.com", { allowedActions: [] });

  it("marks only what differs and offers what the server allows", async () => {
    const server: Server = { members: [admin, jo, sam, kept], sent: [] };
    await open(server, "/settings/members");

    await expect.element(page.getByText("You", { exact: true })).toBeVisible();
    await expect.element(page.getByText("Admin", { exact: true })).toBeVisible();
    await expect.element(page.getByText("Not verified", { exact: true })).toBeVisible();
    await expect.element(page.getByText("Deactivated", { exact: true })).toBeVisible();
    await expect.element(button("Deactivate jo@example.com")).toBeVisible();
    await expect.element(button("Reactivate sam@example.com")).toBeVisible();
    // The page uses what its loader just read rather than reading it again.
    await frames();
    expect(server.sent.filter((sent) => sent === `GET ${membersPath}`)).toHaveLength(1);
    // Neither the administrator nor a member the server keeps has a button.
    expect(page.getByRole("button", { name: /ana@example\.com|kept@example\.com/ }).all()).toEqual(
      [],
    );
  });

  it("holds a change in its dialog, then returns focus to the row", async () => {
    const save = held();
    const server: Server = { members: [admin, jo], sent: [] };
    await open(server, "/settings/members");
    await expect.element(button("Deactivate jo@example.com")).toBeVisible();
    server.hold = { path: "/api/v0/members/2/deactivate", until: save.until };

    await userEvent.click(button("Deactivate jo@example.com"));
    // Heard whole, though the address is split for line breaks.
    await expect
      .element(page.getByRole("alertdialog", { name: "Deactivate jo@example.com?" }))
      .toBeVisible();
    await confirmByKeyboard();
    await expect.element(button("Deactivating...")).toBeVisible();
    await expect.element(button("Cancel")).toBeDisabled();

    save.release();
    await expect.element(dialog()).not.toBeInTheDocument();
    await expect.element(button("Reactivate jo@example.com")).toHaveFocus();
    await expect.element(page.getByText("Deactivated", { exact: true })).toBeVisible();
    await expect.element(announcement()).toHaveTextContent("jo@example.com is deactivated.");
  });

  it("shows a failure in the dialog, where pressing again retries", async () => {
    const server: Server = {
      members: [admin, jo],
      failOnce: ["POST /api/v0/members/2/deactivate"],
      sent: [],
    };
    await open(server, "/settings/members");

    await userEvent.click(button("Deactivate jo@example.com"));
    await confirmByKeyboard();
    await expect
      .element(dialog().getByRole("alert"))
      .toHaveTextContent(
        "Something went wrong on our side. Couldn't confirm whether jo@example.com was deactivated. Trying again is safe.",
      );

    await userEvent.keyboard("{Enter}");
    await expect.element(dialog()).not.toBeInTheDocument();
    expect(server.members?.find((m) => m.id === jo.id)?.deactivatedAt).not.toBeNull();
  });

  it("says trying again is safe when the answer is lost", async () => {
    await open(
      { members: [admin, jo], lose: "POST /api/v0/members/2/deactivate", sent: [] },
      "/settings/members",
    );

    await userEvent.click(button("Deactivate jo@example.com"));
    await confirmByKeyboard();

    await expect
      .element(dialog().getByRole("alert"))
      .toHaveTextContent(
        "Couldn't confirm whether jo@example.com was deactivated. Trying again is safe.",
      );
  });

  it("opens sign-in, and comes back, when the session has ended", async () => {
    const router = await open({ ended: true, sent: [] }, "/settings/members");

    await expect.element(page.getByText("Sign in")).toBeVisible();
    expect(router.state.location.search).toEqual({ redirect: "/settings/members" });
  });

  it("opens sign-in, and comes back, when a change finds the session ended", async () => {
    const server: Server = { members: [admin, jo], sent: [] };
    const router = await open(server, "/settings/members");
    await expect.element(button("Deactivate jo@example.com")).toBeVisible();
    server.writesEnded = true;

    await userEvent.click(button("Deactivate jo@example.com"));
    await confirmByKeyboard();

    await expect.element(page.getByText("Sign in")).toBeVisible();
    expect(router.state.location.search).toEqual({ redirect: "/settings/members" });
  });

  it("keeps the list and says so when a refresh fails", async () => {
    await open(
      { members: [admin, jo], readsFailFrom: { path: membersPath, nth: 1 }, sent: [] },
      "/settings/members",
    );

    await expect.element(button("Deactivate jo@example.com")).toBeVisible();
    // The page's own reread, e.g. on returning to the window, fails.
    await opened?.invalidateQueries();
    await expect.element(page.getByText(/Couldn't refresh the member list/)).toBeVisible();
    await expect.element(button("Deactivate jo@example.com")).toBeVisible();
  });

  it("scrolls the list in its own box, header and member column held", async () => {
    const many = Array.from({ length: 20 }, (_, i) => member(10 + i, `member${i}@example.com`));
    await open(
      { members: [admin, ...many], readsFailFrom: { path: membersPath, nth: 1 }, sent: [] },
      "/settings/members",
    );
    await expect.element(button("Deactivate member19@example.com")).toBeInTheDocument();
    // With the refresh banner above it, the list keeps the height left.
    await opened?.invalidateQueries();
    await expect.element(page.getByText(/Couldn't refresh the member list/)).toBeVisible();
    const region = page.getByRole("region", { name: "Member list" });
    await expect.element(region).toHaveAttribute("tabindex", "0");
    const box = region.element();
    const memberHead = page.getByRole("columnheader", { name: "Member" }).element();
    const statusHead = page.getByRole("columnheader", { name: "Status" }).element();
    const firstRow = page.getByRole("rowheader").first().element();
    const statusBefore = statusHead.getBoundingClientRect().left;
    // The page fits the frame: the list overflows its box, not the page.
    expect(box.scrollHeight).toBeGreaterThan(box.clientHeight);
    expect(box.scrollWidth).toBeGreaterThan(box.clientWidth);

    box.scrollTop = 200;
    box.scrollLeft = 120;
    await frames();

    const top = box.getBoundingClientRect().top;
    const left = box.getBoundingClientRect().left;
    expect(memberHead.getBoundingClientRect().top).toBeCloseTo(top, 0);
    expect(statusHead.getBoundingClientRect().top).toBeCloseTo(top, 0);
    expect(memberHead.getBoundingClientRect().left).toBeCloseTo(left, 0);
    expect(firstRow.getBoundingClientRect().left).toBeCloseTo(left, 0);
    expect(statusHead.getBoundingClientRect().left).toBeCloseTo(statusBefore - 120, 0);
    // The pinned column's edge shows only while the columns are scrolled.
    expect(box.hasAttribute("data-scrolled")).toBe(true);
    // Fades mark the columns to the right and the rows below until the
    // last of each is in view.
    expect(fade("right")).not.toBeNull();
    expect(fade("below")).not.toBeNull();
    box.scrollLeft = box.scrollWidth;
    box.scrollTop = box.scrollHeight;
    await frames();
    expect(fade("right")).toBeNull();
    expect(fade("below")).toBeNull();
    // Scrolling back up to a button stops below the header, not under it.
    box.scrollTop = box.scrollHeight;
    await frames();
    const earlier = button("Deactivate member3@example.com").element();
    // As Shift+Tab scrolls a button into view: to the nearest edge.
    earlier.scrollIntoView({ block: "nearest" });
    await frames();
    expect(earlier.getBoundingClientRect().top).toBeGreaterThanOrEqual(
      memberHead.getBoundingClientRect().bottom - 1,
    );
    // Scrolling down to a button stops above the bottom fade.
    box.scrollTop = 0;
    await frames();
    const later = button("Deactivate member15@example.com").element();
    later.scrollIntoView({ block: "nearest" });
    await frames();
    expect(later.getBoundingClientRect().bottom).toBeLessThanOrEqual(
      box.getBoundingClientRect().bottom - 32 + 1,
    );
    // A row's action still answers a press.
    await userEvent.click(button("Deactivate member19@example.com"));
    await expect.element(dialog()).toBeVisible();
  });

  it("takes a Tab stop only while the list scrolls", async () => {
    await open({ members: [admin, jo], sent: [] }, "/settings/members", 900);
    await expect.element(button("Deactivate jo@example.com")).toBeVisible();
    const box = document.querySelector("[data-slot=table-container]");

    // Wide and short enough to fit: nothing to scroll, so no Tab stop, no
    // region of its own, and no fade.
    expect(box?.hasAttribute("tabindex")).toBe(false);
    expect(box?.getAttribute("role")).toBeNull();
    expect(fade("right")).toBeNull();
    expect(fade("below")).toBeNull();
  });

  it("keeps an address with no break point inside its column", async () => {
    const long = member(5, `${"w".repeat(64)}@${"x".repeat(63)}.com`);
    await open({ members: [admin, long], sent: [] }, "/settings/members");
    const cell = page.getByRole("rowheader").last();
    await expect.element(cell).toBeVisible();

    const element = cell.element();
    expect(element.scrollWidth).toBeLessThanOrEqual(element.clientWidth + 1);
  });
});
