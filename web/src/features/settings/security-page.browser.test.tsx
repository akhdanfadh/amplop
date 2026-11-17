import { afterEach, describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { cleanup, render } from "vitest-browser-react";
import { RouterProvider } from "@tanstack/react-router";
import { getGetCurrentSessionQueryKey, getGetTOTPStatusQueryKey } from "@/api/generated/inbok";
import type { ListedSessions, SentEmailVerification, TotpStatus } from "@/api/generated/model";
import { clearAccountCache } from "@/features/auth/session-cache";
import { appRouter } from "@/test/app-router";
import { data, fakeServer, held, memberSession, problem, type Server } from "@/test/server";

const fault = 500;

const twoFactorOff: TotpStatus = { status: "off", backupCodesLeft: 0, backupCodesWaiting: false };

const browsers: ListedSessions = {
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
};

const sentTo: SentEmailVerification = { address: "ana@example.com" };

// serverFault is a server fault, as the API words one.
const serverFault = () => problem(fault, "/api/v0/problems/internal-error");

let current: Awaited<ReturnType<typeof appRouter>> | undefined;

afterEach(async () => {
  await cleanup();
  if (current) clearAccountCache(current.options.context.queryClient);
  current = undefined;
});

// openSecurity renders the app at Security for a member whose address is
// not verified, with two-factor authentication off and one signed-in
// browser, and returns the server and the query client, to refresh a read
// as a background check would.
async function openSecurity() {
  const server: Server = fakeServer({ ...memberSession, emailVerified: false });
  server.on("GET", "/api/v0/totp", () => data(twoFactorOff));
  server.on("GET", "/api/v0/sessions", () => data(browsers));
  const router = await appRouter("/settings/security", server);
  current = router;
  await render(<RouterProvider router={router} />);
  await expect.element(page.getByText(/Firefox on Linux/)).toBeVisible();
  return { server, queryClient: router.options.context.queryClient };
}

describe("Security", () => {
  it("says a failed session refresh failed, keeps the sections, and recovers on Try again", async () => {
    const { server, queryClient } = await openSecurity();
    const signOut = page.getByRole("button", { name: "Sign out of this browser" });
    await expect.element(signOut).toBeEnabled();

    server.on("GET", "/api/v0/sessions/current", serverFault);
    await queryClient.refetchQueries({ queryKey: getGetCurrentSessionQueryKey() });

    await expect.element(page.getByText(/Could not check your session\./)).toBeVisible();
    await expect.element(page.getByRole("heading", { name: "Email address" })).toBeVisible();
    await expect.element(signOut).toBeDisabled();

    server.on("GET", "/api/v0/sessions/current", () => data(server.session));
    await userEvent.click(page.getByRole("button", { name: "Try again" }));

    await expect.element(page.getByText(/Could not check your session\./)).not.toBeInTheDocument();
    await expect.element(signOut).toBeEnabled();
  });

  it("says a failed two-factor refresh failed and keeps the row's last state", async () => {
    const { server, queryClient } = await openSecurity();
    await expect.element(page.getByText("Off. Signing in needs only your password.")).toBeVisible();

    server.on("GET", "/api/v0/totp", serverFault);
    await queryClient.refetchQueries({ queryKey: getGetTOTPStatusQueryKey() });

    await expect
      .element(page.getByText(/Could not check two-factor authentication\./))
      .toBeVisible();
    await expect.element(page.getByText("Off. Signing in needs only your password.")).toBeVisible();

    server.on("GET", "/api/v0/totp", () => data(twoFactorOff));
    await userEvent.click(page.getByRole("button", { name: "Try again" }));

    await expect
      .element(page.getByText(/Could not check two-factor authentication\./))
      .not.toBeInTheDocument();
  });

  it("keeps focus on Send verification link while it sends", async () => {
    const { server } = await openSecurity();
    const link = held();
    server.on("POST", "/api/v0/account/email-verification", async () => {
      await link.promise;
      return data(sentTo);
    });
    const send = page.getByRole("button", { name: "Send verification link" });
    await userEvent.click(send);

    const sending = page.getByRole("button", { name: "Sending verification link..." });
    await expect.element(sending).toHaveFocus();
    // Pressing again while it sends sends nothing more.
    await userEvent.keyboard("{Enter}");
    const sends = server.requests.filter(
      (request) => request.path === "/api/v0/account/email-verification",
    );
    expect(sends).toHaveLength(1);

    link.resolve();
    await expect
      .element(page.getByText("Check your inbox at ana@example.com. The link expires in 24 hours."))
      .toBeVisible();
    await expect.element(send).toHaveFocus();
  });
});
