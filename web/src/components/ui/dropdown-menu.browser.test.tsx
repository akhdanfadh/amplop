import { describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { Button } from "./button";
import { DropdownMenu, DropdownMenuItem, DropdownMenuTrigger } from "./dropdown-menu";

describe("DropdownMenu", () => {
  it.each(["none", "single", "multiple"] as const)(
    "rings the item keyboard focus is on, choosing %s",
    async (selectionMode) => {
      await render(
        <DropdownMenuTrigger>
          <Button>More</Button>
          <DropdownMenu aria-label="More" selectionMode={selectionMode}>
            <DropdownMenuItem id="spam">Report spam</DropdownMenuItem>
            <DropdownMenuItem id="gmail">Open in Gmail</DropdownMenuItem>
          </DropdownMenu>
        </DropdownMenuTrigger>,
      );
      page.getByRole("button", { name: "More" }).element().focus();

      await userEvent.keyboard("{ArrowDown}");

      const item = page.getByRole("menu").getByText("Report spam");
      await expect.element(item).toHaveFocus();
      expect(getComputedStyle(item.element()).outlineStyle).toBe("solid");
    },
  );
});
