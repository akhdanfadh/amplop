import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { cleanup, render } from "vitest-browser-react";
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRouter,
} from "@tanstack/react-router";
import { NotFound } from "@/components/route-states";
import { clearAccountCache } from "@/features/auth/session-cache";
import { mailApi } from "@/test/mail-api";
import { PlacePage } from "./place-page";

let current: QueryClient | undefined;

afterEach(async () => {
  // Unmounted first, so no list starts reading again once its mail ends.
  await cleanup();
  if (current) clearAccountCache(current);
  current = undefined;
  vi.unstubAllGlobals();
});

// app returns a router whose page is the Inbox, with reader in its reading
// pane, opened as pane says, on an API with no mail.
function app(pane: "list" | "reader") {
  vi.stubGlobal("fetch", vi.fn(mailApi([], []).fetch));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  current = client;
  const root = createRootRoute({
    component: () => (
      <PlacePage
        place="inbox"
        name="Inbox"
        account={undefined}
        openId={undefined}
        pane={pane}
        reader={pane === "reader" ? <NotFound /> : null}
      />
    ),
  });
  return createRouter({
    routeTree: root,
    history: createMemoryHistory(),
    Wrap: ({ children }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>,
  });
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
