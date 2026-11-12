import { describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { Button } from "./button";
import {
  Popover,
  PopoverDescription,
  PopoverHeader,
  PopoverTitle,
  PopoverTrigger,
} from "./popover";

describe("Popover", () => {
  it("heads its content with a title, keeping its own name", async () => {
    await render(
      <PopoverTrigger>
        <Button>Account</Button>
        <Popover aria-label="Account menu">
          <PopoverHeader>
            <PopoverTitle>ana@example.com</PopoverTitle>
            <PopoverDescription>Signed in</PopoverDescription>
          </PopoverHeader>
        </Popover>
      </PopoverTrigger>,
    );
    await userEvent.click(page.getByRole("button", { name: "Account" }));

    const dialog = page.getByRole("dialog", { name: "Account menu" });
    await expect.element(dialog).toBeVisible();
    await expect.element(dialog.getByRole("heading", { name: "ana@example.com" })).toBeVisible();
    await expect.element(dialog.getByText("Signed in")).toBeVisible();
  });
});
