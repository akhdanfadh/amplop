import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import {
  QueryClient,
  QueryClientProvider,
  QueryObserver,
  focusManager,
  onlineManager,
} from "@tanstack/react-query";
import {
  Outlet,
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from "@tanstack/react-router";
import { routeTree } from "@/routeTree.gen";
import {
  getGetCurrentSessionQueryKey,
  getGetSystemEmailQueryKey,
  getGetSystemEmailQueryOptions,
} from "@/api/generated/inbok";
import type { CurrentSession } from "@/api/generated/model";
import { retryOptions } from "@/api/retry";
import { Frame } from "@/components/frame";
import { SetupReminder } from "./reminder";
import { ComposeStore } from "@/features/outgoing-messages/compose-store";

const fault = 500;
const settings = {
  host: "smtp.example.com",
  port: 587,
  security: "starttls",
  username: "inbok",
  hasPassword: true,
  fromAddress: "inbok@example.com",
  fromName: "inbok",
};

// What the fake server says about system email: nothing saved, saved
// without a passing test, tested, a fault, or no answer yet.
type SystemEmail = "none" | "untested" | "tested" | "fault" | "waiting";
let systemEmail: SystemEmail = "none";

// What the fake server says about the session: the one the test opened
// with, its address verified since when verifiedSince is set.
let session: Pick<CurrentSession, "role" | "emailVerified"> = {
  role: "member",
  emailVerified: true,
};
let verifiedSince = false;
// Set when the session has ended on the server since.
let ended = false;

function answer(path: string): Response | Promise<Response> {
  if (path.endsWith("/sessions/current") && ended)
    return Response.json(
      { type: "/api/v0/problems/unauthorized", status: 401, title: "Unauthorized", detail: "" },
      { status: 401, headers: { "Content-Type": "application/problem+json" } },
    );
  if (path.endsWith("/sessions/current"))
    return Response.json({
      data: {
        email: "ana@example.com",
        totpRequired: false,
        ...session,
        emailVerified: session.emailVerified || verifiedSince,
      },
    });
  if (path.endsWith("/setup/status")) return Response.json({ data: { status: "done" } });
  if (!path.endsWith("/system-email")) return Response.json({}, { status: fault });
  if (systemEmail === "waiting") return new Promise(() => {});
  if (systemEmail === "fault")
    return Response.json(
      {
        type: "/api/v0/problems/internal-error",
        status: fault,
        title: "Internal Server Error",
        detail: "Something went wrong on our side.",
      },
      { status: fault, headers: { "Content-Type": "application/problem+json" } },
    );
  if (systemEmail === "none") return Response.json({ data: {} });
  return Response.json({
    data: {
      settings: {
        ...settings,
        testedAt: systemEmail === "tested" ? "2026-11-15T10:00:00Z" : null,
      },
    },
  });
}

// stub answers every request through answer and returns the fake fetch.
function stub() {
  const fetch = vi.fn<(input: RequestInfo | URL) => Promise<Response>>(async (input) =>
    answer(new URL(input instanceof Request ? input.url : input, location.href).pathname),
  );
  vi.stubGlobal("fetch", fetch);
  return fetch;
}

// signedIn returns a query client that holds opened as the session, as
// the route guard leaves it.
function signedIn(opened: typeof session): QueryClient {
  session = opened;
  const client = new QueryClient({ defaultOptions: retryOptions });
  client.setQueryData(getGetCurrentSessionQueryKey(), {
    data: { email: "ana@example.com", totpRequired: false, ...opened },
  });
  return client;
}

// open shows SetupReminder alone, or around "frame" the whole Frame around
// a page, at address for opened, with the server answering through answer.
// It returns the query client and the fake fetch.
async function open(
  opened: typeof session,
  { address = "/", around = "reminder" }: { address?: string; around?: "reminder" | "frame" } = {},
) {
  const fetch = stub();
  const client = signedIn(opened);
  const root = createRootRoute({
    component: () =>
      around === "frame" ? (
        <Frame label="Mail" nav={null}>
          <Outlet />
        </Frame>
      ) : (
        <>
          <SetupReminder />
          <Outlet />
        </>
      ),
  });
  const pages = [
    "/",
    "/settings/system-email",
    "/settings/security",
    "/settings/security/two-factor",
    "/sign-in",
  ].map((path) =>
    createRoute({ getParentRoute: () => root, path, component: () => <h1>{path}</h1> }),
  );
  const router = createRouter({
    routeTree: root.addChildren(pages),
    history: createMemoryHistory({ initialEntries: [address] }),
  });
  await render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  await expect.element(page.getByRole("heading", { level: 1 })).toBeVisible();
  return { client, fetch };
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

// systemEmailReads counts the reads of the system email settings.
function systemEmailReads(fetch: ReturnType<typeof vi.fn>): number {
  return fetch.mock.calls.filter(([input]) =>
    (input instanceof Request ? input.url : String(input)).endsWith("/system-email"),
  ).length;
}

afterEach(() => {
  systemEmail = "none";
  verifiedSince = false;
  ended = false;
  onlineManager.setOnline(true);
  focusManager.setFocused(undefined);
  vi.unstubAllGlobals();
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
    const { fetch } = await open({ role: "member", emailVerified: false });

    await expect.element(page.getByRole("link", { name: "Verify it in Security" })).toBeVisible();
    expect(systemEmailReads(fetch)).toBe(0);
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
    const { fetch } = await open({ role: "admin", emailVerified: false });

    await vi.waitFor(() => expect(systemEmailReads(fetch)).toBe(1));
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
    const { client } = await open({ role: "member", emailVerified: false });
    await expect.element(page.getByRole("link", { name: "Verify it in Security" })).toBeVisible();

    verifiedSince = true;
    focusManager.setFocused(false);
    focusManager.setFocused(true);

    await expect
      .element(page.getByRole("link", { name: "Verify it in Security" }))
      .not.toBeInTheDocument();
    // Resetting the focus after the test must not reread with the real fetch.
    client.clear();
  });

  it("opens sign-in when the focus reread finds the session ended", async () => {
    await open({ role: "member", emailVerified: false });
    await expect.element(page.getByRole("link", { name: "Verify it in Security" })).toBeVisible();

    ended = true;
    focusManager.setFocused(false);
    focusManager.setFocused(true);

    await expect.element(page.getByRole("heading", { name: "/sign-in" })).toBeVisible();
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
    expect(
      offline.element().compareDocumentPosition(reminders().element()) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });
});

// loadApp loads the app's own routes at the Security page, which reads
// nothing in its loader, for client's session, with the server answering
// through answer, and returns the router and the fake fetch.
async function loadApp(client: QueryClient) {
  const fetch = stub();
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: ["/settings/security"] }),
    context: { queryClient: client, compose: new ComposeStore() },
  });
  await router.load();
  return { fetch, router };
}

