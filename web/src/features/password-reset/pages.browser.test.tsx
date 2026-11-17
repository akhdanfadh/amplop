import { RouterProvider } from "@tanstack/react-router";
import { describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import type { PasswordResetRequestResult } from "@/api/generated/model";
import { appRouter } from "@/test/app-router";
import { data, fakeServer, problem, type Server } from "@/test/server";

// The tests of a visitor's password reset pages, /forgot-password and
// /reset-password, through the app's own routes.

const accepted = 202;
const notFound = 404;

const requested: PasswordResetRequestResult = { status: "requested" };

const redeemPath = "/api/v0/password-reset/links/redeem";

// open shows the app at address, e.g. "/reset-password#secret" for a link
// with a secret, over server, a visitor's when left out, and returns the
// router.
async function open(address: string, server?: Server) {
  const router = await appRouter(address, server);
  await render(<RouterProvider router={router} />);
  return router;
}

// requesting returns a visitor's server that accepts a request for a
// reset link.
function requesting(): Server {
  const server = fakeServer();
  server.on("POST", "/api/v0/password-reset/links", () => data(requested, accepted));
  return server;
}

// linkGone returns a visitor's server that refuses every redeem: its link
// no longer works.
function linkGone(): Server {
  const server = fakeServer();
  server.on("POST", redeemPath, () =>
    problem(notFound, "/api/v0/problems/password-reset-link-invalid", {
      detail: "This link has expired, was already used, or is not valid. Ask for a new one.",
    }),
  );
  return server;
}

// resetWith fills the reset form and sends it.
async function resetWith(password: string) {
  await userEvent.type(page.getByRole("textbox", { name: "Email address" }), "ana@example.com");
  await userEvent.type(page.getByLabelText("New password", { exact: true }), password);
  await userEvent.type(page.getByLabelText("Confirm new password", { exact: true }), password);
  await userEvent.click(page.getByRole("button", { name: "Change password" }));
}

describe("password reset pages", () => {
  it("show an outcome as the title, which takes focus", async () => {
    await open("/forgot-password", requesting());
    await userEvent.type(page.getByRole("textbox", { name: "Email address" }), "ana@example.com");

    await userEvent.click(page.getByRole("button", { name: "Send reset link" }));

    await expect.element(page.getByRole("heading", { name: "Check your email" })).toHaveFocus();
    // The address the link went to, on a line of its own.
    await expect.element(page.getByText("ana@example.com", { exact: true })).toBeVisible();
  });

  it("return to the form with the last address and focus in its field", async () => {
    await open("/forgot-password", requesting());
    await userEvent.type(page.getByRole("textbox", { name: "Email address" }), "ana@example.com");
    await userEvent.click(page.getByRole("button", { name: "Send reset link" }));

    await userEvent.click(page.getByRole("button", { name: "Use a different email address" }));

    const email = page.getByRole("textbox", { name: "Email address" });
    await expect.element(email).toHaveFocus();
    await expect.element(email).toHaveValue("ana@example.com");
  });

  it("end a page whose link no longer works, with a way to a new one", async () => {
    await open("/reset-password#secret", linkGone());

    await resetWith("a long new password");

    await expect
      .element(page.getByRole("heading", { name: "This link no longer works" }))
      .toHaveFocus();
    await expect
      .element(page.getByRole("link", { name: "Ask for a new link" }))
      .toHaveAttribute("href", "/forgot-password");
  });

  it("start afresh with a new link pasted after one ended", async () => {
    const server = linkGone();
    const router = await open("/reset-password#old", server);
    await resetWith("a long new password");
    await expect
      .element(page.getByRole("heading", { name: "This link no longer works" }))
      .toBeVisible();

    await router.navigate({ to: "/reset-password", hash: "fresh" });
    await expect
      .element(page.getByRole("heading", { name: "Choose a new password" }))
      .toBeVisible();
    await resetWith("another long password");

    const redeems = () => server.requests.filter((asked) => asked.path === redeemPath);
    await expect.poll(() => redeems()).toHaveLength(2);
    expect(redeems().at(-1)?.body).toMatchObject({ secret: "fresh" });
  });
});
