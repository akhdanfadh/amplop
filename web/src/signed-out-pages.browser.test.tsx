import "@/zod-config";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { retryOptions } from "@/api/retry";
import {
  Outlet,
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  type RouteComponent,
} from "@tanstack/react-router";
import { Route as ForgotPassword } from "@/routes/_auth/_signed-out/forgot-password";
import { Route as Register } from "@/routes/_auth/_signed-out/register/index";
import { Route as CompleteRegistration } from "@/routes/_auth/register/complete";
import { Route as ResetPassword } from "@/routes/_auth/reset-password";
import { Route as VerifyEmail } from "@/routes/_auth/verify-email";

const accepted = 202;
const notFound = 404;
const unauthorized = 401;
const fault = 500;

// The pages under test, by address.
const pages: Record<string, RouteComponent | undefined> = {
  "/forgot-password": ForgotPassword.options.component,
  "/register": Register.options.component,
  "/register/complete": CompleteRegistration.options.component,
  "/reset-password": ResetPassword.options.component,
  "/verify-email": VerifyEmail.options.component,
};

// Answer is the fake server's response to one request.
type Answer = () => Response;

// problem answers with a Problem Details body of type and status.
function problem(status: number, type: string, detail: string): Response {
  return Response.json(
    { type, status, title: "Refused", detail },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}

const signedOut: Answer = () => problem(unauthorized, "/api/v0/problems/unauthorized", "");

// broken answers with a server fault, as a failed session read would.
const broken: Answer = () =>
  problem(fault, "/api/v0/problems/internal-error", "Something went wrong on our side.");

// requested answers an accepted request for a reset link.
const requested: Answer = () =>
  Response.json({ data: { status: "requested" } }, { status: accepted });

// open shows the page at address, alone in a router of its own, with each
// request answered by the entry of answers for its path, else as signed
// out. It returns the router and the fake fetch, which records requests.
async function open(address: string, answers: Record<string, Answer> = {}) {
  const fetch = vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>(
    async (input) => {
      const url = input instanceof Request ? input.url : String(input);
      const path = new URL(url, window.location.origin).pathname;
      return (answers[path] ?? signedOut)();
    },
  );
  vi.stubGlobal("fetch", fetch);
  const root = createRootRoute({ component: Outlet });
  const routes = Object.entries(pages).map(([path, component]) =>
    createRoute({ getParentRoute: () => root, path, component }),
  );
  const router = createRouter({
    routeTree: root.addChildren(routes),
    history: createMemoryHistory({ initialEntries: [address] }),
  });
  await router.load();
  await render(
    // The app's retries: a 401 or a fault answers at once.
    <QueryClientProvider client={new QueryClient({ defaultOptions: retryOptions })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return { router, fetch };
}

// resetWith fills the reset form and sends it.
async function resetWith(password: string) {
  await userEvent.type(page.getByRole("textbox", { name: "Email address" }), "ana@example.com");
  await userEvent.type(page.getByLabelText("New password", { exact: true }), password);
  await userEvent.type(page.getByLabelText("Confirm new password", { exact: true }), password);
  await userEvent.click(page.getByRole("button", { name: "Change password" }));
}

// linkGone answers a redeem whose link no longer works.
const linkGone: Answer = () =>
  problem(
    notFound,
    "/api/v0/problems/password-reset-link-invalid",
    "This link has expired, was already used, or is not valid. Ask for a new one.",
  );

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("signed-out pages", () => {
  it("open with focus on their title", async () => {
    await open("/forgot-password");

    await expect.element(page.getByRole("heading", { name: "Reset your password" })).toHaveFocus();
  });

  it("show an outcome as the title, which takes focus", async () => {
    await open("/forgot-password", {
      "/api/v0/password-reset/links": requested,
    });
    await userEvent.type(page.getByRole("textbox", { name: "Email address" }), "ana@example.com");

    await userEvent.click(page.getByRole("button", { name: "Send reset link" }));

    await expect.element(page.getByRole("heading", { name: "Check your email" })).toHaveFocus();
    // The address the link went to, on a line of its own.
    await expect.element(page.getByText("ana@example.com", { exact: true })).toBeVisible();
  });

  it("return to the form with the last address and focus in its field", async () => {
    await open("/forgot-password", {
      "/api/v0/password-reset/links": requested,
    });
    await userEvent.type(page.getByRole("textbox", { name: "Email address" }), "ana@example.com");
    await userEvent.click(page.getByRole("button", { name: "Send reset link" }));

    await userEvent.click(page.getByRole("button", { name: "Use a different email address" }));

    const email = page.getByRole("textbox", { name: "Email address" });
    await expect.element(email).toHaveFocus();
    await expect.element(email).toHaveValue("ana@example.com");
  });

  it("end a page whose link no longer works, with a way to a new one", async () => {
    await open("/reset-password#secret", { "/api/v0/password-reset/links/redeem": linkGone });

    await resetWith("a long new password");

    await expect
      .element(page.getByRole("heading", { name: "This link no longer works" }))
      .toHaveFocus();
    await expect
      .element(page.getByRole("link", { name: "Ask for a new link" }))
      .toHaveAttribute("href", "/forgot-password");
  });

  it("start afresh with a new link pasted after one ended", async () => {
    const { router, fetch } = await open("/reset-password#old", {
      "/api/v0/password-reset/links/redeem": linkGone,
    });
    await resetWith("a long new password");
    await expect
      .element(page.getByRole("heading", { name: "This link no longer works" }))
      .toBeVisible();

    await router.navigate({ to: "/reset-password", hash: "fresh" });
    await expect
      .element(page.getByRole("heading", { name: "Choose a new password" }))
      .toBeVisible();
    await resetWith("another long password");

    await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
    const sent = fetch.mock.calls.at(-1)?.[1]?.body;
    expect(typeof sent === "string" ? JSON.parse(sent) : sent).toMatchObject({ secret: "fresh" });
  });

  it("start afresh on sign-up with a new link pasted after one ended", async () => {
    const { router, fetch } = await open("/register/complete#old", {
      "/api/v0/registration/links/redeem": () =>
        problem(
          notFound,
          "/api/v0/problems/registration-link-invalid",
          "This link has expired, was already used, or is not valid. Ask for a new one.",
        ),
    });
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
    expect(fetch).toHaveBeenCalledTimes(1);
  });

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
      await open("/verify-email", { "/api/v0/sessions/current": broken });

      await expect
        .element(page.getByRole("heading", { name: "Open the link from your email" }))
        .toBeVisible();
      await expect
        .element(page.getByRole("link", { name: "Ask for a new link in Security" }))
        .toHaveAttribute("href", "/settings/security");
    });

    it("lead home after verifying", async () => {
      await open("/verify-email#secret", {
        "/api/v0/sessions/current": broken,
        "/api/v0/email-verifications/redeem": () => Response.json({ data: { status: "verified" } }),
      });

      await userEvent.click(page.getByRole("button", { name: "Verify email" }));

      await expect
        .element(page.getByRole("heading", { name: "Your email address is verified" }))
        .toBeVisible();
      await expect
        .element(page.getByRole("link", { name: "Go to home" }))
        .toHaveAttribute("href", "/");
    });

    it("link to a new link after a refused one", async () => {
      await open("/verify-email#secret", {
        "/api/v0/sessions/current": broken,
        "/api/v0/email-verifications/redeem": () =>
          problem(
            notFound,
            "/api/v0/problems/email-verification-link-invalid",
            "This link has expired, was already used, or is not valid.",
          ),
      });

      await userEvent.click(page.getByRole("button", { name: "Verify email" }));

      await expect
        .element(page.getByRole("heading", { name: "This link no longer works" }))
        .toBeVisible();
      await expect
        .element(page.getByRole("link", { name: "Ask for a new link in Security" }))
        .toHaveAttribute("href", "/settings/security");
    });
  });

  it("keep a title still when a check finds the form under it", async () => {
    await open("/register", {
      "/api/v0/registration": () => Response.json({ data: { open: true } }),
    });

    await expect.element(page.getByRole("textbox", { name: "Email address" })).toBeVisible();

    const header = page.getByRole("heading", { name: "Create an account" }).element().parentElement;
    expect(header?.getAnimations()).toEqual([]);
    expect(header?.className).not.toContain("animate-in");
  });

  it("say when registration is closed", async () => {
    await open("/register", {
      "/api/v0/registration": () => Response.json({ data: { open: false } }),
    });

    await expect
      .element(page.getByRole("heading", { name: "Registration is closed" }))
      .toHaveFocus();
  });
});
