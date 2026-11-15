import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  Outlet,
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from "@tanstack/react-router";
import type { CurrentSession, TotpStatus } from "@/api/generated/model";
import { Route as Security } from "@/routes/_app/settings/security/index";
import { Route as TwoFactor } from "@/routes/_app/settings/security/two-factor";

const unauthorized = 401;
const forbidden = 403;
const conflict = 409;
const notWaitingType = "/api/v0/problems/backup-codes-not-waiting";
const offNotice =
  "Two-factor authentication is off. Remove the inbok entry from your authenticator app.";
const codes = [
  "AAAA-BBBB1",
  "AAAA-BBBB2",
  "AAAA-BBBB3",
  "AAAA-BBBB4",
  "AAAA-BBBB5",
  "AAAA-BBBB6",
  "AAAA-BBBB7",
  "AAAA-BBBB8",
  "AAAA-BBBB9",
  "AAAA-BBBB0",
];

// The server the pages talk to: its two-factor status, the role the
// session read answers, and the requests it was sent ("METHOD /path").
type Server = {
  totp: TotpStatus;
  role?: CurrentSession["role"];
  sent: string[];
  // Whether a turn-off commits but its answer never arrives.
  loseTurnOff?: boolean;
  // Requests ("METHOD /path") refused with a problem of status and type,
  // once; before runs before the answer, e.g. to change the role.
  refuse?: Partial<Record<string, { status: number; type: string; before?: () => void }>>;
  // Requests ("METHOD /path") that commit, then lose their answer, once.
  lose?: Partial<Record<string, true>>;
};

