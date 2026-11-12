import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  Outlet,
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from "@tanstack/react-router";
import { ApiError } from "@/api/transport";
import { SetupGate } from "./gate";

// setDone answers the setup check: inbok is set up.
function setDone(): Response {
  return new Response(JSON.stringify({ data: { status: "done" } }), {
    headers: { "Content-Type": "application/json" },
  });
}

// app returns the gate around a home page, as the root route has it;
// homeCheck is the home page's own beforeLoad.
function app(homeCheck: () => void = () => {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const root = createRootRoute({
    component: () => (
      <QueryClientProvider client={client}>
        <SetupGate>
          <Outlet />
        </SetupGate>
      </QueryClientProvider>
    ),
  });
  const home = createRoute({
    getParentRoute: () => root,
    path: "/",
    beforeLoad: homeCheck,
    component: () => <h1>Home</h1>,
  });
  return createRouter({
    routeTree: root.addChildren([home]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("SetupGate", () => {
  it("keeps the first load's loading view hidden for 300 ms, and leaves focus alone", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn<typeof fetch>(() => new Promise(() => {})),
    );
    await render(<RouterProvider router={app()} />);
    const loading = page.getByRole("status");
    await expect.element(loading).toHaveTextContent("Loading...");
    // Hidden by CSS for the router's pending delay, then shown.
    const [hide] = loading.element().getAnimations();
    expect(hide?.effect?.getTiming().delay).toBe(300);
    await expect.poll(() => getComputedStyle(loading.element()).opacity).toBe("1");
    expect(document.activeElement).not.toBe(loading.element());
  });

  it("swaps a failed check for the loading view on Try again, then opens the page", async () => {
    let answer: ((response: Response) => void) | undefined;
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockRejectedValueOnce(new TypeError("Offline"))
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            answer = resolve;
          }),
      );
    vi.stubGlobal("fetch", fetchMock);
    await render(<RouterProvider router={app()} />);

    const heading = page.getByRole("heading", { name: "Couldn't load this page" });
    await expect.element(heading).toHaveFocus();
    expect(document.title).toBe("Couldn't load this page - inbok");
    await userEvent.keyboard("{Tab}");
    await userEvent.keyboard("{Enter}");
    // At once, with focus, so the pressed button's going loses nothing.
    const loading = page.getByRole("status");
    await expect.element(loading).toHaveFocus();
    expect(loading.element().getAnimations()).toHaveLength(0);

    answer?.(setDone());
    await expect.element(page.getByRole("heading", { name: "Home" })).toBeVisible();
  });

  it("moves focus to the heading again when the check fails again", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn<typeof fetch>(() => Promise.reject(new TypeError("Offline"))),
    );
    await render(<RouterProvider router={app()} />);

    const heading = page.getByRole("heading", { name: "Couldn't load this page" });
    await expect.element(heading).toHaveFocus();
    await userEvent.click(page.getByRole("button", { name: "Try again" }));
    await expect.element(heading).toHaveFocus();
  });

  it("runs the page's own checks again on Try again", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn<typeof fetch>()
        .mockRejectedValueOnce(new TypeError("Offline"))
        .mockResolvedValueOnce(setDone()),
    );
    // The page's check fails behind the setup check's failure, once.
    let homeFails = true;
    await render(
      <RouterProvider
        router={app(() => {
          if (homeFails) {
            homeFails = false;
            throw new ApiError("network", { method: "GET" });
          }
        })}
      />,
    );

    await userEvent.click(page.getByRole("button", { name: "Try again" }));
    await expect.element(page.getByRole("heading", { name: "Home" })).toBeVisible();
  });
});
