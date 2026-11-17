import { describe, expect, it } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { NotFound } from "@/components/route-states";
import { mailApi } from "@/test/mail-api";
import { stubRouter } from "@/test/stub-router";
import { PlacePage } from "./place-page";

// show renders the Inbox's page, with Page not found in its reading pane
// when pane is "reader", opened as pane says, on an API with no mail.
async function show(pane: "list" | "reader") {
  mailApi([], []).server.install();
  await stubRouter(
    <PlacePage
      place="inbox"
      name="Inbox"
      account={undefined}
      openId={undefined}
      pane={pane}
      reader={pane === "reader" ? <NotFound /> : null}
    />,
  );
}

// frame lets React render what an effect changed.
function frame(): Promise<void> {
  return new Promise((resolve) =>
    requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
  );
}

describe("PlacePage", () => {
  it("focuses its heading when the list is what opened", async () => {
    await show("list");
    await expect.element(page.getByRole("heading", { name: "Inbox", level: 1 })).toHaveFocus();
  });

  it("leaves focus on Page not found in the reading pane", async () => {
    await show("reader");
    const notFound = page.getByRole("heading", { name: "Page not found", level: 2 });
    await expect.element(notFound).toHaveFocus();
    await frame();
    await expect.element(notFound).toHaveFocus();
  });
});
