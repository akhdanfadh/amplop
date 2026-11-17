import { RouterProvider } from "@tanstack/react-router";
import { beforeEach, describe, expect, it } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import type { ListedSessions, TotpStatus } from "@/api/generated/model";
import { appRouter } from "@/test/app-router";
import { listedAccount, mailApi } from "@/test/mail-api";
import { data, fakeServer, memberSession } from "@/test/server";
import { forgetMailHref, mailHref, mailPage } from "./places";

const twoFactorOff: TotpStatus = { status: "off", backupCodesLeft: 0, backupCodesWaiting: false };

// The member's one signed-in browser, this one.
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

// pageOf returns what mailPage makes of the page router shows.
function pageOf(router: Awaited<ReturnType<typeof appRouter>>) {
  return mailPage(router, router.state.matches, router.state.location.pathname);
}

beforeEach(() => {
  forgetMailHref();
});

describe("mailPage", () => {
  it("is the place a loaded page in mail shows, with its search", async () => {
    const accounts = mailApi([], [listedAccount(2, "ana@example.com")]);
    const router = await appRouter("/mail/spam?account=2", accounts.server);
    expect(pageOf(router)).toEqual({
      to: "/mail/$place",
      params: { place: "spam" },
      search: { account: 2 },
    });
  });

  it("is a message in the Outbox", async () => {
    const id = "0193c8a0-0000-7000-8000-000000000000";
    const router = await appRouter(`/mail/outbox/${id}`, mailApi([], []).server);
    expect(pageOf(router)).toMatchObject({ to: "/mail/outbox/$id", params: { id } });
  });

  it("is nothing for a page outside mail, which the frame sees as it leaves", async () => {
    const router = await appRouter("/settings/security", fakeServer(memberSession));
    expect(pageOf(router)).toBeUndefined();
  });

  it.each([
    ["a place that does not exist", "/mail/bogus"],
    ["an address deeper than any page in a place", "/mail/inbox/nope"],
  ])("is nothing for %s", async (_, address) => {
    const router = await appRouter(address, mailApi([], []).server);
    expect(pageOf(router)).toBeUndefined();
  });

  it("is nothing for a page still loading", async () => {
    const router = await appRouter("/mail/spam", mailApi([], []).server);
    const loading = router.state.matches.map((match, index, all) =>
      index === all.length - 1 ? { ...match, status: "pending" as const } : match,
    );
    expect(mailPage(router, loading, router.state.location.pathname)).toBeUndefined();
  });
});

describe("MailFrame", () => {
  it("keeps the last loaded page in mail through a page not found and a visit to settings", async () => {
    const api = mailApi([], []);
    // What Security, settings' first page, reads.
    api.server.on("GET", "/api/v0/totp", () => data(twoFactorOff));
    api.server.on("GET", "/api/v0/sessions", () => data(browsers));
    const router = await appRouter("/mail/starred", api.server);
    await render(<RouterProvider router={router} />);
    await expect.element(page.getByRole("heading", { name: "Starred" })).toBeVisible();

    await router.navigate({ href: "/mail/spam" });
    await expect.element(page.getByRole("heading", { name: "Spam" })).toBeVisible();
    await router.navigate({ href: "/mail/inbox/nope" });
    await expect.element(page.getByRole("heading", { name: "Page not found" })).toBeVisible();
    await router.navigate({ href: "/settings" });
    await expect.element(page.getByRole("heading", { name: "Security" })).toBeVisible();

    expect(mailHref()).toEqual({ to: "/mail/$place", params: { place: "spam" }, search: {} });
  });
});
