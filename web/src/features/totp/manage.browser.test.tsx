import { RouterProvider } from "@tanstack/react-router";
import { describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { appRouter } from "@/test/app-router";
import { lose, problem } from "@/test/server";
import { codeField, on, totpApi, type TotpApi } from "./test-helpers";

// The tests of the two-factor page under Security while two-factor
// authentication is on: replacing the backup codes and turning it off,
// through the app's own routes.

const forbidden = 403;
const conflict = 409;
const notWaitingType = "/api/v0/problems/backup-codes-not-waiting";

const backLink = () => page.getByRole("link", { name: "Back to Security" });

// open renders the app at the two-factor page for api's member, and
// returns the router.
async function open(api: TotpApi) {
  const router = await appRouter("/settings/security/two-factor", api.server);
  await render(<RouterProvider router={router} />);
  return router;
}

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
    const server = on();
    // The server turns it off; its answer never arrives.
    server.server.on("POST", "/api/v0/totp/disable", async (_, earlier) => {
      await earlier();
      return lose();
    });
    await open(server);

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
    await open(on("admin"));

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
    await open(totpApi({ status: "on", backupCodesLeft: 10, backupCodesWaiting: true }));

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
    server.server.once("POST", "/api/v0/totp/backup-codes/finish", () =>
      problem(conflict, notWaitingType),
    );

    await userEvent.click(page.getByRole("button", { name: "I've saved these codes" }));

    await expect.element(page.getByRole("list", { name: "Backup codes" })).not.toBeInTheDocument();
    await expect
      .element(page.getByText(/New codes were made but not saved yet; your old codes still work\./))
      .toBeVisible();
  });

  it("opens sign-in when a write finds the session ended", async () => {
    const server = on();
    const router = await open(server);

    await userEvent.click(page.getByRole("button", { name: "Replace backup codes" }));
    await userEvent.keyboard("123456");
    // The session ends, e.g. signed out from another browser: the server
    // answers the write a 401.
    server.server.session = undefined;
    await userEvent.click(page.getByRole("button", { name: "Continue" }));

    await expect.element(page.getByText("Your session ended. Sign in to continue.")).toBeVisible();
    expect(router.state.location.pathname).toBe("/sign-in");
    expect(router.state.location.search).toEqual({ redirect: "/settings/security/two-factor" });
  });

  it("shows the administrator line after a refused turn-off rereads the role", async () => {
    const server = on();
    // The member was made an administrator since the page opened.
    server.server.once("POST", "/api/v0/totp/disable", () => {
      server.role = "admin";
      return problem(forbidden, "/api/v0/problems/totp-disable-forbidden");
    });
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
