import { RouterProvider } from "@tanstack/react-router";
import { describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import type { EmailVerificationResult } from "@/api/generated/model";
import { appRouter } from "@/test/app-router";
import { data, fakeServer, memberSession, problem, type Server } from "@/test/server";

// The tests of /verify-email, the page a verification email's link opens,
// through the app's own routes.

const notFound = 404;
const fault = 500;

const verified: EmailVerificationResult = { status: "verified" };

// open shows the app at address, e.g. "/verify-email#secret" for a link
// with a secret, over server, a visitor's when left out, and returns the
// router.
async function open(address: string, server?: Server) {
  const router = await appRouter(address, server);
  await render(<RouterProvider router={router} />);
  return router;
}

// brokenSessionRead returns a server whose session read fails with a
// server fault, so the page cannot tell whether anyone is signed in.
function brokenSessionRead(): Server {
  // With a session: without one the server answers the read a 401 itself.
  const server = fakeServer(memberSession);
  server.on("GET", "/api/v0/sessions/current", () =>
    problem(fault, "/api/v0/problems/internal-error", {
      detail: "Something went wrong on our side.",
    }),
  );
  return server;
}

describe("the verify-email page", () => {
  it("say how to get a link when opened without one", async () => {
    await open("/verify-email");

    await expect
      .element(page.getByRole("heading", { name: "Open the link from your email" }))
      .toBeVisible();
    // Security has a visitor without a session sign in, then opens.
    await expect
      .element(page.getByRole("link", { name: "Sign in to ask for a new link" }))
      .toHaveAttribute("href", "/settings/security");
  });

  describe("on /verify-email, when the session read fails,", () => {
    it("link to a new link in words for signed in or out", async () => {
      await open("/verify-email", brokenSessionRead());

      await expect
        .element(page.getByRole("heading", { name: "Open the link from your email" }))
        .toBeVisible();
      await expect
        .element(page.getByRole("link", { name: "Ask for a new link in Security" }))
        .toHaveAttribute("href", "/settings/security");
    });

    it("lead home after verifying", async () => {
      const server = brokenSessionRead();
      server.on("POST", "/api/v0/email-verifications/redeem", () => data(verified));
      await open("/verify-email#secret", server);

      await userEvent.click(page.getByRole("button", { name: "Verify email" }));

      await expect
        .element(page.getByRole("heading", { name: "Your email address is verified" }))
        .toBeVisible();
      await expect
        .element(page.getByRole("link", { name: "Go to home" }))
        .toHaveAttribute("href", "/");
    });

    it("link to a new link after a refused one", async () => {
      const server = brokenSessionRead();
      server.on("POST", "/api/v0/email-verifications/redeem", () =>
        problem(notFound, "/api/v0/problems/email-verification-link-invalid", {
          detail: "This link has expired, was already used, or is not valid.",
        }),
      );
      await open("/verify-email#secret", server);

      await userEvent.click(page.getByRole("button", { name: "Verify email" }));

      await expect
        .element(page.getByRole("heading", { name: "This link no longer works" }))
        .toBeVisible();
      await expect
        .element(page.getByRole("link", { name: "Ask for a new link in Security" }))
        .toHaveAttribute("href", "/settings/security");
    });
  });
});
