import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import {
  type QueryClient,
  QueryObserver,
  focusManager,
  onlineManager,
} from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import {
  getGetCurrentSessionQueryKey,
  getGetSystemEmailQueryKey,
  getGetSystemEmailQueryOptions,
} from "@/api/generated/inbok";
import type { CurrentSession, SystemEmailSettings } from "@/api/generated/model";
import { Frame } from "@/components/frame";
import { appRouter } from "@/test/app-router";
import { testQueryClient } from "@/test/query-client";
import { type Server, data, fakeServer, held, memberSession, problem } from "@/test/server";
import { stubRouter } from "@/test/stub-router";
import { SetupReminder } from "./reminder";

const fault = 500;
const settings: SystemEmailSettings = {
  host: "smtp.example.com",
  port: 587,
  security: "starttls",
  username: "inbok",
  hasPassword: true,
  fromAddress: "inbok@example.com",
  fromName: "inbok",
  testedAt: null,
};

// What the fake server says about system email: nothing saved, saved
// without a passing test, tested, a fault, or no answer yet.
type SystemEmail = "none" | "untested" | "tested" | "fault" | "waiting";
let systemEmail: SystemEmail = "none";

// Opened is the part of the session a test chooses.
type Opened = Pick<CurrentSession, "role" | "emailVerified">;

// serve returns a server opened is signed in to, whose system email read
// answers as systemEmail says at the time of the read.
function serve(opened: Opened): Server {
  const server = fakeServer({ ...memberSession, ...opened });
  // Never resolved: a read that has not answered.
  const waiting = held();
  server.on("GET", "/api/v0/system-email", async () => {
    if (systemEmail === "waiting") await waiting.promise;
    if (systemEmail === "fault") return problem(fault, "/api/v0/problems/internal-error");
    if (systemEmail === "none") return data({});
    return data({
      settings: { ...settings, testedAt: systemEmail === "tested" ? "2026-11-15T10:00:00Z" : null },
    });
  });
  return server;
}

// open shows SetupReminder alone, or around "frame" the whole Frame around
// a page, at address for opened, whose session the query client already
// holds, as the route guard leaves it. It returns the query client and
// the server.
async function open(
  opened: Opened,
  { address = "/", around = "reminder" }: { address?: string; around?: "reminder" | "frame" } = {},
) {
  const server = serve(opened).install();
  const client = testQueryClient();
  client.setQueryData(getGetCurrentSessionQueryKey(), { data: server.session });
  await stubRouter(null, {
    at: address,
    pages: [
      "/",
      "/settings/system-email",
      "/settings/security",
      "/settings/security/two-factor",
      "/sign-in",
    ],
    layout: (shown) =>
      around === "frame" ? (
        <Frame label="Mail" nav={null}>
          {shown}
        </Frame>
      ) : (
        <>
          <SetupReminder />
          {shown}
        </>
      ),
    client,
  });
  await expect.element(page.getByRole("heading", { level: 1 })).toBeVisible();
  return { client, server };
}

// settled waits until the system email read has answered and React has
// drawn what it says.
async function settled(client: QueryClient): Promise<void> {
  await vi.waitFor(() =>
    expect(client.getQueryState(getGetSystemEmailQueryKey())?.status).not.toBe("pending"),
  );
  await new Promise((resolve) =>
    requestAnimationFrame(() => requestAnimationFrame(() => resolve(undefined))),
  );
}

// systemEmailReads counts the reads of the system email settings server
// got.
function systemEmailReads(server: Server): number {
  return server.requests.filter((asked) => asked.url.pathname === "/api/v0/system-email").length;
}

afterEach(() => {
  systemEmail = "none";
  focusManager.setFocused(undefined);
});

const reminders = () => page.getByRole("region", { name: "Setup reminders" });

