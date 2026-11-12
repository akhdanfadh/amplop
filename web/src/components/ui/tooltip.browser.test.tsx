import { describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { Button } from "./button";
import { Tooltip, TooltipTrigger } from "./tooltip";

// Two icon buttons named by tooltips, as the rail's are.
function Rail() {
  return (
    <>
      <TooltipTrigger>
        <Button aria-label="Inbox">I</Button>
        <Tooltip>Inbox</Tooltip>
      </TooltipTrigger>
      <TooltipTrigger>
        <Button aria-label="Sent">S</Button>
        <Tooltip>Sent</Tooltip>
      </TooltipTrigger>
      <p>Away</p>
    </>
  );
}

// wait lets ms pass.
function wait(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function tooltip(): string | null {
  return document.querySelector("[role=tooltip]")?.textContent ?? null;
}

describe("Tooltip", () => {
  it("waits for the pointer to rest, then names the next icon at once", async () => {
    await render(<Rail />);
    // React Aria shows tooltips on hover once the pointer is in use.
    await userEvent.click(page.getByText("Away"));
    await userEvent.hover(page.getByRole("button", { name: "Inbox" }));
    await wait(200);
    // A pointer only passing by shows nothing.
    expect(tooltip()).toBeNull();
    await expect.poll(tooltip).toBe("Inbox");

    // Warmed up: the next one shows without the wait.
    await userEvent.hover(page.getByRole("button", { name: "Sent" }));
    await wait(100);
    expect(tooltip()).toBe("Sent");
  });

  it("shows at once on keyboard focus", async () => {
    await render(<Rail />);
    await userEvent.keyboard("{Tab}");
    await wait(100);
    expect(tooltip()).toBe("Inbox");
  });
});
