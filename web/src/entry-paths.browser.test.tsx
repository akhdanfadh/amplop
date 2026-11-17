import { describe, expect, it } from "vite-plus/test";
import { appRouter } from "@/test/app-router";
import { mailApi } from "@/test/mail-api";
import { fakeServer } from "@/test/server";
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

// signedIn returns a member's server that answers Connected accounts'
// loader: no account yet, Gmail offered.
function signedIn() {
  return mailApi([], []).server;
}

describe("entry addresses", () => {
  it.each(entryPaths)("%s opens a page, not Page not found", async (path) => {
    const router = await appRouter(path, path === "/sign-in" ? fakeServer() : signedIn());
    expect(router.state.matches.at(-1)?.routeId).not.toBe("__root__");
    expect(router.state.matches.every((match) => match.status === "success")).toBe(true);
  });

  it("opens Security from /security, in place of the entry address", async () => {
    const router = await appRouter("/security", signedIn());
    expect(router.state.location.pathname).toBe("/settings/security");
    expect(router.history.length).toBe(1);
  });

  it("opens Connected accounts from /connected-accounts with Google's outcome", async () => {
    const router = await appRouter("/connected-accounts?outcome=connected", signedIn());
    expect(router.state.location.pathname).toBe("/settings/connected-accounts");
    expect(router.state.location.search).toEqual({ outcome: "connected" });
    expect(router.history.length).toBe(1);
  });
});
