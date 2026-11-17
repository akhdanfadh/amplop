import { RouterProvider } from "@tanstack/react-router";
import { describe, expect, it } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import type { CurrentSession, ListedMembers, RegistrationSettings } from "@/api/generated/model";
import { appRouter } from "@/test/app-router";
import { adminSession, data, fakeServer, memberSession } from "@/test/server";

// The tests of the route table's guards (web/README.md "Routes"), through
// the app's own routes.

// An administrator without two-factor authentication, whose session the
// server lets only set it up.
const setupSession: CurrentSession = { ...memberSession, role: "admin", totpRequired: true };

const members: ListedMembers = {
  members: [
    {
      allowedActions: [],
      deactivatedAt: null,
      email: "ana@example.com",
      emailVerified: true,
      id: 1,
      joinedAt: "2026-01-01T00:00:00Z",
      role: "admin",
      totpEnabled: true,
    },
  ],
};

const registration: RegistrationSettings = {
  allowedActions: ["open", "close"],
  status: "closed",
  systemEmailTested: true,
};

// adminServer returns a server for session that answers what the
// administrator's pages read, with nothing to report.
function adminServer(session: CurrentSession) {
  const server = fakeServer(session);
  server.on("GET", "/api/v0/members", () => data(members));
  server.on("GET", "/api/v0/registration/settings", () => data(registration));
  return server;
}

describe("two-factor setup guards", () => {
  it.each(["/", "/mail/inbox", "/settings/security", "/settings/members", "/security"])(
    "sends a setup-only session from %s to the setup card",
    async (path) => {
      const router = await appRouter(path, fakeServer(setupSession));
      expect(router.state.location.pathname).toBe("/two-factor-setup");
    },
  );

  it("opens the setup card for a setup-only session", async () => {
    const router = await appRouter("/two-factor-setup", fakeServer(setupSession));
    expect(router.state.location.pathname).toBe("/two-factor-setup");
    expect(router.state.matches.every((match) => match.status === "success")).toBe(true);
  });

  it("sends a visitor without a session from the setup card to sign-in", async () => {
    const router = await appRouter("/two-factor-setup");
    expect(router.state.location.pathname).toBe("/sign-in");
  });

  it("sends a full session from the setup card into the app", async () => {
    const router = await appRouter("/two-factor-setup", fakeServer(memberSession));
    expect(router.state.location.pathname).toBe("/mail/inbox");
  });

  it("opens the two-factor page under Security", async () => {
    const router = await appRouter("/settings/security/two-factor", fakeServer(memberSession));
    expect(router.state.location.pathname).toBe("/settings/security/two-factor");
    expect(router.state.matches.every((match) => match.status === "success")).toBe(true);
  });
});

describe("the administrator guard", () => {
  const pages = [
    ["/settings/members", "Members"],
    ["/settings/registration", "Registration"],
    ["/settings/system-email", "System email"],
  ];

  it.each(pages)("shows a member Page not found at %s", async (address, title) => {
    const server = adminServer(memberSession);
    const router = await appRouter(address, server);
    await render(<RouterProvider router={router} />);

    await expect
      .element(page.getByRole("heading", { level: 1, name: "Page not found" }))
      .toBeVisible();
    expect(router.state.location.pathname).toBe(address);
    expect(page.getByRole("heading", { level: 1, name: title, exact: true }).query()).toBeNull();
    // The guard refuses before the page's loader reads anything.
    const read = server.requests.map((asked) => asked.url.pathname);
    expect(read).not.toContain("/api/v0/members");
    expect(read).not.toContain("/api/v0/registration/settings");
    expect(read).not.toContain("/api/v0/system-email");
  });

  it.each(pages)("opens %s for an administrator", async (address, title) => {
    const router = await appRouter(address, adminServer(adminSession));
    await render(<RouterProvider router={router} />);

    await expect
      .element(page.getByRole("heading", { level: 1, name: title, exact: true }))
      .toBeVisible();
    expect(router.state.location.pathname).toBe(address);
  });
});
