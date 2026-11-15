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
import { NewSystemEmailSettings, type SystemEmailSettings } from "@/api/generated/model";
import { Route as App } from "@/routes/_app";
import { Route as SystemEmail, loadSystemEmail } from "@/routes/_app/settings/_admin/system-email";

const ok = 200;
const unauthorized = 401;
const saved: SystemEmailSettings = {
  host: "smtp.example.com",
  port: 465,
  security: "tls",
  username: "inbok@example.com",
  hasPassword: true,
  fromAddress: "inbok@example.com",
  fromName: "inbok",
  testedAt: "2026-11-12T09:38:00Z",
};

// Server is what the test API holds and how it answers: settings is what
// is saved; ended answers every request with a 401, and readsEnded every
// request but the session read; readsFail answers the settings read with a
// server fault; testFails fails a test; lose drops the answer to one
// request, e.g. "PUT /api/v0/system-email", after the server acted on it;
// sent lists every request, e.g. "GET /api/v0/system-email".
type Server = {
  settings?: SystemEmailSettings;
  ended?: boolean;
  readsEnded?: boolean;
  readsFail?: boolean;
  testFails?: boolean;
  lose?: string;
  sent: string[];
  bodies: unknown[];
};

function json(data: unknown, status = ok): Response {
  return Response.json(data, {
    status,
    headers: { "Content-Type": status === ok ? "application/json" : "application/problem+json" },
  });
}

// answer answers one request as the API would, changing server.
function answer(server: Server, method: string, path: string, body: unknown): Response {
  if (server.ended || (server.readsEnded && path !== "/api/v0/sessions/current"))
    return json(
      { type: "/api/v0/problems/unauthorized", status: unauthorized, title: "", detail: "" },
      unauthorized,
    );
  if (path === "/api/v0/sessions/current")
    return json({
      data: { email: "ana@example.com", emailVerified: true, role: "admin", totpRequired: false },
    });
  if (path === "/api/v0/system-email" && method === "GET")
    return server.readsFail
      ? json(
          {
            type: "/api/v0/problems/internal-error",
            status: 500,
            title: "Internal Server Error",
            detail: "Something went wrong on our side. Try again in a moment.",
          },
          500,
        )
      : json({ data: { settings: server.settings } });
  if (path === "/api/v0/system-email" && method === "PUT") {
    const input = NewSystemEmailSettings.parse(body);
    const { password, ...rest } = input;
    server.settings = {
      ...rest,
      username: rest.username ?? "",
      hasPassword: Boolean(rest.username && (password || server.settings?.hasPassword)),
      testedAt: null,
    };
    return json({ data: { settings: server.settings } });
  }
  if (path === "/api/v0/system-email/test-message") {
    if (server.settings && !server.testFails)
      server.settings = { ...server.settings, testedAt: "2026-11-15T10:00:00Z" };
    else if (server.settings) server.settings = { ...server.settings, testedAt: null };
    return json({
      data: server.testFails ? { status: "failed", reason: "auth" } : { status: "accepted" },
    });
  }
  return json({ type: "about:blank", status: 404, title: "", detail: "" }, 404);
}

