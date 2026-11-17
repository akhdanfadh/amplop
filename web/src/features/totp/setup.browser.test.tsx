import { QueryObserver } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { appRouter } from "@/test/app-router";
import { held, lose, problem } from "@/test/server";
import {
  codeField,
  codes,
  off,
  onNotice,
  password,
  passwordField,
  sent,
  toCodes,
  toQr,
  totpApi,
  type TotpApi,
} from "./test-helpers";

// The tests of setting up two-factor authentication on its page under
// Security, through the app's own routes.

const conflict = 409;
const notWaitingType = "/api/v0/problems/backup-codes-not-waiting";

const backLink = () => page.getByRole("link", { name: "Back to Security" });

// Lets a held answer go. The transport holds every later request behind
// a held finish, in this test and the next, until it settles.
let release: (() => void) | undefined;

afterEach(() => {
  release?.();
  release = undefined;
});

// open renders the app at start, the two-factor page when left out, for
// api's member, and returns the router.
async function open(api: TotpApi, start = "/settings/security/two-factor") {
  const router = await appRouter(start, api.server);
  await render(<RouterProvider router={router} />);
  return router;
}

describe("two-factor setup", () => {
  it("links the QR code's address on a narrow page, not on a wide one with a mouse", async () => {
    await page.viewport(390, 844);
    await open(off());
    await toQr();
    // Hidden links leave the accessibility tree, so the locator includes them.
    const addToApp = page.getByRole("link", {
      name: "Add to authenticator app",
      includeHidden: true,
    });
    await expect.element(addToApp).toBeVisible();
    await expect.element(addToApp).toHaveAttribute("href", expect.stringMatching(/^otpauth:\/\//));

    await page.viewport(1440, 900);
    await expect.element(addToApp).not.toBeVisible();
  });

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
    await open(totpApi({ status: "expired", backupCodesLeft: 0, backupCodesWaiting: false }));

    await expect.element(page.getByText(/This setup expired after 15 minutes\./)).toBeVisible();
    await expect.element(passwordField()).toHaveFocus();
  });

  it("shows the codes again after the password when another tab showed them", async () => {
    const server = off();
    await open(server);
    await toQr();
    await toCodes();
    server.server.once("POST", "/api/v0/totp/enrollment/finish", () =>
      problem(conflict, notWaitingType),
    );

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
    expect(sent(server)).not.toContain("DELETE /api/v0/totp/enrollment");

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
    server.server.once("POST", "/api/v0/totp", () => {
      server.totp = { status: "pending", backupCodesLeft: 0, backupCodesWaiting: true };
      return problem(conflict, "/api/v0/problems/totp-code-checked");
    });
    await open(server);

    await userEvent.fill(passwordField(), password);
    await userEvent.click(page.getByRole("button", { name: "Continue" }));

    await expect
      .element(page.getByText("Enter your password to see the backup codes you haven't saved yet."))
      .toBeVisible();
  });

  it("rereads the status after a lost code check and shows the codes after the password", async () => {
    const server = off();
    // The server checks the code; its answer never arrives.
    server.server.once("POST", "/api/v0/totp/enrollment/code", async (_, earlier) => {
      await earlier();
      return lose();
    });
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
    await open(server, "/settings/security");
    await expect
      .element(page.getByRole("button", { name: "Sign out Safari on iOS" }))
      .toBeVisible();
    await userEvent.click(page.getByRole("link", { name: "Set up two-factor authentication" }));
    await toQr();
    await toCodes();
    // Security's read after the finish stays unanswered, so what shows is
    // the cache, not a fresh read.
    const sessions = held();
    release = sessions.resolve;
    server.server.on("GET", "/api/v0/sessions", async (_, earlier) => {
      await sessions.promise;
      return earlier();
    });

    await userEvent.click(page.getByRole("button", { name: "I've saved these codes" }));

    await expect.element(page.getByText(onNotice)).toBeVisible();
    await expect.element(page.getByText("Loading signed-in browsers...")).toBeVisible();
    expect(page.getByRole("button", { name: "Sign out Safari on iOS" }).query()).toBeNull();
  });

  it("keeps the member's other reads once I've saved, e.g. mail's", async () => {
    const server = off();
    const { queryClient } = (await open(server)).options.context;
    // A read outside the session that something watches, as mail's
    // collections watch theirs.
    let reads = 0;
    const mail = new QueryObserver(queryClient, {
      queryKey: ["/api/v0/conversations"],
      queryFn: () => {
        reads += 1;
        return reads;
      },
      staleTime: Infinity,
    });
    const stop = mail.subscribe(() => undefined);
    try {
      await expect.poll(() => reads).toBe(1);
      await toQr();
      await toCodes();

      await userEvent.click(page.getByRole("button", { name: "I've saved these codes" }));

      await expect.element(page.getByText(onNotice)).toBeVisible();
      await expect
        .poll(() => sent(server).filter((s) => s === "GET /api/v0/sessions").length)
        .toBeGreaterThan(0);
      expect(reads).toBe(1);
      // The session cache's rule, which this page does not show: the
      // answer is still held.
      expect(queryClient.getQueryData(["/api/v0/conversations"])).toBe(1);
    } finally {
      stop();
    }
  });

  it("sends no other request while I've saved replaces the cookie, even after leaving", async () => {
    const server = off();
    const finish = held();
    release = finish.resolve;
    server.server.on("POST", "/api/v0/totp/enrollment/finish", async (_, earlier) => {
      await finish.promise;
      return earlier();
    });
    const router = await open(server);
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
    const finishAt = sent(server).indexOf("POST /api/v0/totp/enrollment/finish");
    expect(sent(server).slice(finishAt)).toEqual(["POST /api/v0/totp/enrollment/finish"]);

    release?.();

    await expect.element(page.getByText(/Firefox on Linux/)).toBeVisible();
    expect(sent(server).slice(finishAt + 1)).toContain("GET /api/v0/sessions/current");
    await expect
      .element(page.getByRole("heading", { name: "Two-factor authentication" }))
      .toBeVisible();
    // Still in the app, not at sign-in.
    expect(router.state.location.pathname).toBe("/settings/security");
    expect(page.getByRole("heading", { name: "Sign in to inbok" }).query()).toBeNull();
  });
});
