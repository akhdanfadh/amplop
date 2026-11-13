import { describe, expect, it } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRouter,
} from "@tanstack/react-router";
import { PlacePage } from "./place-page";

// app returns a router whose page is the Inbox.
function app() {
  const root = createRootRoute({ component: () => <PlacePage place="inbox" /> });
  return createRouter({ routeTree: root, history: createMemoryHistory() });
}

describe("PlacePage", () => {
  it("focuses its heading when it opens", async () => {
    await page.viewport(1440, 900);
    await render(<RouterProvider router={app()} />);
    await expect.element(page.getByRole("heading", { name: "Inbox", level: 1 })).toHaveFocus();
  });
});
