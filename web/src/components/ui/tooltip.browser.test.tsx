import { describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { Button } from "./button";
import { fakeClock } from "@/test/clock";
import { pointAway, pointTo } from "@/test/pointer";
import { Tooltip, TooltipTrigger } from "./tooltip";

// Rail is a line of words, "Away", over two icon buttons named by tooltips,
// as the rail's are; buttons says whether they are drawn yet.
function Rail({ buttons }: { buttons: "drawn" | "not yet" }) {
  return (
    <>
      <p>Away</p>
      {buttons === "drawn" && (
        <>
          <TooltipTrigger>
            <Button aria-label="Inbox">I</Button>
            <Tooltip>Inbox</Tooltip>
          </TooltipTrigger>
          <TooltipTrigger>
            <Button aria-label="Sent">S</Button>
            <Tooltip>Sent</Tooltip>
          </TooltipTrigger>
        </>
      )}
    </>
  );
}

// tooltip returns the words of the tooltip shown, not one still closing.
function tooltip(): string | null {
  return document.querySelector("[role=tooltip]:not([data-exiting])")?.textContent ?? null;
}

describe("Tooltip", () => {
  it("waits for the pointer to rest, then names the next icon at once", async () => {
    // The real pointer rests on "Away" before the buttons are drawn: one an
    // earlier test left over a button would start React Aria's warm-up,
    // which every tooltip shares. The click also puts React Aria in pointer
    // mode, where hovering shows tooltips.
    const screen = await render(<Rail buttons="not yet" />);
    await userEvent.click(page.getByText("Away"));
    // Nothing below waits on Playwright, so the clock moves only here.
    const clock = fakeClock();
    await screen.rerender(<Rail buttons="drawn" />);
    pointTo(page.getByRole("button", { name: "Inbox" }).element());
    await clock.advance(499);
    // A pointer only passing by shows nothing.
    expect(tooltip()).toBeNull();
    await clock.advance(1);
    expect(tooltip()).toBe("Inbox");

    // Warmed up: the next one shows without the wait.
    pointAway(page.getByRole("button", { name: "Inbox" }).element());
    pointTo(page.getByRole("button", { name: "Sent" }).element());
    await clock.advance(0);
    expect(tooltip()).toBe("Sent");
  });

  it("shows at once on keyboard focus", async () => {
    const clock = fakeClock();
    await render(<Rail buttons="drawn" />);
    // A key press makes React Aria treat the next focus as the keyboard's.
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", bubbles: true }));
    page.getByRole("button", { name: "Inbox" }).element().focus();
    await clock.advance(0);
    expect(tooltip()).toBe("Inbox");
  });
});
