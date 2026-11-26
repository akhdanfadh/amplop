import { describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { RouterProvider } from "@tanstack/react-router";
import {
  NewSystemEmailSettings,
  type ListedSessions,
  type SystemEmailSettings,
  type SystemEmailTestMessageResult,
  type TotpStatus,
} from "@/api/generated/model";
import { appRouter } from "@/test/app-router";
import { pressInDialog } from "@/test/dialog";
import {
  adminSession,
  data,
  fakeServer,
  held,
  lose,
  problem,
  testedSystemEmail as saved,
  type Asked,
  type Server,
} from "@/test/server";

const fault = 500;

// What Security, the page a test leaves to, reads.
const twoFactorOn: TotpStatus = { status: "on", backupCodesLeft: 10, backupCodesWaiting: false };
const browsers: ListedSessions = { sessions: [] };

// api returns a fake server for an administrator, which holds settings as
// the saved system email, nothing when left out. A save replaces them,
// untested; a test message answers test and marks them tested when it is
// accepted, untested when not.
function api(
  settings?: SystemEmailSettings,
  test: SystemEmailTestMessageResult = { status: "accepted" },
): Server {
  const server = fakeServer(adminSession);
  server.on("GET", "/api/v0/system-email", () => data({ settings }));
  server.on("PUT", "/api/v0/system-email", ({ body }) => {
    const { password, ...rest } = NewSystemEmailSettings.parse(body);
    settings = {
      ...rest,
      username: rest.username ?? "",
      hasPassword: Boolean(rest.username && (password || settings?.hasPassword)),
      testedAt: null,
    };
    return data({ settings });
  });
  server.on("POST", "/api/v0/system-email/test-message", () => {
    if (settings)
      settings = {
        ...settings,
        testedAt: test.status === "accepted" ? "2026-11-15T10:00:00Z" : null,
      };
    return data(test);
  });
  server.on("GET", "/api/v0/totp", () => data(twoFactorOn));
  server.on("GET", "/api/v0/sessions", () => data(browsers));
  return server;
}

// sent lists requests as e.g. "GET /api/v0/system-email".
const sent = (requests: Asked[]) =>
  requests.map((request) => `${request.method} ${request.url.pathname}`);

// open renders the app at System email, its requests answered by server,
// and returns its router.
async function open(server: Server) {
  const router = await appRouter("/settings/system-email", server);
  await render(<RouterProvider router={router} />);
  return router;
}

const button = (name: string) => page.getByRole("button", { name, exact: true });
const field = (name: string) => page.getByRole("textbox", { name, exact: true });
const summary = () => page.getByRole("definition");

describe("System email", () => {
  it("shows the saved settings and opens the form in their place", async () => {
    await open(api(saved));

    await expect.element(page.getByText("Tested", { exact: true })).toBeVisible();
    await expect.element(page.getByText("amplop <amplop@example.com>")).toBeVisible();
    await expect.element(page.getByText(/Last test passed/)).toBeVisible();

    await userEvent.click(button("Edit settings"));

    await expect.element(field("Server")).toHaveFocus();
    await expect.element(field("Server")).toHaveValue("smtp.example.com");
    await expect.element(field("Port")).toHaveValue("465");
    await expect.element(page.getByRole("radio", { name: "TLS" })).toBeChecked();
    await expect.element(button("Send test message")).toBeDisabled();
    await expect
      .element(page.getByText("Save or cancel your changes to send a test."))
      .toBeVisible();
    expect(summary().elements()).toHaveLength(0);
  });

  it("reads the settings once as it opens", async () => {
    const server = api(saved);
    await open(server);
    await expect.element(page.getByText("amplop <amplop@example.com>")).toBeVisible();

    // The page uses what its loader just read rather than reading it again.
    await new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(done)));
    expect(
      sent(server.requests).filter((request) => request === "GET /api/v0/system-email"),
    ).toHaveLength(1);
  });

  it("saves only, closes the form, and gives focus back to Edit settings", async () => {
    const server = api(saved);
    await open(server);
    await userEvent.click(button("Edit settings"));

    await userEvent.fill(field("From name"), "amplop team");
    await userEvent.click(button("Save only"));

    await expect.element(button("Edit settings")).toHaveFocus();
    await expect.element(page.getByText("amplop team <amplop@example.com>")).toBeVisible();
    await expect
      .element(page.getByText("Settings saved. Send a test message to check them."))
      .toBeVisible();
    await expect.element(page.getByText("Not confirmed", { exact: true })).toBeVisible();
    // The port goes as a number, the password left out keeps the saved one.
    expect(server.requests.flatMap(({ body }) => (body === undefined ? [] : [body]))).toEqual([
      expect.objectContaining({ port: 465, password: "", fromName: "amplop team" }),
    ]);
    expect(sent(server.requests)).not.toContain("POST /api/v0/system-email/test-message");
  });

  it("saves and sends a test, from the button or Enter in a field", async () => {
    const server = api(saved);
    await open(server);
    await userEvent.click(button("Edit settings"));

    await userEvent.type(field("From name"), " team{Enter}");

    await expect
      .element(
        page.getByText(
          "The mail server accepted the test message. Check the inbox of ana@example.com.",
        ),
      )
      .toBeVisible();
    await expect.element(page.getByText("Tested", { exact: true })).toBeVisible();
    expect(sent(server.requests).filter((request) => !request.startsWith("GET"))).toEqual([
      "PUT /api/v0/system-email",
      "POST /api/v0/system-email/test-message",
    ]);
    await expect.element(button("Send test message")).toHaveFocus();
  });

  it("moves focus to an invalid recipient when Save and send test cannot send", async () => {
    const server = api(saved);
    await open(server);
    await userEvent.click(button("Edit settings"));
    await userEvent.fill(field("Recipient"), "ana");

    await userEvent.click(button("Save and send test"));

    await expect.element(field("Recipient")).toHaveFocus();
    await expect.element(field("Recipient")).toHaveAttribute("aria-invalid", "true");
    expect(sent(server.requests)).not.toContain("POST /api/v0/system-email/test-message");
  });

  it("keeps the form open after a lost answer, and Cancel shows what was saved", async () => {
    const server = api(saved);
    // The server saves, and its answer is lost.
    server.once("PUT", "/api/v0/system-email", async (_, earlier) => {
      await earlier();
      return lose();
    });
    await open(server);
    await userEvent.click(button("Edit settings"));

    await userEvent.fill(field("From name"), "amplop team");
    const reads = () =>
      sent(server.requests).filter((request) => request === "GET /api/v0/system-email");
    const readsBefore = reads().length;
    await userEvent.click(button("Save and send test"));

    await expect
      .element(page.getByText(/We couldn't confirm the settings were saved\. Save again/))
      .toBeVisible();
    await expect.element(field("From name")).toBeEnabled();
    await expect.element(button("Save and send test")).toBeEnabled();
    // The page reads what is saved now, and sends no test.
    await vi.waitFor(() => expect(reads()).toHaveLength(readsBefore + 1));
    expect(sent(server.requests)).not.toContain("POST /api/v0/system-email/test-message");

    await userEvent.click(button("Cancel"));

    await expect.element(page.getByText("amplop team <amplop@example.com>")).toBeVisible();
    await expect.element(page.getByText("Not confirmed", { exact: true })).toBeVisible();
  });

  it("moves a usual port with Security and keeps a typed one", async () => {
    await open(api(saved));
    await userEvent.click(button("Edit settings"));

    await userEvent.click(page.getByText("STARTTLS", { exact: true }));
    await expect.element(field("Port")).toHaveValue("587");
    await expect
      .element(field("Port"))
      .toHaveAccessibleDescription("Changed to 587, the usual port for STARTTLS.");

    await userEvent.fill(field("Port"), "2525");
    await userEvent.click(page.getByText("TLS", { exact: true }));
    await expect.element(field("Port")).toHaveValue("2525");
    await expect
      .element(field("Port"))
      .toHaveAccessibleDescription("The usual port for TLS is 465.");
  });

  it("opens the form at once when nothing is saved, with nothing to cancel", async () => {
    await open(api());

    await expect.element(page.getByText("Not set up", { exact: true })).toBeVisible();
    await expect.element(field("Server")).toBeVisible();
    await expect.element(field("Port")).toHaveValue("587");
    expect(button("Cancel").elements()).toHaveLength(0);
    await expect
      .element(page.getByText("Save the settings before sending a test message."))
      .toBeVisible();
    await expect.element(button("Send test message")).toBeDisabled();
  });

  it("asks before leaving with changes, and not after typing them back", async () => {
    const router = await open(api(saved));
    await userEvent.click(button("Edit settings"));

    await userEvent.fill(field("From name"), "amplop team");
    void router.navigate({ to: "/settings/security" });
    await expect
      .element(page.getByRole("alertdialog", { name: "Discard your changes?" }))
      .toBeVisible();
    await userEvent.click(button("Keep editing"));
    await expect.element(field("From name")).toHaveValue("amplop team");

    await userEvent.fill(field("From name"), "amplop");
    await router.navigate({ to: "/settings/security" });
    await expect.element(page.getByRole("heading", { level: 1, name: "Security" })).toBeVisible();
  });

  it("keeps typed settings when the session ends on a save, and signs in back here", async () => {
    const server = api(saved);
    const router = await open(server);
    await userEvent.click(button("Edit settings"));
    await userEvent.fill(field("From name"), "amplop team");

    server.session = undefined;
    await userEvent.click(button("Save only"));

    const banner = page.getByRole("region", { name: "Signed out" });
    await expect.element(banner).toBeVisible();
    expect(banner.element().textContent).toContain("Copy what you need, then sign in.");
    await expect.element(field("From name")).toHaveValue("amplop team");
    expect(router.state.location.pathname).toBe("/settings/system-email");

    // The leave warning asks, since the typed settings go with the page.
    await userEvent.click(banner.getByRole("link", { name: "sign in" }));
    await pressInDialog("Discard changes");

    await expect.element(page.getByText("Your session ended. Sign in to continue.")).toBeVisible();
    expect(router.state.location.pathname).toBe("/sign-in");
    expect(router.state.location.search).toEqual({ redirect: "/settings/system-email" });
    expect(router.state.location.state.sessionEnded).toBe(true);
  });

  it("opens sign-in when the session ended before the page first drew", async () => {
    // The guard's session read passes; the settings read finds it ended,
    // as when another tab signed out between the two. The loader's 401
    // comes before any page has drawn, so the rule leaves it to the
    // router's view; the frame's own reads, made once it draws, get a 401
    // on a page that needs a session, and the rule opens sign-in.
    const server = api(saved);
    server.once("GET", "/api/v0/sessions/current", () => {
      const session = server.session;
      server.session = undefined;
      return data(session);
    });
    const router = await open(server);

    await expect.element(page.getByText("Your session ended. Sign in to continue.")).toBeVisible();
    expect(router.state.location.pathname).toBe("/sign-in");
    expect(router.state.location.search).toEqual({ redirect: "/settings/system-email" });
  });

  it("opens sign-in when the session ends on a test message", async () => {
    const server = api(saved);
    const router = await open(server);
    await expect.element(button("Send test message")).toBeEnabled();

    server.session = undefined;
    await userEvent.click(button("Send test message"));

    await expect.element(page.getByText("Your session ended. Sign in to continue.")).toBeVisible();
    expect(router.state.location.pathname).toBe("/sign-in");
    expect(router.state.location.search).toEqual({ redirect: "/settings/system-email" });
  });

  it("says why a test failed, and that the settings have not passed one", async () => {
    await open(api(saved, { status: "failed", reason: "auth" }));
    await expect.element(button("Send test message")).toBeEnabled();

    await userEvent.click(button("Send test message"));

    await expect
      .element(
        page.getByText("The mail server rejected the sign-in. Check the username and password."),
      )
      .toBeVisible();
    await expect.element(page.getByText("These settings haven't passed a test yet.")).toBeVisible();
    await expect.element(page.getByText("Not confirmed", { exact: true })).toBeVisible();
  });

  it("keeps the summary when a refresh fails, says so, and recovers on Try again", async () => {
    const server = api(saved);
    const router = await open(server);
    await expect.element(page.getByText("amplop <amplop@example.com>")).toBeVisible();

    server.on("GET", "/api/v0/system-email", () =>
      problem(fault, "/api/v0/problems/internal-error"),
    );
    await router.options.context.queryClient.refetchQueries();

    await expect
      .element(page.getByText(/Could not check the system email settings\./))
      .toBeVisible();
    await expect.element(page.getByText("amplop <amplop@example.com>")).toBeVisible();

    server.on("GET", "/api/v0/system-email", () => data({ settings: saved }));
    await userEvent.click(button("Try again"));
    await expect
      .element(page.getByText(/Could not check the system email settings\./))
      .not.toBeInTheDocument();
  });

  it("keeps a save waiting while a test message sends", async () => {
    const test = held();
    const server = api(saved);
    server.once("POST", "/api/v0/system-email/test-message", async (_, earlier) => {
      await test.promise;
      return earlier();
    });
    await open(server);
    await userEvent.click(button("Send test message"));
    await expect.element(button("Sending test message...")).toBeVisible();

    await userEvent.click(button("Edit settings"));
    await userEvent.fill(field("From name"), "amplop team");
    await expect.element(button("Save and send test")).toBeDisabled();
    await expect.element(button("Save only")).toBeDisabled();

    test.resolve();
    await expect.element(button("Save and send test")).toBeEnabled();
    await userEvent.click(button("Save and send test"));
    await expect.element(page.getByText("amplop team <amplop@example.com>")).toBeVisible();
    await vi.waitFor(() =>
      expect(
        sent(server.requests).filter(
          (request) => request === "POST /api/v0/system-email/test-message",
        ),
      ).toHaveLength(2),
    );
  });
});