// problem is an answer refused with status and the problem type.
function problem(status: number, type: string): Response {
  return Response.json(
    { type, status, title: "Refused", detail: "Refused." },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}

// answer replies to one request as inbok's API would.
function answer(server: Server, method: string, path: string): Response {
  const refusal = server.refuse?.[`${method} ${path}`];
  if (refusal) {
    delete server.refuse?.[`${method} ${path}`];
    refusal.before?.();
    return problem(refusal.status, refusal.type);
  }
  if (method === "GET" && path === "/api/v0/sessions/current")
    return Response.json({
      data: {
        email: "ana@example.com",
        emailVerified: true,
        role: server.role ?? "member",
        totpRequired: false,
      },
    });
  if (method === "GET" && path === "/api/v0/sessions")
    return Response.json({
      data: {
        sessions: [
          {
            id: "0193c8a0-0000-7000-8000-000000000001",
            browser: "Firefox",
            os: "Linux",
            signedInAt: "2026-11-01T10:00:00Z",
            signedInFrom: "203.0.113.7",
            lastActiveAt: "2026-11-01T10:00:00Z",
            current: true,
          },
        ],
      },
    });
  if (method === "GET" && path === "/api/v0/totp") return Response.json({ data: server.totp });
  if (method === "POST" && path === "/api/v0/totp/backup-codes") {
    server.totp = { ...server.totp, backupCodesWaiting: true };
    return Response.json({ data: { backupCodes: codes, token: "replace-token" } });
  }
  if (method === "POST" && path === "/api/v0/totp/backup-codes/finish") {
    server.totp = { status: "on", backupCodesLeft: 10, backupCodesWaiting: false };
    return Response.json({ data: server.totp });
  }
  if (method === "POST" && path === "/api/v0/totp/disable") {
    server.totp = { status: "off", backupCodesLeft: 0, backupCodesWaiting: false };
    if (server.loseTurnOff) throw new TypeError("Failed to fetch");
    return Response.json({ data: server.totp });
  }
  throw new Error(`unexpected request: ${method} ${path}`);
}

// open renders the two-factor page and Security in a router of their own
// (web/README.md "Checks and tests"), at start, for a signed-in member
// with role, answered by server, and returns the router.
async function open(
  server: Server,
  role: CurrentSession["role"] = "member",
  start = "/settings/security/two-factor",
) {
  server.role ??= role;
  vi.stubGlobal(
    "fetch",
    vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>(
      async (input, init) => {
        const request = input instanceof Request ? input : undefined;
        const method = (init?.method ?? request?.method ?? "GET").toUpperCase();
        const address =
          input instanceof Request ? input.url : input instanceof URL ? input.href : input;
        const path = new URL(address, location.href).pathname;
        server.sent.push(`${method} ${path}`);
        const answered = answer(server, method, path);
        if (!server.lose?.[`${method} ${path}`]) return answered;
        delete server.lose[`${method} ${path}`];
        throw new TypeError("Failed to fetch");
      },
    ),
  );
  const root = createRootRoute({ component: Outlet });
  // The signed-in layout, under its real id, so the two-factor route finds
  // the session in its context.
  const app = createRoute({
    getParentRoute: () => root,
    id: "_app",
    component: Outlet,
    beforeLoad: () => ({ session: { email: "ana@example.com", role } }),
  });
  const router = createRouter({
    routeTree: root.addChildren([
      app.addChildren([
        createRoute({
          getParentRoute: () => app,
          path: "/settings/security",
          component: Security.options.component,
        }),
        createRoute({
          getParentRoute: () => app,
          path: "/settings/security/two-factor",
          component: TwoFactor.options.component,
        }),
      ]),
      createRoute({ getParentRoute: () => root, path: "/sign-in", component: () => "Sign in" }),
    ]),
    history: createMemoryHistory({ initialEntries: [start] }),
  });
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return router;
}

// on is a server where two-factor authentication is on with ten codes.
function on(): Server {
  return { totp: { status: "on", backupCodesLeft: 10, backupCodesWaiting: false }, sent: [] };
}

const codeField = () => page.getByRole("textbox", { name: "Authenticator code" });
const backLink = () => page.getByRole("link", { name: "Back to Security" });

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("two-factor management", () => {
  it("turns off with a code on the page, after the red button opens the field", async () => {
    await open(on());
    const turnOff = page.getByRole("button", { name: "Turn off two-factor authentication" });

    await userEvent.click(turnOff);
    await expect.element(codeField()).toHaveFocus();
    await userEvent.click(page.getByRole("button", { name: "Keep two-factor authentication" }));
    await expect.element(codeField()).not.toBeInTheDocument();
    await expect.element(page.getByRole("heading", { name: "Turn off" })).toHaveFocus();

    await userEvent.click(turnOff);
    await userEvent.keyboard("123456");
    await userEvent.click(page.getByRole("button", { name: "Turn off two-factor authentication" }));

    await expect.element(page.getByText(offNotice)).toBeVisible();
    await expect.element(page.getByRole("heading", { level: 1, name: "Security" })).toBeVisible();
  });

  it("reports the turn-off on Security once, not again after Back and Forward", async () => {
    const router = await open(on());
    await userEvent.click(page.getByRole("button", { name: "Turn off two-factor authentication" }));
    await userEvent.keyboard("123456");
    await userEvent.click(page.getByRole("button", { name: "Turn off two-factor authentication" }));
    await expect.element(page.getByText(offNotice)).toBeVisible();
    await vi.waitFor(() => expect(router.state.location.state.twoFactorChange).toBeUndefined());

    router.history.back();
    await expect
      .element(page.getByRole("heading", { level: 1, name: "Two-factor authentication" }))
      .toBeVisible();
    router.history.forward();

    await expect.element(page.getByRole("heading", { level: 1, name: "Security" })).toBeVisible();
    await expect.element(page.getByText(/Firefox on Linux/)).toBeVisible();
    expect(page.getByText(offNotice).query()).toBeNull();
  });

  it("reports a turn-off whose answer was lost once a status read finds it off", async () => {
    await open({ ...on(), loseTurnOff: true });

    await userEvent.click(page.getByRole("button", { name: "Turn off two-factor authentication" }));
    await userEvent.keyboard("123456");
    await userEvent.click(page.getByRole("button", { name: "Turn off two-factor authentication" }));

    await expect.element(page.getByText(offNotice)).toBeVisible();
    await expect.element(page.getByRole("heading", { level: 1, name: "Security" })).toBeVisible();
  });

  it("tells an administrator they must keep it on, with no way to turn it off", async () => {
    await open(on(), "admin");

    await expect
      .element(page.getByText("Administrators must keep two-factor authentication on."))
      .toBeVisible();
    expect(
      page.getByRole("button", { name: "Turn off two-factor authentication" }).query(),
    ).toBeNull();
  });

  it("replaces the backup codes with a fresh code, asking before leaving the new ones", async () => {
    await open(on());

    await userEvent.click(page.getByRole("button", { name: "Replace backup codes" }));
    await expect.element(codeField()).toHaveFocus();
    await userEvent.keyboard("123456");
    await userEvent.click(page.getByRole("button", { name: "Continue" }));
    await expect
      .element(page.getByRole("heading", { level: 3, name: "Save your new backup codes" }))
      .toHaveFocus();

    await userEvent.click(backLink());
    const dialog = page.getByRole("alertdialog", {
      name: "Leave without activating the new codes?",
    });
    await expect.element(dialog).toBeVisible();
    await userEvent.click(dialog.getByRole("button", { name: "Stay" }));

    await userEvent.click(page.getByRole("button", { name: "I've saved these codes" }));
    await expect
      .element(page.getByText("New backup codes are active. The old ones no longer work."))
      .toBeVisible();
    await expect.element(page.getByText(/10 of 10 backup codes left\./)).toBeVisible();
  });

  it("says codes left unsaved earlier were not saved, with no way to show them again", async () => {
    await open({
      totp: { status: "on", backupCodesLeft: 10, backupCodesWaiting: true },
      sent: [],
    });

    await expect
      .element(
        page.getByText(
          "10 of 10 backup codes left. New codes were made but not saved yet; your old codes still work.",
        ),
      )
      .toBeVisible();
    await expect.element(page.getByRole("button", { name: "Replace backup codes" })).toBeVisible();
    expect(page.getByRole("button", { name: "Show the new codes" }).query()).toBeNull();
  });

  it("drops the new codes when another tab made newer ones, and says codes wait unsaved", async () => {
    const server = on();
    await open(server);
    await userEvent.click(page.getByRole("button", { name: "Replace backup codes" }));
    await userEvent.keyboard("123456");
    await userEvent.click(page.getByRole("button", { name: "Continue" }));
    await expect.element(page.getByRole("list", { name: "Backup codes" })).toBeVisible();
    server.refuse = {
      "POST /api/v0/totp/backup-codes/finish": { status: conflict, type: notWaitingType },
    };

    await userEvent.click(page.getByRole("button", { name: "I've saved these codes" }));

    await expect.element(page.getByRole("list", { name: "Backup codes" })).not.toBeInTheDocument();
    await expect
      .element(page.getByText(/New codes were made but not saved yet; your old codes still work\./))
      .toBeVisible();
  });

  it("opens sign-in when the session read finds the session ended", async () => {
    const server = on();
    server.refuse = {
      "GET /api/v0/sessions/current": {
        status: unauthorized,
        type: "/api/v0/problems/unauthenticated",
      },
    };
    const router = await open(server);

    await expect.element(page.getByText("Sign in")).toBeVisible();
    expect(router.state.location.pathname).toBe("/sign-in");
  });

  it("opens sign-in when a write finds the session ended", async () => {
    const server = on();
    server.refuse = {
      "POST /api/v0/totp/backup-codes": {
        status: unauthorized,
        type: "/api/v0/problems/unauthenticated",
      },
    };
    const router = await open(server);

    await userEvent.click(page.getByRole("button", { name: "Replace backup codes" }));
    await userEvent.keyboard("123456");
    await userEvent.click(page.getByRole("button", { name: "Continue" }));

    await expect.element(page.getByText("Sign in")).toBeVisible();
    expect(router.state.location.pathname).toBe("/sign-in");
  });

  it("shows the administrator line after a refused turn-off rereads the role", async () => {
    const server = on();
    server.refuse = {
      "POST /api/v0/totp/disable": {
        status: forbidden,
        type: "/api/v0/problems/totp-disable-forbidden",
        before: () => {
          server.role = "admin";
        },
      },
    };
    await open(server);

    await userEvent.click(page.getByRole("button", { name: "Turn off two-factor authentication" }));
    await userEvent.keyboard("123456");
    await userEvent.click(page.getByRole("button", { name: "Turn off two-factor authentication" }));

    await expect
      .element(page.getByText("Administrators must keep two-factor authentication on."))
      .toBeVisible();
    expect(
      page.getByRole("button", { name: "Turn off two-factor authentication" }).query(),
    ).toBeNull();
    // The form is gone with its way back: focus and the other section move on.
    await expect.element(page.getByRole("heading", { name: "Turn off" })).toHaveFocus();
    const replace = page.getByRole("button", { name: "Replace backup codes" });
    await expect.element(replace).toBeEnabled();
    await userEvent.click(replace);
    await expect.element(codeField()).toHaveFocus();
  });
});
