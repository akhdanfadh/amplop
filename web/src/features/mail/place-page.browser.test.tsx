import type { QueryClient } from "@tanstack/react-query";
import { afterEach, describe, expect, it } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { cleanup } from "vitest-browser-react";
import { NotFound } from "@/components/route-states";
import { clearAccountCache } from "@/features/auth/session-cache";
import { mailApi } from "@/test/mail-api";
import { stubRouter } from "@/test/stub-router";
import { PlacePage } from "./place-page";

let current: QueryClient | undefined;

afterEach(async () => {
  // Unmounted first, so no list starts reading again once its mail ends.
  await cleanup();
  if (current) clearAccountCache(current);
  current = undefined;
});

// show renders the Inbox's page, with Page not found in its reading pane
// when pane is "reader", opened as pane says, on an API with no mail.
async function show(pane: "list" | "reader") {
  mailApi([], []).server.install();
  const { client } = await stubRouter(
    <PlacePage
      place="inbox"
      name="Inbox"
      account={undefined}
      openId={undefined}
      pane={pane}
      reader={pane === "reader" ? <NotFound /> : null}
    />,
  );
  current = client;
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
    await show("list");
    await expect.element(page.getByRole("heading", { name: "Inbox", level: 1 })).toHaveFocus();
  });

  it("leaves focus on Page not found in the reading pane", async () => {
    await page.viewport(1440, 900);
    await show("reader");
    const notFound = page.getByRole("heading", { name: "Page not found", level: 2 });
    await expect.element(notFound).toHaveFocus();
    await frame();
    await expect.element(notFound).toHaveFocus();
  });
});
