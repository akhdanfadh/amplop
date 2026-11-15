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
const unprocessable = 422;
const notWaitingType = "/api/v0/problems/backup-codes-not-waiting";
const onNotice = "Two-factor authentication is on. Your other browsers are signed out.";
const password = "correct horse battery staple";
const secret = "JBSWY3DPEHPK3PXP";
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
// session read answers, the requests it was sent ("METHOD /path"), and,
// when set, a finish it holds unanswered.
type Server = {
  totp: TotpStatus;
  role?: CurrentSession["role"];
  sent: string[];
  hold?: Promise<void>;
  // Whether a turn-off commits but its answer never arrives.
  loseTurnOff?: boolean;
  // Requests ("METHOD /path") refused with a problem of status and type,
  // once; before runs before the answer, e.g. to change the role.
  refuse?: Partial<Record<string, { status: number; type: string; before?: () => void }>>;
  // Requests ("METHOD /path") that commit, then lose their answer, once.
  lose?: Partial<Record<string, true>>;
  // When set, a sessions read waits for it before answering.
  holdSessions?: Promise<void>;
};

// problem is an answer refused with status and the problem type.
function problem(status: number, type: string): Response {
  return Response.json(
    { type, status, title: "Refused", detail: "Refused." },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}

// answer replies to one request as inbok's API would.
async function answer(
  server: Server,
  method: string,
  path: string,
  body: unknown,
): Promise<Response> {
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
  if (method === "GET" && path === "/api/v0/sessions") {
    await server.holdSessions;
    const sessions = [
      {
        id: "0193c8a0-0000-7000-8000-000000000001",
        browser: "Firefox",
        os: "Linux",
        signedInAt: "2026-11-01T10:00:00Z",
        signedInFrom: "203.0.113.7",
        lastActiveAt: "2026-11-01T10:00:00Z",
        current: true,
      },
    ];
    // Turning on signs out the other browsers.
    if (server.totp.status !== "on")
      sessions.push({
        id: "0193c8a0-0000-7000-8000-000000000002",
        browser: "Safari",
        os: "iOS",
        signedInAt: "2026-11-02T10:00:00Z",
        signedInFrom: "203.0.113.8",
        lastActiveAt: "2026-11-02T10:00:00Z",
        current: false,
      });
    return Response.json({ data: { sessions } });
  }
  if (method === "GET" && path === "/api/v0/totp") return Response.json({ data: server.totp });
  if (method === "POST" && path === "/api/v0/totp") {
    if (
      !(
        typeof body === "object" &&
        body !== null &&
        "password" in body &&
        body.password === password
      )
    )
      return Response.json(
        {
          type: "/api/v0/problems/invalid-password",
          status: unprocessable,
          title: "Incorrect password",
          detail: "Enter your current password.",
          errors: [{ location: "body.password", message: "Enter your current password." }],
        },
        { status: unprocessable, headers: { "Content-Type": "application/problem+json" } },
      );
    server.totp = { status: "pending", backupCodesLeft: 0, backupCodesWaiting: false };
    return Response.json({
      data: { secret, uri: `otpauth://totp/inbok:ana%40example.com?secret=${secret}&issuer=inbok` },
    });
  }
  if (method === "POST" && path === "/api/v0/totp/backup-codes/waiting")
    return Response.json({ data: { backupCodes: codes, token: "shown-again-token" } });
  if (method === "DELETE" && path === "/api/v0/totp/enrollment") {
    server.totp = { status: "off", backupCodesLeft: 0, backupCodesWaiting: false };
    return Response.json({ data: server.totp });
  }
  if (method === "POST" && path === "/api/v0/totp/enrollment/code") {
    server.totp = { ...server.totp, backupCodesWaiting: true };
    return Response.json({ data: { backupCodes: codes, token: "setup-token" } });
  }
  if (method === "POST" && path === "/api/v0/totp/enrollment/finish") {
    await server.hold;
    server.totp = { status: "on", backupCodesLeft: 10, backupCodesWaiting: false };
    return Response.json({ data: server.totp });
  }
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
        const body: unknown = typeof init?.body === "string" ? JSON.parse(init.body) : undefined;
        const answered = await answer(server, method, path, body);
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

// off is a server where two-factor authentication is off.
function off(): Server {
  return { totp: { status: "off", backupCodesLeft: 0, backupCodesWaiting: false }, sent: [] };
}

// on is a server where two-factor authentication is on with ten codes.
function on(): Server {
  return { totp: { status: "on", backupCodesLeft: 10, backupCodesWaiting: false }, sent: [] };
}

const passwordField = () => page.getByLabelText("Password", { exact: true });
const codeField = () => page.getByRole("textbox", { name: "Authenticator code" });
const backLink = () => page.getByRole("link", { name: "Back to Security" });

// toQr enters the password on the setup's password step.
async function toQr(): Promise<void> {
  await expect.element(passwordField()).toHaveFocus();
  await userEvent.fill(passwordField(), password);
  await userEvent.click(page.getByRole("button", { name: "Continue" }));
  await expect.element(page.getByRole("heading", { name: /Scan this QR code/ })).toHaveFocus();
}

// toCodes checks the first code on the QR step.
async function toCodes(): Promise<void> {
  await userEvent.click(codeField());
  await userEvent.keyboard("123456");
  await userEvent.click(page.getByRole("button", { name: "Continue" }));
  await expect
    .element(page.getByRole("heading", { level: 2, name: "Save your backup codes" }))
    .toHaveFocus();
}

// Lets a held finish answer. The transport holds every later request
// behind it, in this test and the next, until it settles.
let release: (() => void) | undefined;

afterEach(() => {
  release?.();
  release = undefined;
  vi.unstubAllGlobals();
});

describe("two-factor setup", () => {
  it("asks for the password, shows the QR code and the codes, and turns on at I've saved", async () => {
    const server = off();
    await open(server);
    await expect
      .element(page.getByRole("heading", { level: 1, name: "Set up two-factor authentication" }))
      .toBeVisible();
    await expect.element(passwordField()).toHaveFocus();

    await userEvent.fill(passwordField(), "wrong password");
    await userEvent.click(page.getByRole("button", { name: "Continue" }));
    await expect.element(page.getByText("Enter your current password.")).toBeVisible();
    await expect.element(passwordField()).toHaveAttribute("aria-invalid", "true");

    await toQr();
    await expect.element(page.getByText("JBSW Y3DP EHPK 3PXP")).toBeVisible();
    await toCodes();
    await expect
      .element(page.getByRole("list", { name: "Backup codes" }))
      .toHaveTextContent(codes.join(""));

    await userEvent.click(page.getByRole("button", { name: "I've saved these codes" }));
    await expect
      .element(
        page.getByText("Two-factor authentication is on. Your other browsers are signed out."),
      )
      .toBeVisible();
    await expect.element(page.getByRole("heading", { level: 1, name: "Security" })).toBeVisible();
  });

  it("asks before leaving the codes step, and Stay keeps the codes", async () => {
    await open(off());
    await toQr();
    await toCodes();

    await userEvent.click(backLink());
    const dialog = page.getByRole("alertdialog", {
      name: "Leave before two-factor authentication is on?",
    });
    await expect.element(dialog).toBeVisible();
    await userEvent.click(dialog.getByRole("button", { name: "Stay" }));

    await expect.element(dialog).not.toBeInTheDocument();
    await expect.element(page.getByRole("list", { name: "Backup codes" })).toBeVisible();
  });

  it("leaves the QR step without asking", async () => {
    const server = off();
    const router = await open(server);
    await toQr();

    await userEvent.click(backLink());

    await expect.element(page.getByRole("heading", { level: 1, name: "Security" })).toBeVisible();
    expect(page.getByRole("alertdialog").query()).toBeNull();
    expect(router.state.location.pathname).toBe("/settings/security");
  });

  it("says an expired setup expired and starts again from the password", async () => {
    await open({
      totp: { status: "expired", backupCodesLeft: 0, backupCodesWaiting: false },
      sent: [],
    });

    await expect.element(page.getByText(/This setup expired after 15 minutes\./)).toBeVisible();
    await expect.element(passwordField()).toHaveFocus();
  });

  it("shows the codes again after the password when another tab showed them", async () => {
    const server = off();
    await open(server);
    await toQr();
    await toCodes();
    server.refuse = {
      "POST /api/v0/totp/enrollment/finish": { status: conflict, type: notWaitingType },
    };

    await userEvent.click(page.getByRole("button", { name: "I've saved these codes" }));

    await expect.element(passwordField()).toHaveFocus();
    await expect
      .element(page.getByText("Enter your password to see the backup codes you haven't saved yet."))
      .toBeVisible();
    await userEvent.fill(passwordField(), password);
    await userEvent.click(page.getByRole("button", { name: "Continue" }));
    await expect
      .element(page.getByRole("heading", { level: 2, name: "Save your backup codes" }))
      .toHaveFocus();
    await expect
      .element(page.getByRole("list", { name: "Backup codes" }))
      .toHaveTextContent(codes.join(""));
  });

  it("asks before canceling the setup, and Cancel setup returns to Security", async () => {
    const server = off();
    await open(server);
    await toQr();
    const dialog = page.getByRole("alertdialog", { name: "Cancel setup?" });

    await userEvent.click(page.getByRole("button", { name: "Cancel setup" }));
    await expect.element(dialog.getByRole("button", { name: "Keep setting up" })).toHaveFocus();
    await userEvent.click(dialog.getByRole("button", { name: "Keep setting up" }));
    await expect.element(dialog).not.toBeInTheDocument();
    await expect.element(page.getByRole("heading", { name: /Scan this QR code/ })).toBeVisible();
    expect(server.sent).not.toContain("DELETE /api/v0/totp/enrollment");

    await userEvent.click(page.getByRole("button", { name: "Cancel setup" }));
    await expect.element(dialog.getByRole("button", { name: "Keep setting up" })).toHaveFocus();
    // NOTE: the dialog's action ignores a pointer press just after it opens
    // (components/ui/alert-dialog.tsx); the keyboard is kept.
    await userEvent.keyboard("{Tab}");
    await expect.element(dialog.getByRole("button", { name: "Cancel setup" })).toHaveFocus();
    await userEvent.keyboard("{Enter}");

    await expect
      .element(
        page.getByText(
          "Setup canceled. Remove the inbok entry from your authenticator app if you added it.",
        ),
      )
      .toBeVisible();
    await expect.element(page.getByRole("heading", { level: 1, name: "Security" })).toBeVisible();
  });

  it("reports the change on Security once, not again after Back and Forward", async () => {
    const router = await open(off());
    await toQr();
    await toCodes();
    await userEvent.click(page.getByRole("button", { name: "I've saved these codes" }));
    await expect.element(page.getByText(onNotice)).toBeVisible();
    await vi.waitFor(() => expect(router.state.location.state.twoFactorChange).toBeUndefined());

    router.history.back();
    await expect.element(page.getByRole("heading", { level: 2, name: "Turn off" })).toBeVisible();
    router.history.forward();

    await expect.element(page.getByRole("heading", { level: 1, name: "Security" })).toBeVisible();
    await expect.element(page.getByText(/Firefox on Linux/)).toBeVisible();
    expect(page.getByText(onNotice).query()).toBeNull();
  });

  it("rereads the status when the start finds the codes already checked elsewhere", async () => {
    const server = off();
    server.refuse = {
      "POST /api/v0/totp": {
        status: conflict,
        type: "/api/v0/problems/totp-code-checked",
        before: () => {
          server.totp = { status: "pending", backupCodesLeft: 0, backupCodesWaiting: true };
        },
      },
    };
    await open(server);

    await userEvent.fill(passwordField(), password);
    await userEvent.click(page.getByRole("button", { name: "Continue" }));

    await expect
      .element(page.getByText("Enter your password to see the backup codes you haven't saved yet."))
      .toBeVisible();
  });

  it("rereads the status after a lost code check and shows the codes after the password", async () => {
    const server = off();
    server.lose = { "POST /api/v0/totp/enrollment/code": true };
    await open(server);
    await toQr();

    await userEvent.click(codeField());
    await userEvent.keyboard("123456");
    await userEvent.click(page.getByRole("button", { name: "Continue" }));

    await expect.element(passwordField()).toHaveFocus();
    await expect
      .element(page.getByText("Enter your password to see the backup codes you haven't saved yet."))
      .toBeVisible();
    await userEvent.fill(passwordField(), password);
    await userEvent.click(page.getByRole("button", { name: "Continue" }));
    await expect
      .element(page.getByRole("heading", { level: 2, name: "Save your backup codes" }))
      .toHaveFocus();
  });

  it("drops what the old session read once I've saved, so Security reads afresh", async () => {
    const server = off();
    await open(server, "member", "/settings/security");
    await expect
      .element(page.getByRole("button", { name: "Sign out Safari on iOS" }))
      .toBeVisible();
    await userEvent.click(page.getByRole("link", { name: "Set up two-factor authentication" }));
    await toQr();
    await toCodes();
    // Security's read after the finish stays unanswered, so what shows is
    // the cache, not a fresh read.
    server.holdSessions = new Promise((resolve) => {
      release = resolve;
    });

    await userEvent.click(page.getByRole("button", { name: "I've saved these codes" }));

    await expect.element(page.getByText(onNotice)).toBeVisible();
    await expect.element(page.getByText("Loading signed-in browsers...")).toBeVisible();
    expect(page.getByRole("button", { name: "Sign out Safari on iOS" }).query()).toBeNull();
  });

  it("sends no other request while I've saved replaces the cookie, even after leaving", async () => {
    const server = off();
    server.hold = new Promise((resolve) => {
      release = resolve;
    });
    await open(server);
    await toQr();
    await toCodes();

    await userEvent.click(page.getByRole("button", { name: "I've saved these codes" }));
    await expect.element(page.getByRole("button", { name: /Turning on/ })).toBeVisible();
    // The finish still runs, so leaving asks; the member leaves anyway.
    await userEvent.click(backLink());
    const dialog = page.getByRole("alertdialog", {
      name: "Leave before two-factor authentication is on?",
    });
    await expect.element(dialog).toBeVisible();
    // NOTE: a pointer press on Leave anyway soon after the dialog opens is
    // dropped and the leave never proceeds; the keyboard is not affected.
    await expect.element(dialog.getByRole("button", { name: "Stay" })).toHaveFocus();
    await userEvent.keyboard("{Tab}");
    await expect.element(dialog.getByRole("button", { name: "Leave anyway" })).toHaveFocus();
    await userEvent.keyboard("{Enter}");
    await expect.element(page.getByRole("heading", { level: 1, name: "Security" })).toBeVisible();
    const finishAt = server.sent.indexOf("POST /api/v0/totp/enrollment/finish");
    expect(server.sent.slice(finishAt)).toEqual(["POST /api/v0/totp/enrollment/finish"]);

    release?.();

    await expect.element(page.getByText(/Firefox on Linux/)).toBeVisible();
    expect(server.sent.slice(finishAt + 1)).toContain("GET /api/v0/sessions/current");
    await expect
      .element(page.getByRole("heading", { name: "Two-factor authentication" }))
      .toBeVisible();
    expect(page.getByText("Sign in").query()).toBeNull();
  });
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

    await expect
      .element(
        page.getByText(
          "Two-factor authentication is off. Remove the inbok entry from your authenticator app.",
        ),
      )
      .toBeVisible();
    await expect.element(page.getByRole("heading", { level: 1, name: "Security" })).toBeVisible();
  });

  it("reports a turn-off whose answer was lost once a status read finds it off", async () => {
    await open({ ...on(), loseTurnOff: true });

    await userEvent.click(page.getByRole("button", { name: "Turn off two-factor authentication" }));
    await userEvent.keyboard("123456");
    await userEvent.click(page.getByRole("button", { name: "Turn off two-factor authentication" }));

    await expect
      .element(
        page.getByText(
          "Two-factor authentication is off. Remove the inbok entry from your authenticator app.",
        ),
      )
      .toBeVisible();
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
