import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import type { QueryClient } from "@tanstack/react-query";
import { testQueryClient } from "@/test/query-client";
import {
  RouterProvider,
  createBrowserHistory,
  createRoute,
  createRootRouteWithContext,
  createRouter,
  linkOptions,
} from "@tanstack/react-router";
import { Link } from "react-aria-components";
import { showError } from "@/components/ui/toast";
import { Route as appRoot } from "@/routes/__root";

// Page links to another page in the app, once adding to the history and
// once replacing its entry, and to a place on the page, as the app's
// links do.
function Page() {
  return (
    <>
      <h1>Members</h1>
      <Link href={linkOptions({ to: "/settings" })}>Settings</Link>
      <Link href={linkOptions({ to: "/settings" })} routerOptions={{ replace: true }}>
        Settings, in place
      </Link>
      <Link href="#top">Top</Link>
      {/* @ts-expect-error A path written as a string, which nothing checks. */}
      <Link href="/settings">Unchecked</Link>
    </>
  );
}

// The app's root component in a root route of its own: the app's root
// route also renders the HTML document, which a test page cannot hold.
// It lives outside src/routes, which the router plugin reads.
const rootRoute = createRootRouteWithContext<{ queryClient: QueryClient }>()({
  component: appRoot.options.component,
});

// setup returns a router at /members, with a settings page, on the
// browser's own history.
function setup() {
  const members = createRoute({
    getParentRoute: () => rootRoute,
    path: "/members",
    component: Page,
  });
  const settings = createRoute({
    getParentRoute: () => rootRoute,
    path: "/settings",
    component: () => <h1>Settings page</h1>,
  });
  return createRouter({
    routeTree: rootRoute.addChildren([members, settings]),
    history: createBrowserHistory(),
    context: { queryClient: testQueryClient() },
  });
}

const testAddress = location.pathname + location.search;
beforeEach(() => history.replaceState(null, "", "/members"));
afterEach(() => {
  vi.restoreAllMocks();
  // React Aria rereads the browser's language, unmocked again.
  window.dispatchEvent(new Event("languagechange"));
  history.replaceState(null, "", testAddress);
});

describe("root route", () => {
  it("opens a React Aria link in the app, its address shown on the link", async () => {
    const router = setup();
    await render(<RouterProvider router={router} />);
    const settings = page.getByRole("link", { name: "Settings", exact: true });
    // The <a> carries the real address, e.g. for opening it in a new tab.
    await expect.element(settings).toHaveAttribute("href", "/settings");
    const entries = history.length;

    await userEvent.click(settings);
    await expect.element(page.getByRole("heading", { name: "Settings page" })).toBeVisible();
    expect(router.state.location.pathname).toBe("/settings");
    expect(history.length).toBe(entries + 1);
  });

  it("leaves a link to a place on the page to the browser", async () => {
    await render(<RouterProvider router={setup()} />);
    const top = page.getByRole("link", { name: "Top" });
    await expect.element(top).toHaveAttribute("href", "#top");
    await userEvent.click(top);
    expect(location.hash).toBe("#top");
  });

  it("opens a link in place of the current history entry when the link asks", async () => {
    const router = setup();
    await render(<RouterProvider router={router} />);
    const entries = history.length;
    await userEvent.click(page.getByRole("link", { name: "Settings, in place" }));
    await expect.element(page.getByRole("heading", { name: "Settings page" })).toBeVisible();
    expect(history.length).toBe(entries);
  });

  it("holds the app's toast region", async () => {
    await render(<RouterProvider router={setup()} />);
    await expect.element(page.getByRole("heading", { name: "Members" })).toBeVisible();
    showError("Couldn't archive the conversation");
    await expect.element(page.getByRole("alertdialog")).toBeVisible();
  });

  it("keeps React Aria's words and direction English in a browser set to Arabic", async () => {
    await render(<RouterProvider router={setup()} />);
    await expect.element(page.getByRole("heading", { name: "Members" })).toBeVisible();
    // React Aria rereads the browser's language on this event, while one
    // of its parts is on screen.
    vi.spyOn(navigator, "language", "get").mockReturnValue("ar-EG");
    window.dispatchEvent(new Event("languagechange"));
    showError("Couldn't archive the conversation");
    const toast = page.getByRole("alertdialog");
    await expect.element(toast.getByRole("button", { name: "Close" })).toBeVisible();
    expect(toast.element().closest("[dir]")?.getAttribute("dir")).toBe("ltr");
  });
});
