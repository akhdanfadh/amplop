import { describe, expect, it } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRouter,
} from "@tanstack/react-router";
import { NotFound } from "@/components/route-states";
import { PlacePage } from "./place-page";

// app returns a router whose page is the Inbox, with reader in its reading
// pane, opened as pane says.
function app(pane: "list" | "reader") {
  const root = createRootRoute({
    component: () => (
      <PlacePage place="inbox" pane={pane} reader={pane === "reader" ? <NotFound /> : null} />
    ),
  });
  return createRouter({ routeTree: root, history: createMemoryHistory() });
}

// frame lets React render what an effect changed.
function frame(): Promise<void> {
  return new Promise((resolve) =>
    requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
  );
}

describe("PlacePage", () => {
  it("focuses its heading when the list is what opened", async () => {
    await page.viewport(1440, 900);
    await render(<RouterProvider router={app("list")} />);
    await expect.element(page.getByRole("heading", { name: "Inbox", level: 1 })).toHaveFocus();
  });

  it("leaves focus on Page not found in the reading pane", async () => {
    await page.viewport(1440, 900);
    await render(<RouterProvider router={app("reader")} />);
    const notFound = page.getByRole("heading", { name: "Page not found", level: 2 });
    await expect.element(notFound).toHaveFocus();
    await frame();
    await expect.element(notFound).toHaveFocus();
  });
});
