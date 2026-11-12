import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import {
  HeadContent,
  Outlet,
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from "@tanstack/react-router";
import { z } from "zod";
import { ApiError } from "@/api/transport";
import { NotFound, RouteError, RoutePending } from "./route-states";

// app returns a router with one page at /, whose checks run beforeLoad and
// whose content is Page, opened at address.
function app(beforeLoad: () => void, Page: () => React.ReactNode, address = "/") {
  const root = createRootRoute();
  const home = createRoute({ getParentRoute: () => root, path: "/", beforeLoad, component: Page });
  return createRouter({
    routeTree: root.addChildren([home]),
    history: createMemoryHistory({ initialEntries: [address] }),
    defaultErrorComponent: RouteError,
    defaultNotFoundComponent: NotFound,
  });
}

function Inbox() {
  return <h1>Inbox</h1>;
}

// Broken is a page whose own code fails.
function Broken(): React.ReactNode {
  throw new Error("bug");
}

beforeEach(() => {
  // React reports every error a boundary catches; these tests cause them.
  vi.spyOn(console, "error").mockImplementation(() => {});
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("RouteError", () => {
  it("says what failed, keeps the reference, and runs the page's checks again", async () => {
    const busy = new ApiError("http", {
      method: "GET",
      status: 503,
      problem: { title: "Service busy" },
      requestId: "req-1",
    });
    const check = vi.fn<() => void>().mockImplementationOnce(() => {
      throw busy;
    });
    await render(<RouterProvider router={app(check, Inbox)} />);

    const heading = page.getByRole("heading", { name: "Couldn't load this page" });
    await expect.element(heading).toHaveFocus();
    await expect.element(page.getByText("Service busy. Nothing you saved is lost.")).toBeVisible();
    await expect.element(page.getByText("req-1")).toBeVisible();
    expect(document.title).toBe("Couldn't load this page - inbok");
    // The focused heading announces it; an alert would say it twice.
    expect(document.querySelector("[role=alert]")).toBeNull();

    await userEvent.click(page.getByRole("button", { name: "Try again" }));
    await expect.element(page.getByRole("heading", { name: "Inbox" })).toBeVisible();
    expect(check).toHaveBeenCalledTimes(2);
    // The error's title goes with it.
    expect(document.title).not.toContain("Couldn't load");
  });

  it("gives focus to the loading view while Try again runs the checks", async () => {
    const busy = new ApiError("http", { method: "GET", status: 503, requestId: "req-2" });
    let calls = 0;
    const check = async () => {
      calls += 1;
      if (calls === 1) throw busy;
      await new Promise((resolve) => setTimeout(resolve, 1000));
    };
    const root = createRootRoute();
    const home = createRoute({
      getParentRoute: () => root,
      path: "/",
      beforeLoad: check,
      component: Inbox,
    });
    const router = createRouter({
      routeTree: root.addChildren([home]),
      history: createMemoryHistory({ initialEntries: ["/"] }),
      defaultErrorComponent: RouteError,
      defaultPendingComponent: RoutePending,
      defaultPendingMs: 0,
    });
    await render(<RouterProvider router={router} />);

    const retry = page.getByRole("button", { name: "Try again" });
    await expect.element(retry).toBeVisible();
    retry.element().focus();
    await userEvent.keyboard("{Enter}");
    // The error view is hidden, not gone, while the checks run.
    await expect.element(page.getByRole("status")).toHaveFocus();
    await expect.element(page.getByRole("heading", { name: "Inbox" })).toBeVisible();
  });

  it("offers Reload, and the start page, when the page's own code fails", async () => {
    await render(<RouterProvider router={app(() => {}, Broken)} />);

    await expect
      .element(page.getByRole("heading", { name: "This page stopped working" }))
      .toHaveFocus();
    await expect.element(page.getByRole("button", { name: "Reload" })).toBeVisible();
    // A way out when the same page fails on every load.
    await expect
      .element(page.getByRole("link", { name: "open inbok's start page" }))
      .toHaveAttribute("href", "/");
  });

  it("shows Page not found for an address whose search its route refuses", async () => {
    // e.g. an old link's ?account=abc, which Reload would only repeat.
    const root = createRootRoute();
    const inbox = createRoute({
      getParentRoute: () => root,
      path: "/",
      validateSearch: z.object({ account: z.number().optional() }),
      component: Inbox,
    });
    const router = createRouter({
      routeTree: root.addChildren([inbox]),
      history: createMemoryHistory({ initialEntries: ["/?account=abc"] }),
      defaultErrorComponent: RouteError,
      defaultNotFoundComponent: NotFound,
    });
    await render(<RouterProvider router={router} />);
    await expect.element(page.getByRole("heading", { name: "Page not found" })).toBeVisible();
  });

  it("names the tab for the state, over the page's own title", async () => {
    // The app's head lists each page's title; the state's comes first.
    const root = createRootRoute({
      component: () => (
        <>
          <HeadContent />
          <Outlet />
        </>
      ),
    });
    const inbox = createRoute({
      getParentRoute: () => root,
      path: "/",
      head: () => ({ meta: [{ title: "Inbox - inbok" }] }),
      beforeLoad: () => {
        throw new ApiError("http", { method: "GET", status: 503 });
      },
      component: Inbox,
    });
    const router = createRouter({
      routeTree: root.addChildren([inbox]),
      history: createMemoryHistory({ initialEntries: ["/"] }),
      defaultErrorComponent: RouteError,
    });
    await render(<RouterProvider router={router} />);
    await expect
      .element(page.getByRole("heading", { name: "Couldn't load this page" }))
      .toBeVisible();
    await expect.poll(() => document.title).toBe("Couldn't load this page - inbok");
  });
});

// slowApp returns a router at address, which draws Elsewhere beside its
// pages, with a page at /members whose checks take a second.
function slowApp(address: string) {
  const root = createRootRoute({
    component: () => (
      <>
        <button type="button">Elsewhere</button>
        <Outlet />
      </>
    ),
  });
  const home = createRoute({ getParentRoute: () => root, path: "/", component: Inbox });
  const slow = createRoute({
    getParentRoute: () => root,
    path: "/members",
    beforeLoad: () => new Promise((resolve) => setTimeout(resolve, 1000)),
    component: () => <h1>Slow</h1>,
  });
  return createRouter({
    routeTree: root.addChildren([home, slow]),
    history: createMemoryHistory({ initialEntries: [address] }),
    defaultPendingComponent: RoutePending,
    defaultPendingMs: 0,
  });
}

describe("RoutePending", () => {
  it("shows the first load's view 300 ms after the page's start, and leaves focus alone", async () => {
    await render(<RouterProvider router={slowApp("/members")} />);
    const loading = page.getByRole("status");
    await expect.element(loading).toHaveTextContent("Loading...");
    // The test page started long ago, so the view shows at once, as it
    // does after a setup check that took over 300 ms.
    expect(loading.element()).toHaveClass("pending-delay");
    const [hide] = loading.element().getAnimations();
    expect(hide?.effect?.getTiming().delay).toBe(0);
    expect(document.activeElement).not.toBe(loading.element());
  });

  it("shows a later load's view at once, focus kept where it is shown", async () => {
    const router = slowApp("/");
    await render(<RouterProvider router={router} />);
    await expect.element(page.getByRole("heading", { name: "Inbox" })).toBeVisible();
    page.getByRole("button", { name: "Elsewhere" }).element().focus();

    void router.navigate({ to: "/members" });
    const loading = page.getByRole("status");
    await expect.element(loading).toBeVisible();
    expect(loading.element().getAnimations()).toHaveLength(0);
    await expect.element(page.getByRole("button", { name: "Elsewhere" })).toHaveFocus();
  });
});

describe("NotFound", () => {
  it("names the page and links to the start", async () => {
    await render(<RouterProvider router={app(() => {}, Inbox, "/nope")} />);

    await expect.element(page.getByRole("heading", { name: "Page not found" })).toHaveFocus();
    await expect
      .element(page.getByRole("link", { name: "open inbok's start page" }))
      .toHaveAttribute("href", "/");
    expect(document.title).toBe("Page not found - inbok");
  });
});
