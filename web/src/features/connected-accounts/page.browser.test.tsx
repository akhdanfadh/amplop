import { describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { RouterProvider } from "@tanstack/react-router";
import type {
  AccountProviders,
  ConnectedAccounts,
  CurrentSession,
  ListedAccount,
  ListedSessions,
  TotpStatus,
} from "@/api/generated/model";
import { appRouter } from "@/test/app-router";
import { fakeClock } from "@/test/clock";
import { listedAccount } from "@/test/mail-api";
import {
  adminSession,
  data,
  fakeServer,
  held,
  lose,
  memberSession,
  problem,
  type Asked,
  type Server,
} from "@/test/server";
import { idleRefresh, syncNowRefresh } from "./sync";

const accepted = 202;
const unauthorized = 401;
const notFound = 404;
const tooManyRequests = 429;
const fault = 500;
const listPath = "/api/v0/connected-accounts";

// What Security, another signed-in page, reads.
const twoFactorOff: TotpStatus = { status: "off", backupCodesLeft: 0, backupCodesWaiting: false };
const browsers: ListedSessions = { sessions: [] };

// Api is the fake server and what it holds: accounts as the list shows
// them, and syncStopped, whether syncing has stopped on this inbok. A test
// changes either as another tab or the server would.
type Api = { server: Server; accounts: ListedAccount[]; syncStopped: boolean };

// api returns a fake server that holds accounts for session, a member's
// when left out, and offers Gmail unless offered is false. A Check now
// lands at once, and a disconnect removes its account.
function api(
  accounts: ListedAccount[],
  { offered = true, session = memberSession }: { offered?: boolean; session?: CurrentSession } = {},
): Api {
  const server = fakeServer(session);
  const state: Api = { server, accounts, syncStopped: false };
  const providers: AccountProviders = { offered: offered ? ["gmail"] : [] };
  server.on("GET", "/api/v0/connected-accounts", () => {
    const list: ConnectedAccounts = { accounts: state.accounts, syncStopped: state.syncStopped };
    return data(list);
  });
  server.on("GET", "/api/v0/connected-accounts/providers", () => data(providers));
  server.on("POST", "/api/v0/connected-accounts/{id}/sync", ({ params }) => {
    const id = Number(params.id);
    const before = state.accounts.find((a) => a.id === id);
    // The check lands at once, a minute after the last: the next read
    // shows it.
    const checks = server.requests.filter((asked) => asked.path.endsWith("/sync")).length - 1;
    const checkedAt = new Date(Date.UTC(2026, 10, 16, 9, checks)).toISOString();
    state.accounts = state.accounts.map((a) =>
      a.id === id ? { ...a, sync: { ...a.sync, lastSyncedAt: checkedAt } } : a,
    );
    return data(before?.sync, accepted);
  });
  server.on("DELETE", "/api/v0/connected-accounts/{id}", ({ params }) => {
    const id = Number(params.id);
    const removed = state.accounts.find((a) => a.id === id);
    state.accounts = state.accounts.filter((a) => a.id !== id);
    return removed ? data({ account: removed }) : accountGone();
  });
  server.on("GET", "/api/v0/totp", () => data(twoFactorOff));
  server.on("GET", "/api/v0/sessions", () => data(browsers));
  return state;
}

// accountGone says the account does not exist, as the API does.
const accountGone = () =>
  problem(notFound, "/api/v0/problems/connected-account-not-found", {
    detail: "The account does not exist.",
  });

// serverFault is a server fault, as the API words one.
const serverFault = () =>
  problem(fault, "/api/v0/problems/internal-error", {
    detail: "Something went wrong on our side.",
  });

// refuse answers with status: an ended session's 401, or else a fault.
const refuse = (status: number) =>
  status === unauthorized
    ? problem(unauthorized, "/api/v0/problems/unauthenticated")
    : serverFault();

// sent lists requests as e.g. "GET /api/v0/connected-accounts".
const sent = (requests: Asked[]) =>
  requests.map((request) => `${request.method} ${request.url.pathname}`);

// holdReads holds the list's reads on server from now until the returned
// function is called.
function holdReads(server: Server): () => void {
  const reads = held();
  server.on("GET", "/api/v0/connected-accounts", async (_, earlier) => {
    await reads.promise;
    return earlier();
  });
  return reads.resolve;
}

// open renders the app at path, Connected accounts when left out, its
// requests answered by server. It waits for Connected accounts' heading
// unless loaded is false, and returns the router and its query client.
async function open(server: Server, path = "/settings/connected-accounts", loaded = true) {
  const router = await appRouter(path, server);
  await render(<RouterProvider router={router} />);
  const result = { router, client: router.options.context.queryClient };
  if (!loaded) return result;
  await expect
    .element(page.getByRole("heading", { level: 1, name: "Connected accounts" }))
    .toBeVisible();
  return result;
}

const work = { ...listedAccount(1, "ana@work.example"), name: "Work" };
const home = listedAccount(2, "ana@home.example");
const refused: ListedAccount = {
  ...listedAccount(3, "ana@old.example"),
  status: "needs-reconnect",
  needsReconnectSince: "2026-11-10T09:00:00Z",
  allowedActions: ["reconnect", "rename", "disconnect"],
};

describe("Connected accounts", () => {
  it("offers each account the actions the server allows it", async () => {
    await open(api([work, refused]).server);

    await expect
      .element(page.getByRole("button", { name: "Check now ana@work.example" }))
      .toBeVisible();
    await expect
      .element(page.getByRole("button", { name: "Reconnect ana@old.example" }))
      .toBeVisible();
    expect(page.getByRole("button", { name: "Check now ana@old.example" }).elements()).toHaveLength(
      0,
    );
    await expect.element(page.getByText("Needs reconnecting")).toBeVisible();
    await expect.element(page.getByRole("button", { name: "Connect Gmail" })).toBeVisible();
  });

  it("says why nothing can be connected while Gmail isn't offered", async () => {
    const unoffered: ListedAccount = { ...work, allowedActions: ["rename", "disconnect"] };
    await open(api([unoffered], { offered: false }).server);

    await expect
      .element(page.getByText(/^Your administrator hasn't set up Gmail on this inbok/))
      .toBeVisible();
    expect(page.getByRole("button", { name: "Connect Gmail" }).elements()).toHaveLength(0);
    expect(page.getByRole("button", { name: /^Check now/ }).elements()).toHaveLength(0);
  });

  it("tells an administrator where Gmail is set up when nothing can be connected", async () => {
    await open(api([], { offered: false, session: adminSession }).server);

    await expect
      .element(page.getByRole("heading", { name: "Gmail can't be connected yet" }))
      .toBeVisible();
    await expect
      .element(page.getByText(/^Add a Google client to this inbok, as the self-hosting guide/))
      .toBeVisible();
    expect(page.getByRole("button", { name: "Connect Gmail" }).elements()).toHaveLength(0);
  });

  it("says how to start when no account is connected", async () => {
    await open(api([]).server);

    await expect
      .element(page.getByRole("heading", { name: "Connect your first account" }))
      .toBeVisible();
    await expect.element(page.getByRole("button", { name: "Connect Gmail" })).toBeVisible();
  });

  it("keeps Check now turning until the list shows the check", async () => {
    const state = api([home]);
    const { client } = await open(state.server);
    const release = holdReads(state.server);

    await userEvent.click(page.getByRole("button", { name: "Check now ana@home.example" }));

    await expect
      .element(page.getByRole("button", { name: "Checking... ana@home.example" }))
      .toBeVisible();
    release();
    await expect
      .element(page.getByRole("button", { name: "Check now ana@home.example" }))
      .toBeVisible();
    // A screen reader hears the outcome, not only the button's name.
    const said = page.getByRole("status").filter({ hasText: "ana@home.example: Up to date" });
    await expect.element(said).toBeInTheDocument();
    // Once: a full sync starting after it, whose count moves with every
    // read, isn't said again.
    state.accounts = state.accounts.map((a) => ({
      ...a,
      sync: { ...a.sync, status: "running" as const, messagesSoFar: 120 },
    }));
    await client.refetchQueries();
    await expect.element(page.getByText(/120 messages so far/)).toBeInTheDocument();
    await expect.element(said).toBeInTheDocument();
    expect(said.element().textContent).not.toMatch(/Copying/);
    expect(sent(state.server.requests)).toContain("POST /api/v0/connected-accounts/2/sync");
  });

  it("says each check's own outcome when two follow each other", async () => {
    await open(api([home]).server);
    const said = page.getByRole("status").filter({ hasText: "ana@home.example:" });
    const check = page.getByRole("button", { name: "Check now ana@home.example" });

    await userEvent.click(check);
    await expect.element(said).toBeInTheDocument();
    const first = said.element().textContent;
    // Within the first check's 30 s.
    await userEvent.click(check);

    await expect.poll(() => said.element().textContent).not.toBe(first);
  });

  it("says a check started moments ago in words when it is refused for that", async () => {
    const { server } = api([home]);
    server.on("POST", "/api/v0/connected-accounts/{id}/sync", () =>
      problem(tooManyRequests, "/api/v0/problems/sync-throttled", {
        detail: "Requested moments ago.",
        retryAfter: 12,
      }),
    );
    await open(server);

    await userEvent.click(page.getByRole("button", { name: "Check now ana@home.example" }));

    await expect
      .element(page.getByRole("status").filter({ hasText: "checked moments ago" }))
      .toHaveTextContent("ana@home.example was checked moments ago. Check again in a few seconds.");
  });

  it("renames in place, then gives focus back to the pencil", async () => {
    await open(api([work]).server);
    const pencil = page.getByRole("button", { name: "Rename ana@work.example" });

    await userEvent.click(pencil);
    await expect.element(page.getByRole("textbox", { name: /^Name/ })).toHaveFocus();
    // The row still says which account it is, by the address a rename
    // never changes.
    await expect
      .element(page.getByRole("listitem").getByText("ana@work.example").first())
      .toBeInTheDocument();
    expect(page.getByRole("listitem").getByText("Work", { exact: true }).elements()).toHaveLength(
      0,
    );
    await userEvent.keyboard("{Escape}");

    await expect.element(pencil).toHaveFocus();
    expect(page.getByRole("textbox").elements()).toHaveLength(0);
  });

  it("disconnects after asking, then moves focus to the next account", async () => {
    const state = api([work, home]);
    await open(state.server);

    await userEvent.click(page.getByRole("button", { name: "Disconnect ana@work.example" }));
    const dialog = page.getByRole("alertdialog");
    await expect.element(dialog).toHaveAccessibleName("Disconnect Work?");
    await expect.element(dialog.getByRole("button", { name: "Cancel" })).toHaveFocus();
    // A keyboard press counts at once, unlike a click within a
    // double-click of the dialog opening.
    await userEvent.keyboard("{Tab}{Enter}");

    await expect
      .poll(() => sent(state.server.requests))
      .toContain("DELETE /api/v0/connected-accounts/1");
    await expect.element(page.getByText("Work is disconnected.")).toBeVisible();
    await expect
      .element(page.getByRole("button", { name: "Rename ana@home.example" }))
      .toHaveFocus();
    expect(state.accounts.map((a) => a.id)).toEqual([2]);
  });

  it("says an account was already disconnected when the server no longer has it", async () => {
    const state = api([work]);
    // Another tab disconnected it since the list was read: the server
    // removes it and says it was not there.
    state.server.on("DELETE", "/api/v0/connected-accounts/{id}", async (_, earlier) => {
      await earlier();
      return accountGone();
    });
    await open(state.server);

    await userEvent.click(page.getByRole("button", { name: "Disconnect ana@work.example" }));
    await expect.element(page.getByRole("alertdialog")).toBeVisible();
    await userEvent.keyboard("{Tab}{Enter}");

    await expect.element(page.getByText("Work was already disconnected.")).toBeVisible();
    await expect.element(page.getByRole("alertdialog")).not.toBeInTheDocument();
    await expect.element(page.getByRole("button", { name: "Connect Gmail" })).toHaveFocus();
  });

  it("reports Google's return as a problem to fix, and drops it from the address", async () => {
    const { router } = await open(
      api([]).server,
      "/settings/connected-accounts?outcome=gmail-not-granted",
    );

    await expect
      .element(page.getByRole("alert").getByText(/access to Gmail was left unticked/))
      .toBeVisible();
    expect(router.state.location.search).toEqual({});
  });

  it("keeps the list and says so when a refresh fails", async () => {
    const state = api([home]);
    const { client } = await open(state.server);
    state.server.on("GET", "/api/v0/connected-accounts", serverFault);

    void client.refetchQueries();

    await expect
      .element(page.getByRole("alert").getByText(/^Couldn't refresh this page/))
      .toBeVisible();
    await expect
      .element(page.getByRole("button", { name: "Check now ana@home.example" }))
      .toBeVisible();
  });

  it.each([
    ["the list faults, then the session ends", fault, unauthorized],
    ["the session ends, then the list faults", unauthorized, fault],
  ])("opens sign-in when one read ends the session: %s", async (_, list, providers) => {
    const answered = held();
    const frameRead = held();
    const { server } = api([home]);
    // The frame's own read of the list, once it draws, waits.
    server.on("GET", "/api/v0/connected-accounts", async () => {
      await frameRead.promise;
      return refuse(unauthorized);
    });
    server.once("GET", "/api/v0/connected-accounts", () => refuse(list));
    server.on("GET", "/api/v0/connected-accounts/providers", async () => {
      await answered.promise;
      return refuse(providers);
    });
    const opening = open(server, "/settings/connected-accounts", false);
    // The providers answer once the list has.
    await expect.poll(() => sent(server.requests)).toContain(`GET ${listPath}`);
    answered.resolve();
    const { router } = await opening;

    // The page says the session ended, not that a read failed.
    await expect.element(page.getByRole("heading", { name: "You're signed out" })).toBeVisible();
    await expect
      .element(page.getByRole("link", { name: "Sign in" }))
      .toHaveAttribute("href", "/sign-in?redirect=%2Fsettings%2Fconnected-accounts");

    // The frame's read finds the session ended too, on a page that has
    // drawn, so the rule opens sign-in.
    server.session = undefined;
    frameRead.resolve();
    await expect.element(page.getByText("Your session ended. Sign in to continue.")).toBeVisible();
    expect(router.state.location.pathname).toBe("/sign-in");
    expect(router.state.location.search).toEqual({ redirect: "/settings/connected-accounts" });
  });

  it("keeps Check now's newer list when the request's own answer comes later", async () => {
    const answered = held();
    const { server } = api([home]);
    // The server checks at once, and its answer waits.
    server.on("POST", "/api/v0/connected-accounts/{id}/sync", async (_, earlier) => {
      const answer = await earlier();
      await answered.promise;
      return answer;
    });
    const { client } = await open(server);
    await expect.element(page.getByText(/13 Nov 2026/)).toBeVisible();

    await userEvent.click(page.getByRole("button", { name: "Check now ana@home.example" }));
    await expect.poll(() => sent(server.requests)).toContain(`POST ${listPath}/2/sync`);
    // A read after the check lands first; later reads fail, so nothing
    // would put the newer time back.
    await client.refetchQueries();
    await expect.element(page.getByText(/13 Nov 2026/)).not.toBeInTheDocument();
    server.on("GET", "/api/v0/connected-accounts", serverFault);
    answered.resolve();

    await expect
      .element(page.getByRole("button", { name: "Check now ana@home.example" }))
      .toBeVisible();
    expect(page.getByText(/13 Nov 2026/).elements()).toHaveLength(0);
  });

  it("stops waiting for a check after 30 seconds", async () => {
    const clock = fakeClock();
    const { server } = api([home]);
    await open(server);
    // The list never shows the check.
    const release = holdReads(server);

    await userEvent.click(page.getByRole("button", { name: "Check now ana@home.example" }));
    await expect
      .element(page.getByRole("button", { name: "Checking... ana@home.example" }))
      .toBeVisible();
    await clock.advance(syncNowRefresh);

    await expect
      .element(page.getByRole("button", { name: "Check now ana@home.example" }))
      .toBeVisible();
    // Nothing landed, so nothing is said.
    expect(
      page.getByRole("status").filter({ hasText: "ana@home.example:" }).elements(),
    ).toHaveLength(0);
    release();
  });

  it("says when the providers can't be refreshed, and reads them again", async () => {
    const { server } = api([home]);
    const { client } = await open(server);
    // The next read fails, and those after it answer.
    server.once("GET", "/api/v0/connected-accounts/providers", serverFault);

    void client.refetchQueries();
    const failure = page.getByRole("alert");
    await expect.element(failure.getByText(/^Couldn't refresh this page/)).toBeVisible();
    await userEvent.click(failure.getByRole("button", { name: "Try again" }));

    await expect.element(page.getByRole("alert")).not.toBeInTheDocument();
    expect(
      sent(server.requests).filter((request) => request === `GET ${listPath}/providers`),
    ).toHaveLength(3);
  });

  it("shows a failed disconnect in its dialog, where pressing again retries", async () => {
    const { server } = api([work]);
    server.once("DELETE", "/api/v0/connected-accounts/{id}", serverFault);
    await open(server);

    await userEvent.click(page.getByRole("button", { name: "Disconnect ana@work.example" }));
    const dialog = page.getByRole("alertdialog");
    await expect.element(dialog).toBeVisible();
    await userEvent.keyboard("{Tab}{Enter}");
    await expect.element(dialog.getByRole("alert")).toHaveTextContent(
      // A fault may have come after the account went, so it is unknown.
      "Something went wrong on our side. Couldn't confirm whether Work is disconnected. Check the list before trying again.",
    );
    await userEvent.keyboard("{Enter}");

    await expect.element(page.getByText("Work is disconnected.")).toBeVisible();
    await expect.element(dialog).not.toBeInTheDocument();
  });

  it("keeps a long account's name inside the disconnect dialog", async () => {
    const long = listedAccount(
      4,
      "bartholomew.fitzgerald-wisniewska@northwind-industries-holdings.example.com",
    );
    // Wide enough for the dialog's buttons to sit side by side, where a
    // long label would push past its edge.
    await page.viewport(1024, 768);
    await open(api([long]).server);

    await userEvent.click(page.getByRole("button", { name: /^Disconnect bartholomew/ }));
    const dialog = page.getByRole("alertdialog");
    await expect.element(dialog).toBeVisible();

    const box = dialog.element().getBoundingClientRect();
    for (const button of dialog.getByRole("button").elements()) {
      const edge = button.getBoundingClientRect();
      expect(edge.left).toBeGreaterThanOrEqual(box.left);
      expect(edge.right).toBeLessThanOrEqual(box.right);
    }
    // Heard whole, the question mark included.
    await expect.element(dialog).toHaveAccessibleName(`Disconnect ${long.email}?`);
  });

  it("holds the dialog while a disconnect is under way", async () => {
    const answered = held();
    const { server } = api([work]);
    // The server disconnects at once, and its answer waits.
    server.on("DELETE", "/api/v0/connected-accounts/{id}", async (_, earlier) => {
      const answer = await earlier();
      await answered.promise;
      return answer;
    });
    await open(server);

    await userEvent.click(page.getByRole("button", { name: "Disconnect ana@work.example" }));
    const dialog = page.getByRole("alertdialog");
    await expect.element(dialog).toBeVisible();
    await userEvent.keyboard("{Tab}{Enter}");

    await expect.element(dialog.getByRole("button", { name: "Cancel" })).toBeDisabled();
    await expect.element(dialog.getByText("Disconnecting...")).toBeVisible();
    await userEvent.keyboard("{Escape}");
    await expect.element(dialog).toBeVisible();
    answered.resolve();
    await expect.element(dialog).not.toBeInTheDocument();
  });

  it("reads the whole list again after a disconnect", async () => {
    const state = api([work, home]);
    await open(state.server);
    // The read after the disconnect waits; meanwhile another tab names the
    // other account.
    const release = holdReads(state.server);

    await userEvent.click(page.getByRole("button", { name: "Disconnect ana@work.example" }));
    await expect.element(page.getByRole("alertdialog")).toBeVisible();
    await userEvent.keyboard("{Tab}{Enter}");
    await expect.element(page.getByText("Work is disconnected.")).toBeVisible();
    state.accounts = [{ ...home, name: "Home" }];
    release();

    await expect
      .element(page.getByRole("listitem").getByText("Home", { exact: true }))
      .toBeVisible();
  });

  it("asks for a look at the list when a disconnect's answer is lost", async () => {
    const { server } = api([work]);
    // The server disconnects, and its answer is lost.
    server.once("DELETE", "/api/v0/connected-accounts/{id}", async (_, earlier) => {
      await earlier();
      return lose();
    });
    await open(server);

    await userEvent.click(page.getByRole("button", { name: "Disconnect ana@work.example" }));
    await expect.element(page.getByRole("alertdialog")).toBeVisible();
    await userEvent.keyboard("{Tab}{Enter}");

    await expect
      .element(page.getByRole("alertdialog").getByRole("alert"))
      .toHaveTextContent(
        "Couldn't confirm whether Work is disconnected. Check the list before trying again.",
      );
  });
});

describe("the sync banner", () => {
  it("says syncing stopped, and who can fix it", async () => {
    const state = api([home]);
    state.syncStopped = true;
    await open(state.server);

    await expect
      .element(
        page.getByText(/stopped getting new mail for every account\. Ask your administrator\./),
      )
      .toBeVisible();
  });

  it("tells an administrator where to look", async () => {
    const state = api([home], { session: adminSession });
    state.syncStopped = true;
    await open(state.server);

    await expect.element(page.getByText(/Check that its worker is running\./)).toBeVisible();
  });

  it("follows a stop and a recovery on any signed-in page", async () => {
    const clock = fakeClock();
    const state = api([home]);
    await open(state.server, "/settings/security", false);
    await expect.element(page.getByRole("heading", { level: 1, name: "Security" })).toBeVisible();
    const banner = page.getByText(/stopped getting new mail/);

    const landmark = page.getByRole("region", { name: "Mail sync stopped" });
    await expect.element(landmark).not.toBeInTheDocument();

    state.syncStopped = true;
    await clock.advance(idleRefresh);
    await expect.element(banner).toBeVisible();
    // A landmark only while it shows.
    await expect.element(landmark).toBeVisible();
    state.syncStopped = false;
    await clock.advance(idleRefresh);

    await expect.element(banner).not.toBeInTheDocument();
    await expect.element(landmark).not.toBeInTheDocument();
  });
});
