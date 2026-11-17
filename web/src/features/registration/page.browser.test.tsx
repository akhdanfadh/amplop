import { describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { RouterProvider } from "@tanstack/react-router";
import {
  NewRegistrationSettings,
  type RegistrationSettings,
  type SystemEmailSettings,
  type SystemEmailTestMessageResult,
} from "@/api/generated/model";
import { appRouter } from "@/test/app-router";
import { adminSession, data, fakeServer, held, lose, problem, type Server } from "@/test/server";

const conflict = 409;
const fault = 500;
const testedAt = "2026-11-12T09:38:00Z";
const systemEmail: SystemEmailSettings = {
  host: "smtp.example.com",
  port: 587,
  security: "starttls",
  username: "",
  hasPassword: false,
  fromAddress: "inbok@example.com",
  fromName: "inbok",
  testedAt,
};

// Api is the fake server and what it holds: opened, whether an
// administrator opened registration, and systemEmail, the saved settings,
// whose testedAt says whether they passed a test. A test changes either
// as another tab would.
type Api = { server: Server; opened: boolean; systemEmail: SystemEmailSettings };

// registrationOf returns the settings the API shows for state.
function registrationOf(state: Api): RegistrationSettings {
  const tested = state.systemEmail.testedAt !== null;
  return {
    status: state.opened ? (tested ? "open" : "paused") : "closed",
    systemEmailTested: tested,
    allowedActions: tested ? ["open", "close"] : ["close"],
  };
}

// api returns a fake server for an administrator, with registration
// opened or closed and system email saved, tested or not. Opening needs
// tested system email; a system email save clears its test, which pauses
// an open registration; a test message answers test, and passes or clears
// the test with it.
function api(
  registration: "open" | "closed",
  email: "tested" | "untested",
  test: SystemEmailTestMessageResult = { status: "accepted" },
): Api {
  const server = fakeServer(adminSession);
  const state: Api = {
    server,
    opened: registration === "open",
    systemEmail: { ...systemEmail, testedAt: email === "tested" ? testedAt : null },
  };
  server.on("GET", "/api/v0/registration/settings", () => data(registrationOf(state)));
  server.on("PUT", "/api/v0/registration/settings", ({ body }) => {
    const opening = NewRegistrationSettings.parse(body).open;
    if (opening && state.systemEmail.testedAt === null)
      return problem(conflict, "/api/v0/problems/system-email-not-tested", {
        detail: "Send a test message that passes first.",
      });
    state.opened = opening;
    return data(registrationOf(state));
  });
  server.on("GET", "/api/v0/system-email", () => data({ settings: state.systemEmail }));
  server.on("PUT", "/api/v0/system-email", () => {
    state.systemEmail = { ...state.systemEmail, testedAt: null };
    return data({ settings: state.systemEmail });
  });
  server.on("POST", "/api/v0/system-email/test-message", () => {
    state.systemEmail = {
      ...state.systemEmail,
      testedAt: test.status === "accepted" ? "2026-11-15T10:00:00Z" : null,
    };
    return data(test);
  });
  return state;
}

// serverFault is a server fault, as the API words one.
const serverFault = () =>
  problem(fault, "/api/v0/problems/internal-error", {
    detail: "Something went wrong on our side.",
  });

// The router of the page open now, whose reads reread marks.
let opened: Awaited<ReturnType<typeof appRouter>> | undefined;

// open renders the app at Registration, its requests answered by server,
// and returns its router.
async function open(server: Server) {
  const router = await appRouter("/settings/registration", server);
  opened = router;
  await render(<RouterProvider router={router} />);
  return router;
}

// reread marks every read out of date, so the page reads its own again,
// as it does e.g. on returning to the window.
async function reread(): Promise<void> {
  await opened?.options.context.queryClient.invalidateQueries();
}

const button = (name: string) => page.getByRole("button", { name, exact: true });
const dialog = () => page.getByRole("alertdialog");
const announcement = () => page.getByRole("status").last();

// confirmByKeyboard presses the dialog's confirming button: Cancel has
// focus, the action is next. A keyboard press counts at once, unlike a click
// within a double-click of the dialog opening.
async function confirmByKeyboard(): Promise<void> {
  await expect.element(dialog()).toBeVisible();
  await userEvent.keyboard("{Tab}{Enter}");
}

// frames waits for the browser to draw twice, e.g. after a scroll.
function frames(): Promise<void> {
  return new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(() => done())));
}