// open renders System email in a router of its own (web/README.md "Checks
// and tests"), under the real signed-in guard, its
// requests answered by server, with Security and sign-in to leave to.
// It returns the router.
async function open(server: Server) {
  vi.stubGlobal(
    "fetch",
    vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>(
      async (input, init) => {
        const method = (init?.method ?? "GET").toUpperCase();
        const address = input instanceof Request ? input.url : input.toString();
        const path = new URL(address, location.href).pathname;
        server.sent.push(`${method} ${path}`);
        const body: unknown = typeof init?.body === "string" ? JSON.parse(init.body) : undefined;
        if (body !== undefined) server.bodies.push(body);
        const answered = answer(server, method, path, body);
        if (server.lose !== `${method} ${path}`) return answered;
        server.lose = undefined;
        throw new TypeError("Failed to fetch");
      },
    ),
  );
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  opened = queryClient;
  const root = createRootRouteWithContext<{ queryClient: QueryClient }>()({ component: Outlet });
  // The routes' ids match the app's, as the page's own hooks name them.
  const app = createRoute({
    getParentRoute: () => root,
    id: "_app",
    component: Outlet,
    beforeLoad: App.options.beforeLoad,
  });
  const settings = createRoute({ getParentRoute: () => app, path: "settings", component: Outlet });
  // The administrator guard has its own tests; this page's are about the
  // page.
  const admin = createRoute({ getParentRoute: () => settings, id: "_admin", component: Outlet });
  const router = createRouter({
    routeTree: root.addChildren([
      app.addChildren([
        settings.addChildren([
          admin.addChildren([
            createRoute({
              getParentRoute: () => admin,
              path: "system-email",
              loader: loadSystemEmail,
              component: SystemEmail.options.component,
            }),
          ]),
          createRoute({
            getParentRoute: () => settings,
            path: "security",
            component: () => "Security",
          }),
        ]),
      ]),
      createRoute({ getParentRoute: () => root, path: "/sign-in", component: () => "Sign in" }),
    ]),
    history: createMemoryHistory({ initialEntries: ["/settings/system-email"] }),
    context: { queryClient },
  });
  await render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return router;
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
const field = (name: string) => page.getByRole("textbox", { name, exact: true });
const summary = () => page.getByRole("definition");

describe("System email", () => {
  it("shows the saved settings and opens the form in their place", async () => {
    await open({ settings: saved, sent: [], bodies: [] });

    await expect.element(page.getByText("Tested", { exact: true })).toBeVisible();
    await expect.element(page.getByText("inbok <inbok@example.com>")).toBeVisible();
    await expect.element(page.getByText(/Last test passed/)).toBeVisible();

    await userEvent.click(button("Edit settings"));

    await expect.element(field("Server")).toHaveFocus();
    await expect.element(field("Server")).toHaveValue("smtp.example.com");
    await expect.element(field("Port")).toHaveValue("465");
    await expect.element(page.getByRole("radio", { name: "TLS" })).toBeChecked();
    await expect.element(button("Send test message")).toBeDisabled();
    await expect
      .element(page.getByText("Save or cancel your changes to send a test."))
      .toBeVisible();
    expect(summary().elements()).toHaveLength(0);
  });

  it("saves, closes the form, and gives focus back to Edit settings", async () => {
    const server: Server = { settings: saved, sent: [], bodies: [] };
    await open(server);
    await userEvent.click(button("Edit settings"));

    await userEvent.fill(field("From name"), "inbok team");
    await userEvent.click(button("Save settings"));

    await expect.element(button("Edit settings")).toHaveFocus();
    await expect.element(page.getByText("inbok team <inbok@example.com>")).toBeVisible();
    await expect
      .element(page.getByText("Settings saved. Send a test message to check them."))
      .toBeVisible();
    await expect.element(page.getByText("Not confirmed", { exact: true })).toBeVisible();
    // The port goes as a number, the password left out keeps the saved one.
    expect(server.bodies).toEqual([
      expect.objectContaining({ port: 465, password: "", fromName: "inbok team" }),
    ]);
  });

  it("moves focus to a refused Security choice, which names its error", async () => {
    await open({ settings: saved, sent: [], bodies: [] });
    await userEvent.click(button("Edit settings"));

    await userEvent.click(page.getByText("None", { exact: true }));
    await userEvent.click(button("Save settings"));

    const none = page.getByRole("radio", { name: "None" });
    await expect.element(none).toHaveFocus();
    await expect
      .element(none)
      .toHaveAccessibleDescription(
        "Not encrypted, so it can't sign in. For a local test server only. Choose TLS or STARTTLS to sign in with a username.",
      );
  });

  it("keeps the form open after a lost answer, and Cancel shows what was saved", async () => {
    const server: Server = {
      settings: saved,
      lose: "PUT /api/v0/system-email",
      sent: [],
      bodies: [],
    };
    await open(server);
    await userEvent.click(button("Edit settings"));

    await userEvent.fill(field("From name"), "inbok team");
    const reads = () => server.sent.filter((request) => request === "GET /api/v0/system-email");
    const readsBefore = reads().length;
    await userEvent.click(button("Save settings"));

    await expect
      .element(page.getByText(/We couldn't confirm the settings were saved\. Save again/))
      .toBeVisible();
    await expect.element(field("From name")).toBeEnabled();
    await expect.element(button("Save settings")).toBeEnabled();
    // The page reads what is saved now.
    await vi.waitFor(() => expect(reads()).toHaveLength(readsBefore + 1));

    await userEvent.click(button("Cancel"));

    await expect.element(page.getByText("inbok team <inbok@example.com>")).toBeVisible();
    await expect.element(page.getByText("Not confirmed", { exact: true })).toBeVisible();
  });

  it("moves a usual port with Security and keeps a typed one", async () => {
    await open({ settings: saved, sent: [], bodies: [] });
    await userEvent.click(button("Edit settings"));

    await userEvent.click(page.getByText("STARTTLS", { exact: true }));
    await expect.element(field("Port")).toHaveValue("587");
    await expect
      .element(field("Port"))
      .toHaveAccessibleDescription("Changed to 587, the usual port for STARTTLS.");

    await userEvent.fill(field("Port"), "2525");
    await userEvent.click(page.getByText("TLS", { exact: true }));
    await expect.element(field("Port")).toHaveValue("2525");
    await expect
      .element(field("Port"))
      .toHaveAccessibleDescription("The usual port for TLS is 465.");
  });

  it("opens the form at once when nothing is saved, with nothing to cancel", async () => {
    await open({ sent: [], bodies: [] });

    await expect.element(page.getByText("Not set up", { exact: true })).toBeVisible();
    await expect.element(field("Server")).toBeVisible();
    await expect.element(field("Port")).toHaveValue("587");
    expect(button("Cancel").elements()).toHaveLength(0);
    await expect
      .element(page.getByText("Save the settings before sending a test message."))
      .toBeVisible();
    await expect.element(button("Send test message")).toBeDisabled();
  });

  it("asks before leaving with changes, and not after typing them back", async () => {
    const router = await open({ settings: saved, sent: [], bodies: [] });
    await userEvent.click(button("Edit settings"));

    await userEvent.fill(field("From name"), "inbok team");
    void router.navigate({ to: "/settings/security" });
    await expect
      .element(page.getByRole("alertdialog", { name: "Discard your changes?" }))
      .toBeVisible();
    await userEvent.click(button("Keep editing"));
    await expect.element(field("From name")).toHaveValue("inbok team");

    await userEvent.fill(field("From name"), "inbok");
    await router.navigate({ to: "/settings/security" });
    await expect.element(page.getByText("Security", { exact: true })).toBeVisible();
  });

  it("opens sign-in when the session ends on a save, and comes back here after", async () => {
    const server: Server = { settings: saved, sent: [], bodies: [] };
    const router = await open(server);
    await userEvent.click(button("Edit settings"));
    await userEvent.fill(field("From name"), "inbok team");

    server.ended = true;
    await userEvent.click(button("Save settings"));

    await expect.element(page.getByText("Sign in", { exact: true })).toBeVisible();
    expect(router.state.location.pathname).toBe("/sign-in");
    expect(router.state.location.search).toEqual({ redirect: "/settings/system-email" });
  });

  it("opens sign-in when the session ended before the page loaded", async () => {
    // The guard's session read passes; the settings read finds it ended,
    // as when another tab signed out between the two.
    const router = await open({ settings: saved, readsEnded: true, sent: [], bodies: [] });

    await expect.element(page.getByText("Sign in", { exact: true })).toBeVisible();
    expect(router.state.location.search).toEqual({ redirect: "/settings/system-email" });
  });

  it("opens sign-in when the session ends on a test message", async () => {
    const server: Server = { settings: saved, sent: [], bodies: [] };
    const router = await open(server);
    await expect.element(button("Send test message")).toBeEnabled();

    server.ended = true;
    await userEvent.click(button("Send test message"));

    await expect.element(page.getByText("Sign in", { exact: true })).toBeVisible();
    expect(router.state.location.search).toEqual({ redirect: "/settings/system-email" });
  });

  it("says why a test failed, and that the settings have not passed one", async () => {
    await open({ settings: saved, testFails: true, sent: [], bodies: [] });
    await expect.element(button("Send test message")).toBeEnabled();

    await userEvent.click(button("Send test message"));

    await expect
      .element(
        page.getByText("The mail server rejected the sign-in. Check the username and password."),
      )
      .toBeVisible();
    await expect.element(page.getByText("These settings haven't passed a test yet.")).toBeVisible();
    await expect.element(page.getByText("Not confirmed", { exact: true })).toBeVisible();
  });

  it("keeps the summary when a refresh fails, says so, and recovers on Try again", async () => {
    const server: Server = { settings: saved, sent: [], bodies: [] };
    const router = await open(server);
    await expect.element(page.getByText("inbok <inbok@example.com>")).toBeVisible();

    server.readsFail = true;
    await router.options.context.queryClient.refetchQueries();

    await expect
      .element(page.getByText(/Could not check the system email settings\./))
      .toBeVisible();
    await expect.element(page.getByText("inbok <inbok@example.com>")).toBeVisible();

    server.readsFail = false;
    await userEvent.click(button("Try again"));
    await expect
      .element(page.getByText(/Could not check the system email settings\./))
      .not.toBeInTheDocument();
  });
});
