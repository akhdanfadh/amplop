import { RouterProvider } from "@tanstack/react-router";
import { describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import type { RegistrationAvailability } from "@/api/generated/model";
import { appRouter } from "@/test/app-router";
import { data, fakeServer, problem, type Server } from "@/test/server";

// The tests of a visitor's registration pages, /register and
// /register/complete, through the app's own routes.

const notFound = 404;

const redeemPath = "/api/v0/registration/links/redeem";

// open shows the app at address, e.g. "/register/complete#secret" for a
// link with a secret, over server, and returns the router.
async function open(address: string, server: Server) {
  const router = await appRouter(address, server);
  await render(<RouterProvider router={router} />);
  return router;
}

// registration returns a visitor's server that says whether registration
// is open as availability does.
function registration(availability: RegistrationAvailability): Server {
  const server = fakeServer();
  server.on("GET", "/api/v0/registration", () => data(availability));
  return server;
}

describe("registration's visitor pages", () => {
  it("start afresh on sign-up with a new link pasted after one ended", async () => {
    const server = fakeServer();
    server.on("POST", redeemPath, () =>
      problem(notFound, "/api/v0/problems/registration-link-invalid", {
        detail: "This link has expired, was already used, or is not valid. Ask for a new one.",
      }),
    );
    const router = await open("/register/complete#old", server);
    await userEvent.type(page.getByRole("textbox", { name: "Email address" }), "ana@example.com");
    await userEvent.type(page.getByLabelText("Password", { exact: true }), "a long new password");
    await userEvent.type(
      page.getByLabelText("Confirm password", { exact: true }),
      "a long new password",
    );
    await userEvent.click(page.getByRole("button", { name: "Create account" }));
    await expect
      .element(page.getByRole("heading", { name: "This link no longer works" }))
      .toBeVisible();

    await router.navigate({ to: "/register/complete", hash: "fresh" });

    await expect.element(page.getByRole("heading", { name: "Create your account" })).toBeVisible();
    expect(server.requests.filter((asked) => asked.path === redeemPath)).toHaveLength(1);
  });

  it("keep a title still when a check finds the form under it", async () => {
    await open("/register", registration({ open: true }));

    await expect.element(page.getByRole("textbox", { name: "Email address" })).toBeVisible();

    const header = page.getByRole("heading", { name: "Create an account" }).element().parentElement;
    expect(header?.getAnimations()).toEqual([]);
    expect(header?.className).not.toContain("animate-in");
  });

  it("say when registration is closed", async () => {
    await open("/register", registration({ open: false }));

    await expect
      .element(page.getByRole("heading", { name: "Registration is closed" }))
      .toHaveFocus();
  });
});