describe("Registration", () => {
  it("holds the change in its dialog until it is saved", async () => {
    const save = held();
    const { server } = api("closed", "tested");
    server.on("PUT", "/api/v0/registration/settings", async (_, earlier) => {
      await save.promise;
      return earlier();
    });
    await open(server);
    await expect.element(page.getByText("Closed", { exact: true })).toBeVisible();

    await userEvent.click(button("Open registration"));
    await confirmByKeyboard();
    await expect.element(button("Opening...")).toBeVisible();
    await expect.element(button("Cancel")).toBeDisabled();
    // A change already sent can't be called back.
    await userEvent.keyboard("{Escape}");
    await expect.element(dialog()).toBeVisible();

    save.resolve();
    await expect.element(dialog()).not.toBeInTheDocument();
    await expect.element(page.getByText("Open", { exact: true })).toBeVisible();
    await expect.element(button("Close registration")).toHaveFocus();
    await expect.element(announcement()).toHaveTextContent("Registration opened.");
  });

  it("explains a missing Open and leads to System email", async () => {
    await open(api("closed", "untested").server);

    await expect.element(page.getByText(/needs system email that passed a test/)).toBeVisible();
    await expect.element(button("Open registration")).not.toBeInTheDocument();
    await expect
      .element(page.getByRole("link", { name: "Go to System email" }))
      .toHaveAttribute("href", "/settings/system-email");
  });

  it("moves focus to System email when closing leaves no button", async () => {
    await open(api("open", "untested").server);
    await expect.element(page.getByText("Paused", { exact: true })).toBeVisible();

    await userEvent.click(button("Close registration"));
    await confirmByKeyboard();

    await expect.element(dialog()).not.toBeInTheDocument();
    await expect.element(page.getByText("Closed", { exact: true })).toBeVisible();
    await expect.element(page.getByRole("link", { name: "Go to System email" })).toHaveFocus();
  });

  it("shows a failure in the dialog, where pressing again retries", async () => {
    const state = api("closed", "tested");
    state.server.once("PUT", "/api/v0/registration/settings", serverFault);
    await open(state.server);

    await userEvent.click(button("Open registration"));
    await confirmByKeyboard();
    // A fault leaves it unknown whether the change was saved.
    await expect
      .element(dialog().getByRole("alert"))
      .toHaveTextContent(
        "Something went wrong on our side. Couldn't confirm whether registration was opened. Trying again is safe.",
      );
    await expect.element(button("Open registration").last()).toHaveFocus();

    await userEvent.keyboard("{Enter}");
    await expect.element(dialog()).not.toBeInTheDocument();
    expect(state.opened).toBe(true);
  });

  it("says trying again is safe when the answer is lost", async () => {
    const { server } = api("closed", "tested");
    // The server saves, and its answer is lost.
    server.once("PUT", "/api/v0/registration/settings", async (_, earlier) => {
      await earlier();
      return lose();
    });
    await open(server);

    await userEvent.click(button("Open registration"));
    await confirmByKeyboard();

    await expect
      .element(dialog().getByRole("alert"))
      .toHaveTextContent("Couldn't confirm whether registration was opened. Trying again is safe.");
  });

  it("keeps the page and says so when a refresh fails", async () => {
    const { server } = api("open", "tested");
    await open(server);

    await expect.element(button("Close registration")).toBeVisible();
    // The page's own reread, e.g. on returning to the window, fails.
    server.on("GET", "/api/v0/registration/settings", serverFault);
    await reread();
    await expect.element(page.getByText(/Couldn't refresh registration/)).toBeVisible();
    await expect.element(button("Close registration")).toBeVisible();
  });

  it("reads registration again after System email changed it", async () => {
    const read = held();
    const { server } = api("open", "tested");
    const router = await open(server);
    await expect.element(page.getByText("Open", { exact: true })).toBeVisible();

    await router.navigate({ to: "/settings/system-email" });
    await userEvent.click(button("Edit settings"));
    await userEvent.click(button("Save only"));
    await expect.element(button("Edit settings")).toBeVisible();
    // Back to Registration, whose read now waits: the page must not draw
    // the status it knows is out of date meanwhile.
    server.once("GET", "/api/v0/registration/settings", async (_, earlier) => {
      await read.promise;
      return earlier();
    });
    const back = router.navigate({ to: "/settings/registration" });
    await frames();
    expect(document.body.textContent).not.toMatch(/Anyone who can reach this inbok can create/);

    read.resolve();
    await back;
    await expect.element(page.getByText("Paused", { exact: true })).toBeVisible();
  });
});

describe("Registration, when things change elsewhere", () => {
  it("moves focus on when the action went away while its dialog was open", async () => {
    const state = api("closed", "tested");
    await open(state.server);

    await userEvent.click(button("Open registration"));
    await expect.element(dialog()).toBeVisible();
    // Meanwhile system email loses its test, e.g. in another tab: the
    // page's next read has no Open.
    state.systemEmail = { ...state.systemEmail, testedAt: null };
    await reread();
    await expect
      .element(page.getByText(/needs system email that passed a test/))
      .toBeInTheDocument();
    await userEvent.keyboard("{Escape}");

    await expect.element(dialog()).not.toBeInTheDocument();
    await expect.element(page.getByRole("link", { name: "Go to System email" })).toHaveFocus();
  });

  it("reads registration again after a failed test on System email", async () => {
    const router = await open(api("open", "tested", { status: "failed", reason: "auth" }).server);
    await expect.element(page.getByText("Open", { exact: true })).toBeVisible();

    await router.navigate({ to: "/settings/system-email" });
    await userEvent.click(button("Send test message"));
    await expect.element(page.getByText(/sign-in|failed|Couldn't/i).first()).toBeVisible();
    await router.navigate({ to: "/settings/registration" });

    await expect.element(page.getByText("Paused", { exact: true })).toBeVisible();
  });

  it("shows the router's failure view when the reread on a visit back fails", async () => {
    const { server } = api("open", "tested");
    const router = await open(server);
    await expect.element(page.getByText("Open", { exact: true })).toBeVisible();

    await router.navigate({ to: "/settings/system-email" });
    await userEvent.click(button("Edit settings"));
    await userEvent.click(button("Save only"));
    await expect.element(button("Edit settings")).toBeVisible();
    server.once("GET", "/api/v0/registration/settings", serverFault);
    await router.navigate({ to: "/settings/registration" });

    // Not the old status: the read it waited for failed, and says so.
    await expect.element(button("Try again")).toBeVisible();
    expect(document.body.textContent).not.toMatch(/Anyone who can reach this inbok can create/);
  });
});