describe("the signed-in app's loader", () => {
  it("reads system email for an administrator before the page draws", async () => {
    const client = signedIn({ role: "admin", emailVerified: true });
    await loadApp(client);

    expect(client.getQueryState(getGetSystemEmailQueryKey())?.status).toBe("success");
  });

  it("reads no system email for a member", async () => {
    const { fetch } = await loadApp(signedIn({ role: "member", emailVerified: false }));

    expect(systemEmailReads(fetch)).toBe(0);
  });

  it("draws the page when the read fails", async () => {
    systemEmail = "fault";
    const client = signedIn({ role: "admin", emailVerified: true });
    const { router } = await loadApp(client);

    expect(client.getQueryState(getGetSystemEmailQueryKey())?.status).toBe("error");
    expect(router.state.matches.find((match) => match.routeId === "/_app")?.status).toBe("success");
  });

  it("does not wait for a retry paused after a failed first read", async () => {
    const client = signedIn({ role: "admin", emailVerified: true });
    stub();
    // The first read failed, so nothing is cached; the reminder retries
    // offline, and its retry waits for the connection.
    systemEmail = "fault";
    await client.query(getGetSystemEmailQueryOptions()).catch(() => undefined);
    systemEmail = "none";
    onlineManager.setOnline(false);
    const reminder = new QueryObserver(client, getGetSystemEmailQueryOptions());
    const stop = reminder.subscribe(() => {});
    await vi.waitFor(() =>
      expect(client.getQueryState(getGetSystemEmailQueryKey())?.fetchStatus).toBe("paused"),
    );

    const loaded = await Promise.race([
      loadApp(client).then(() => "loaded"),
      new Promise((resolve) => setTimeout(() => resolve("waiting"), 2000)),
    ]);

    // The retry must not resume once the test has put fetch back.
    stop();
    await client.cancelQueries({ queryKey: getGetSystemEmailQueryKey() });
    expect(loaded).toBe("loaded");
  });

  it("does not wait for a reread paused until the connection returns", async () => {
    const client = signedIn({ role: "admin", emailVerified: true });
    stub();
    // Read once, then marked out of date offline while the reminder watches
    // it, as after a save: its reread waits for the connection.
    await client.query(getGetSystemEmailQueryOptions());
    const reminder = new QueryObserver(client, getGetSystemEmailQueryOptions());
    const stop = reminder.subscribe(() => {});
    onlineManager.setOnline(false);
    void client.invalidateQueries({ queryKey: getGetSystemEmailQueryKey() });
    await vi.waitFor(() =>
      expect(client.getQueryState(getGetSystemEmailQueryKey())?.fetchStatus).toBe("paused"),
    );

    const loaded = await Promise.race([
      loadApp(client).then(() => "loaded"),
      new Promise((resolve) => setTimeout(() => resolve("waiting"), 2000)),
    ]);

    // The reread must not resume once the test has put fetch back.
    stop();
    await client.cancelQueries({ queryKey: getGetSystemEmailQueryKey() });
    expect(loaded).toBe("loaded");
  });
});