describe("SetupReminder", () => {
  it("asks an administrator to set up system email when nothing is saved", async () => {
    await open({ role: "admin", emailVerified: true });

    await expect
      .element(reminders().getByText(/Finish setting up system email: until it works/))
      .toBeVisible();
    await expect
      .element(page.getByRole("link", { name: "Set up system email" }))
      .toHaveAttribute("href", "/settings/system-email");
  });

  it("asks an administrator for a test message when the saved settings have none", async () => {
    systemEmail = "untested";
    await open({ role: "admin", emailVerified: true });

    await expect
      .element(page.getByRole("link", { name: "Send a test message" }))
      .toHaveAttribute("href", "/settings/system-email");
  });

  it("stacks system email above an unverified address, each on its own", async () => {
    const { client } = await open({ role: "admin", emailVerified: false });
    await settled(client);

    const systemEmailStep = reminders().getByRole("link", { name: "Set up system email" });
    const verifyStep = reminders().getByRole("link", { name: "Verify it in Security" });
    await expect.element(systemEmailStep).toBeVisible();
    // The server refuses a link while nothing is saved, and says why.
    await expect.element(verifyStep).toHaveAttribute("href", "/settings/security");
    expect(
      systemEmailStep.element().compareDocumentPosition(verifyStep.element()) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it("shows nothing once every step is done", async () => {
    systemEmail = "tested";
    const { client } = await open({ role: "admin", emailVerified: true });

    await settled(client);
    expect(reminders().query()).toBeNull();
  });

  it("asks a member only to verify, without reading system email", async () => {
    const { server } = await open({ role: "member", emailVerified: false });

    await expect.element(page.getByRole("link", { name: "Verify it in Security" })).toBeVisible();
    expect(systemEmailReads(server)).toBe(0);
  });

  it("leaves system email out when its first read fails", async () => {
    systemEmail = "fault";
    const { client } = await open({ role: "admin", emailVerified: false });

    await settled(client);
    await expect.element(page.getByRole("link", { name: "Verify it in Security" })).toBeVisible();
    expect(page.getByText(/Finish setting up/).query()).toBeNull();
  });

  it("keeps the last answer when a later read fails, as the System email page does", async () => {
    systemEmail = "untested";
    const { client } = await open({ role: "admin", emailVerified: true });
    await expect.element(page.getByRole("link", { name: "Send a test message" })).toBeVisible();

    systemEmail = "fault";
    await client.invalidateQueries({ queryKey: getGetSystemEmailQueryKey() });

    expect(client.getQueryState(getGetSystemEmailQueryKey())?.status).toBe("error");
    await expect.element(page.getByRole("link", { name: "Send a test message" })).toBeVisible();
  });

  it("asks to verify while system email has not answered", async () => {
    systemEmail = "waiting";
    const { server } = await open({ role: "admin", emailVerified: false });

    await vi.waitFor(() => expect(systemEmailReads(server)).toBe(1));
    await expect.element(page.getByRole("link", { name: "Verify it in Security" })).toBeVisible();
    expect(page.getByText(/Finish setting up/).query()).toBeNull();
  });

  it("keeps its words but not its link on System email", async () => {
    const { client } = await open(
      { role: "admin", emailVerified: true },
      { address: "/settings/system-email" },
    );

    await settled(client);
    await expect
      .element(page.getByText(/Finish setting up system email: until it works/))
      .toBeVisible();
    expect(page.getByRole("link", { name: "Set up system email" }).query()).toBeNull();
  });

  it("keeps its words but not its link on Security", async () => {
    await open({ role: "member", emailVerified: false }, { address: "/settings/security" });

    await expect.element(page.getByText(/isn't verified/)).toBeVisible();
    expect(page.getByRole("link", { name: "Verify it in Security" }).query()).toBeNull();
  });

  it("links back to Security from its two-factor page", async () => {
    await open(
      { role: "member", emailVerified: false },
      { address: "/settings/security/two-factor" },
    );

    await expect
      .element(page.getByRole("link", { name: "Verify it in Security" }))
      .toHaveAttribute("href", "/settings/security");
  });

  it("drops the system email step once a test passes", async () => {
    systemEmail = "untested";
    const { client } = await open({ role: "admin", emailVerified: false });
    await expect.element(page.getByRole("link", { name: "Send a test message" })).toBeVisible();

    // As the test message form marks the settings out of date.
    systemEmail = "tested";
    await client.invalidateQueries({ queryKey: getGetSystemEmailQueryKey() });

    await expect
      .element(page.getByRole("link", { name: "Send a test message" }))
      .not.toBeInTheDocument();
    await expect.element(page.getByRole("link", { name: "Verify it in Security" })).toBeVisible();
  });

  it("drops the verify step on return from verifying in another tab", async () => {
    const { client, server } = await open({ role: "member", emailVerified: false });
    await expect.element(page.getByRole("link", { name: "Verify it in Security" })).toBeVisible();

    server.session = { ...memberSession, role: "member", emailVerified: true };
    focusManager.setFocused(false);
    focusManager.setFocused(true);

    await expect
      .element(page.getByRole("link", { name: "Verify it in Security" }))
      .not.toBeInTheDocument();
    // Resetting the focus after the test must not reread with the real fetch.
    client.clear();
  });

  it("sits under the offline banner in the frame, which is still announced", async () => {
    onlineManager.setOnline(false);
    await open({ role: "member", emailVerified: false }, { around: "frame" });

    const offline = page.getByRole("status");
    await expect
      .element(offline)
      .toHaveTextContent(
        "You're offline. inbok shows what it already loaded and catches up when the connection returns.",
      );
    await expect.element(reminders()).toBeVisible();
    // Found again as a landmark of its own while it shows.
    await expect.element(page.getByRole("region", { name: "Offline" })).toBeVisible();
    expect(
      offline.element().compareDocumentPosition(reminders().element()) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });
});

// loadApp loads the app at the Security page, which reads nothing in its
// loader, for opened, and returns the router, its query client, and the
// server.
async function loadApp(opened: Opened) {
  const server = serve(opened);
  const router = await appRouter("/settings/security", server);
  return { router, client: router.options.context.queryClient, server };
}

// loadedAgain loads router's page again, as a visit to another signed-in
// page runs the signed-in loader again, and returns "loaded" as soon as
// that finishes, or "waiting" when the retried look gives up on it.
function loadedAgain(router: Awaited<ReturnType<typeof appRouter>>): Promise<string> {
  let loaded = false;
  void router.invalidate().then(() => {
    loaded = true;
  });
  return vi
    .waitFor(() => expect(loaded).toBe(true))
    .then(
      () => "loaded",
      () => "waiting",
    );
}

describe("the signed-in app's loader", () => {
  it("reads system email for an administrator before the page draws", async () => {
    const { client } = await loadApp({ role: "admin", emailVerified: true });

    // No screen shows this: the frame draws a moment after it is rendered,
    // so a look at the page cannot tell an answer the load waited for from
    // one that came just after. The cache at the load's end can.
    expect(client.getQueryState(getGetSystemEmailQueryKey())?.status).toBe("success");
  });

  it("reads no system email for a member", async () => {
    const { server } = await loadApp({ role: "member", emailVerified: false });

    expect(systemEmailReads(server)).toBe(0);
  });

  it("draws the page when the read fails", async () => {
    systemEmail = "fault";
    const { router, server } = await loadApp({ role: "admin", emailVerified: true });

    // The server answered the one read with its fault.
    expect(systemEmailReads(server)).toBe(1);
    // What the Security page reads once it draws.
    server.on("GET", "/api/v0/totp", () =>
      data({ status: "off", backupCodesLeft: 0, backupCodesWaiting: false }),
    );
    server.on("GET", "/api/v0/sessions", () => data({ sessions: [] }));
    await render(<RouterProvider router={router} />);
    await expect.element(page.getByRole("heading", { level: 1, name: "Security" })).toBeVisible();
    expect(page.getByText(/Finish setting up/).query()).toBeNull();
  });

  it("does not wait for a retry paused after a failed first read", async () => {
    // The first read, the loader's, failed, so nothing is cached; the
    // reminder retries offline, and its retry waits for the connection.
    systemEmail = "fault";
    const { client, router } = await loadApp({ role: "admin", emailVerified: true });
    // No screen shows a read that failed and left nothing: the reminder is
    // just absent. This test and the next are about the query's own state.
    expect(client.getQueryState(getGetSystemEmailQueryKey())?.status).toBe("error");
    systemEmail = "none";
    onlineManager.setOnline(false);
    const reminder = new QueryObserver(client, getGetSystemEmailQueryOptions());
    const stop = reminder.subscribe(() => {});
    await vi.waitFor(() =>
      expect(client.getQueryState(getGetSystemEmailQueryKey())?.fetchStatus).toBe("paused"),
    );

    const loaded = await loadedAgain(router);

    // The retry must not resume once the test has put fetch back.
    stop();
    await client.cancelQueries({ queryKey: getGetSystemEmailQueryKey() });
    expect(loaded).toBe("loaded");
  });

  it("does not wait for a reread paused until the connection returns", async () => {
    // Read once, by the loader, then marked out of date offline while the
    // reminder watches it, as after a save: its reread waits for the
    // connection.
    const { client, router } = await loadApp({ role: "admin", emailVerified: true });
    const reminder = new QueryObserver(client, getGetSystemEmailQueryOptions());
    const stop = reminder.subscribe(() => {});
    onlineManager.setOnline(false);
    void client.invalidateQueries({ queryKey: getGetSystemEmailQueryKey() });
    await vi.waitFor(() =>
      expect(client.getQueryState(getGetSystemEmailQueryKey())?.fetchStatus).toBe("paused"),
    );

    const loaded = await loadedAgain(router);

    // The reread must not resume once the test has put fetch back.
    stop();
    await client.cancelQueries({ queryKey: getGetSystemEmailQueryKey() });
    expect(loaded).toBe("loaded");
  });
});
