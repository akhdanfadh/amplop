import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { appRouter, memberSession } from "@/test/app-router";

// An administrator without two-factor authentication, whose session the
// server lets only set it up.
const setupSession = { ...memberSession, role: "admin", totpRequired: true };

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("two-factor setup guards", () => {
  it.each(["/", "/mail/inbox", "/settings/security", "/settings/members", "/security"])(
    "sends a setup-only session from %s to the setup card",
    async (path) => {
      const router = await appRouter(path, setupSession);
      expect(router.state.location.pathname).toBe("/two-factor-setup");
    },
  );

  it("opens the setup card for a setup-only session", async () => {
    const router = await appRouter("/two-factor-setup", setupSession);
    expect(router.state.location.pathname).toBe("/two-factor-setup");
    expect(router.state.matches.every((match) => match.status === "success")).toBe(true);
  });

  it("sends a visitor without a session from the setup card to sign-in", async () => {
    const router = await appRouter("/two-factor-setup");
    expect(router.state.location.pathname).toBe("/sign-in");
  });

  it("sends a full session from the setup card into the app", async () => {
    const router = await appRouter("/two-factor-setup", memberSession);
    expect(router.state.location.pathname).toBe("/mail/inbox");
  });

  it("opens the two-factor page under Security", async () => {
    const router = await appRouter("/settings/security/two-factor", memberSession);
    expect(router.state.location.pathname).toBe("/settings/security/two-factor");
    expect(router.state.matches.every((match) => match.status === "success")).toBe(true);
  });
});
