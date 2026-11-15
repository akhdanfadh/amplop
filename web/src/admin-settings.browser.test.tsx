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
import { NewRegistrationSettings, type SystemEmailSettings } from "@/api/generated/model";
import { RouteError } from "@/components/route-states";
import { Route as App } from "@/routes/_app";
import {
  Route as Registration,
  registrationLoader,
} from "@/routes/_app/settings/_admin/registration";
import {
  Route as SystemEmail,
  systemEmailLoader,
} from "@/routes/_app/settings/_admin/system-email";

const ok = 200;
const unauthorized = 401;
const conflict = 409;
const fault = 500;
const you = "ana@example.com";
const registrationPath = "/api/v0/registration/settings";
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

// Server is what the test API holds and how it answers: registration is
// its saved state and whether system email passed a test, systemEmail the
// saved settings; ended answers every request with a 401; failOnce answers
// each listed request, e.g. "PUT /api/v0/registration/settings", once with
// a server fault; lose drops the answer to one request after the server
// acted on it; hold holds the answer to one path until it settles;
// readsFailFrom answers a path's reads from the nth on with a fault;
// testFails fails a system email test, which clears its tested time; sent
// lists every request.
type Server = {
  registration?: { state: "closed" | "open"; tested: boolean };
  systemEmail?: SystemEmailSettings;
  ended?: boolean;
  failOnce?: string[];
  lose?: string;
  hold?: { path: string; until: Promise<void> };
  readsFailFrom?: { path: string; nth: number };
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
// box the size of a phone's frame, starting at path, their requests
// answered by server. It returns the router.
async function open(server: Server, path: string) {
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
  const root = createRootRouteWithContext<{ queryClient: QueryClient }>()({ component: Outlet });
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
    context: { queryClient },
  });
  await render(
    <QueryClientProvider client={queryClient}>
      {/* As the app's frame holds a page: a column that scrolls when its
          page overflows. */}
      <div style={{ display: "flex", flexDirection: "column", width: 360, height: 560 }}>
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
