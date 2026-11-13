import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { appRouter, memberSession } from "@/test/app-router";
import type { FileRouteTypes } from "./routeTree.gen";

// The addresses the server writes into emails and redirects (AGENTS.md
// "API contract"): each stays a page however the app's pages move, and
// TypeScript fails here when one is no longer a route.
const entryPaths = [
  "/sign-in",
  "/verify-email",
  "/reset-password",
  "/register/complete",
  "/security",
  "/connected-accounts",
] as const satisfies readonly FileRouteTypes["fullPaths"][];

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("entry addresses", () => {
  it.each(entryPaths)("%s opens a page, not Page not found", async (path) => {
    const router = await appRouter(path, path === "/sign-in" ? undefined : memberSession);
    expect(router.state.matches.at(-1)?.routeId).not.toBe("__root__");
    expect(router.state.matches.every((match) => match.status === "success")).toBe(true);
  });

  it("opens Security from /security, in place of the entry address", async () => {
    const router = await appRouter("/security", memberSession);
    expect(router.state.location.pathname).toBe("/settings/security");
    expect(router.history.length).toBe(1);
  });

  it("opens Connected accounts from /connected-accounts with Google's outcome", async () => {
    const router = await appRouter("/connected-accounts?outcome=connected", memberSession);
    expect(router.state.location.pathname).toBe("/settings/connected-accounts");
    expect(router.state.location.search).toEqual({ outcome: "connected" });
    expect(router.history.length).toBe(1);
  });
});
