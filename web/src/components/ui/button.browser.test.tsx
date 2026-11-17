import { describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { Button } from "./button";
import { Popover, PopoverTrigger } from "./popover";

describe("Button", () => {
  it("keeps focus and ignores presses while pending", async () => {
    const onPress = vi.fn<() => void>();
    const { rerender } = await render(<Button onPress={onPress}>Send again</Button>);
    const button = page.getByRole("button", { name: "Send again" });
    await userEvent.keyboard("{Tab}");
    await rerender(
      <Button isPending onPress={onPress}>
        Send again
      </Button>,
    );
    await userEvent.keyboard("{Enter}");

    await expect.element(button).toHaveFocus();
    expect(onPress).not.toHaveBeenCalled();
  });

  it("shows the focus ring after the keyboard, not after a click", async () => {
    await render(
      <>
        <input aria-label="Name" />
        <Button>Save</Button>
      </>,
    );
    const button = page.getByRole("button", { name: "Save" });
    await userEvent.click(button);
    await expect.element(button).toHaveStyle({ outlineStyle: "none" });

    await userEvent.click(page.getByRole("textbox", { name: "Name" }));
    await userEvent.keyboard("{Tab}");
    // DESIGN.md "Space, size, shape, motion", Focus: a 2 px outline.
    await expect.element(button).toHaveStyle({ outlineStyle: "solid", outlineWidth: "2px" });
  });

  it("scales while a key holds it pressed", async () => {
    await render(<Button>Save</Button>);
    const button = page.getByRole("button", { name: "Save" });
    await userEvent.keyboard("{Tab}");

    await userEvent.keyboard("{ >}");

    // DESIGN.md "Space, size, shape, motion", Press: a pressed button
    // scales to 97%.
    await vi.waitFor(() => expect(getComputedStyle(button.element()).scale).toBe("0.97"));
    await userEvent.keyboard("{/ }");
  });

  it("stands back up while the popover it opened is open", async () => {
    await render(
      <PopoverTrigger>
        <Button variant="outline">Account</Button>
        <Popover aria-label="Account menu">Signed in</Popover>
      </PopoverTrigger>,
    );
    const button = page.getByRole("button", { name: "Account" });
    await userEvent.click(button);
    await expect.element(page.getByRole("dialog", { name: "Account menu" })).toBeVisible();

    await expect.element(button).toHaveAttribute("aria-expanded", "true");
    await vi.waitFor(() => expect(getComputedStyle(button.element()).scale).toBe("none"));
  });
});
