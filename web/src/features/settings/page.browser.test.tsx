import { describe, expect, it } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { SettingsPage, type NoticeTone } from "./page";

// open renders a settings page whose notice is text in tone.
async function open(text: string, tone?: NoticeTone) {
  await render(
    <SettingsPage title="Connected accounts" lead="The accounts." notice={text} noticeTone={tone}>
      <p>Content</p>
    </SettingsPage>,
  );
}

describe("SettingsPage's notice", () => {
  it("reports news politely, success by default", async () => {
    await open("Your Gmail account is connected.");

    await expect
      .element(page.getByRole("status"))
      .toHaveTextContent("Your Gmail account is connected.");
    expect(page.getByRole("alert").elements()).toHaveLength(0);
  });

  it("reports news that changed nothing politely too", async () => {
    await open("Connecting was cancelled, so nothing changed.", "neutral");

    await expect
      .element(page.getByRole("status"))
      .toHaveTextContent("Connecting was cancelled, so nothing changed.");
  });

  it("interrupts with a problem to fix", async () => {
    await open("Gmail wasn't connected. Connect again.", "problem");

    await expect
      .element(page.getByRole("alert"))
      .toHaveTextContent("Gmail wasn't connected. Connect again.");
    // Its polite region stays, empty, for the next notice.
    await expect.element(page.getByRole("status")).toHaveTextContent("");
  });
});
