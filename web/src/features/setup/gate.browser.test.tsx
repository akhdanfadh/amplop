import { useEffect } from "react";
import { describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { QueryClientProvider } from "@tanstack/react-query";
import {
  Outlet,
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from "@tanstack/react-router";
import { ApiError } from "@/api/transport";
import { testQueryClient } from "@/test/query-client";
import { fakeServer, held, lose } from "@/test/server";
import { SetupGate } from "./gate";

// app returns the gate around a home page, as the root route has it;
// homeCheck is the home page's own beforeLoad, client the query cache.
function app(
  homeCheck: () => void = () => {},
  client = testQueryClient(),
  home = () => <h1>Home</h1>,
) {
  const root = createRootRoute({
    component: () => (
      <QueryClientProvider client={client}>
        <SetupGate>
          <Outlet />
        </SetupGate>
      </QueryClientProvider>
    ),
  });
  const homeRoute = createRoute({
    getParentRoute: () => root,
    path: "/",
    beforeLoad: homeCheck,
    component: home,
  });
  return createRouter({
    routeTree: root.addChildren([homeRoute]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
}

describe("SetupGate", () => {
  it("keeps the page mounted once setup is done, even when every query is reset", async () => {
    // The fake server's own answer: setup is done.
    fakeServer().install();
    const client = testQueryClient();
    let mounts = 0;
    function Home() {
      useEffect(() => {
        mounts += 1;
      }, []);
      return <h1>Home</h1>;
    }
    await render(<RouterProvider router={app(() => {}, client, Home)} />);
    await expect.element(page.getByRole("heading", { name: "Home" })).toBeVisible();

    await client.resetQueries();

    await expect.element(page.getByRole("heading", { name: "Home" })).toBeVisible();
    expect(page.getByRole("status").query()).toBeNull();
    expect(mounts).toBe(1);
  });

  it("keeps the first load's loading view hidden for 300 ms, and leaves focus alone", async () => {
    // The setup check never answers.
    const server = fakeServer().install();
    server.on("GET", "/api/v0/setup", () => new Promise<Response>(() => {}));
    await render(<RouterProvider router={app()} />);
    const loading = page.getByRole("status");
    await expect.element(loading).toHaveTextContent("Loading...");
    // Hidden by CSS for the router's pending delay, then shown (DESIGN.md
    // "States": a loading view appears after 300 ms).
    const [hide] = loading.element().getAnimations();
    expect(hide?.effect?.getTiming().delay).toBe(300);
    await expect.poll(() => getComputedStyle(loading.element()).opacity).toBe("1");
    expect(document.activeElement).not.toBe(loading.element());
  });

  it("swaps a failed check for the loading view on Try again, then opens the page", async () => {
    const server = fakeServer().install();
    const answer = held();
    // The second check waits, then answers that setup is done. Of two
    // routes alike the later answers first, so it is added first.
    server.once("GET", "/api/v0/setup", async (_asked, earlier) => {
      await answer.promise;
      return earlier();
    });
    // The first check never reaches the server.
    server.once("GET", "/api/v0/setup", () => lose());
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

    answer.resolve();
    await expect.element(page.getByRole("heading", { name: "Home" })).toBeVisible();
  });

  it("moves focus to the heading again when the check fails again", async () => {
    const server = fakeServer().install();
    server.on("GET", "/api/v0/setup", () => lose());
    await render(<RouterProvider router={app()} />);

    const heading = page.getByRole("heading", { name: "Couldn't load this page" });
    await expect.element(heading).toHaveFocus();
    await userEvent.click(page.getByRole("button", { name: "Try again" }));
    await expect.element(heading).toHaveFocus();
  });

  it("runs the page's own checks again on Try again", async () => {
    const server = fakeServer().install();
    // The first check never reaches the server; the next finds setup done.
    server.once("GET", "/api/v0/setup", () => lose());
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
