import { RouterProvider } from "@tanstack/react-router";
import { describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { getGetCurrentSessionQueryKey } from "@/api/generated/inbok";
import type { CurrentSession } from "@/api/generated/model";
import { appRouter } from "@/test/app-router";
import { held, memberSession, problem } from "@/test/server";
import {
  off,
  on,
  onNotice,
  password,
  passwordField,
  sent,
  toCodes,
  toQr,
  type TotpApi,
} from "./test-helpers";

// The tests of the setup card, /two-factor-setup, where an administrator
// whose session may only set up two-factor authentication does so, through
// the app's own routes.

const unauthorized = 401;
const fault = 500;

// A session read earlier, which still says two-factor must be set up.
const setupSession: CurrentSession = { ...memberSession, role: "admin", totpRequired: true };

const signIn = () => page.getByRole("heading", { name: "Sign in to inbok" });

// openCard renders the app at the setup card for api's member, an
// administrator, and returns the router. stale puts a session read
// earlier in the cache, one that still says two-factor must be set up.
async function openCard(api: TotpApi, stale: "stale-session" | "no-cache" = "no-cache") {
  const router = await appRouter("/two-factor-setup", api.server, (queryClient) => {
    if (stale === "stale-session")
      queryClient.setQueryData(getGetCurrentSessionQueryKey(), { data: setupSession });
  });
  await render(<RouterProvider router={router} />);
  return router;
}

describe("two-factor setup card", () => {
  it("sets up two-factor authentication on the card, then opens Security", async () => {
    const server = off("admin");
    const router = await openCard(server);
    await expect
      .element(page.getByRole("heading", { level: 1, name: /Set up two.factor authentication/ }))
      .toBeVisible();
    await toQr();
    await toCodes();

    await userEvent.click(page.getByRole("button", { name: "I've saved these codes" }));
    await expect.element(page.getByText(onNotice)).toBeVisible();
    expect(router.state.location.pathname).toBe("/settings/security");
  });

  it("names the account and links the QR code's address on a narrow page only", async () => {
    await page.viewport(390, 844);
    await openCard(off("admin"));
    await expect.element(page.getByText("ana@example.com")).toBeVisible();
    await toQr();
    // Hidden links leave the accessibility tree, so the locator includes them.
    const addToApp = page.getByRole("link", {
      name: "Add to authenticator app",
      includeHidden: true,
    });
    await expect.element(addToApp).toBeVisible();

    await page.viewport(1440, 900);
    await expect.element(addToApp).not.toBeVisible();
  });

  it("stays on the card after Cancel setup and says to remove the app's entry", async () => {
    const server = off("admin");
    const router = await openCard(server);
    await toQr();

    await userEvent.click(page.getByRole("button", { name: "Cancel setup" }));
    const dialog = page.getByRole("alertdialog", { name: "Cancel setup?" });
    await expect.element(dialog.getByRole("button", { name: "Keep setting up" })).toHaveFocus();
    // NOTE: the dialog's action ignores a pointer press just after it opens
    // (components/ui/alert-dialog.tsx); the keyboard is kept.
    await userEvent.keyboard("{Tab}");
    await userEvent.keyboard("{Enter}");

    await expect
      .element(
        page.getByText(
          "Setup canceled. Remove the inbok entry from your authenticator app if you added it.",
        ),
      )
      .toBeVisible();
    await expect.element(passwordField()).toBeVisible();
    expect(router.state.location.pathname).toBe("/two-factor-setup");
  });

  it("signs out from the card", async () => {
    const server = off("admin");
    const router = await openCard(server);
    await expect.element(passwordField()).toHaveFocus();

    await userEvent.click(page.getByRole("button", { name: "Sign out" }));

    await expect.element(signIn()).toBeVisible();
    expect(router.state.location.pathname).toBe("/sign-in");
    expect(sent(server)).toContain("DELETE /api/v0/sessions/current");
  });

  it("keeps focus on Sign out when it fails, so Enter tries again", async () => {
    const server = off("admin");
    server.server.once("DELETE", "/api/v0/sessions/current", () =>
      problem(fault, "/api/v0/problems/internal-error"),
    );
    await openCard(server);
    const signOut = page.getByRole("button", { name: "Sign out" });
    await expect.element(passwordField()).toHaveFocus();

    await userEvent.click(signOut);
    await expect.element(page.getByRole("alert")).toBeVisible();
    await expect.element(signOut).toHaveFocus();

    await userEvent.keyboard("{Enter}");
    await expect.element(signIn()).toBeVisible();
    expect(sent(server).filter((s) => s === "DELETE /api/v0/sessions/current")).toHaveLength(2);
  });

  it("opens sign-in when the card's session ended, e.g. after its 15 minutes", async () => {
    const server = off("admin");
    const router = await openCard(server);
    await expect.element(passwordField()).toHaveFocus();

    await userEvent.fill(passwordField(), password);
    // The session ends: the server answers the next request a 401.
    server.server.session = undefined;
    await userEvent.click(page.getByRole("button", { name: "Continue" }));

    await expect.element(signIn()).toBeVisible();
    expect(router.state.location.pathname).toBe("/sign-in");
  });

  it("opens sign-in when the card's status read finds the session ended", async () => {
    const server = off("admin");
    // The session ends after the guard read it, before the card's read.
    server.server.once("GET", "/api/v0/totp", () => {
      server.server.session = undefined;
      return problem(unauthorized, "/api/v0/problems/unauthenticated");
    });
    const router = await openCard(server);

    await expect.element(signIn()).toBeVisible();
    expect(router.state.location.pathname).toBe("/sign-in");
  });

  it("opens Security when two-factor was turned on in another tab", async () => {
    // Another tab finished: the cookie is now a full session's, but this
    // tab's cached session still says it may only set it up.
    const server = on("admin");
    const router = await openCard(server, "stale-session");

    await expect.element(page.getByText(onNotice)).toBeVisible();
    expect(router.state.location.pathname).toBe("/settings/security");
  });

  it("lets Sign out win over two-factor turned on in another tab", async () => {
    // The card sees it on and starts reading the account afresh, slowly;
    // Sign out pressed meanwhile must still end at sign-in, not Security.
    const server = on("admin");
    // Every status read after the first, and the sign-out, wait.
    const status = held();
    const signOut = held();
    const statusReads = () => sent(server).filter((s) => s === "GET /api/v0/totp").length;
    server.server.on("GET", "/api/v0/totp", async (_, earlier) => {
      if (statusReads() > 1) await status.promise;
      return earlier();
    });
    server.server.on("DELETE", "/api/v0/sessions/current", async (_, earlier) => {
      await signOut.promise;
      return earlier();
    });
    const router = await openCard(server, "stale-session");
    await expect.poll(() => statusReads()).toBe(2);
    const visited: string[] = [];
    router.subscribe("onResolved", ({ toLocation }) => visited.push(toLocation.pathname));

    await userEvent.click(page.getByRole("button", { name: "Sign out" }));
    await expect.poll(() => sent(server)).toContain("DELETE /api/v0/sessions/current");
    status.resolve();
    signOut.resolve();

    await expect.element(signIn()).toBeVisible();
    expect(router.state.location.pathname).toBe("/sign-in");
    expect(visited).not.toContain("/settings/security");
  });
});
