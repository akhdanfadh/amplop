import { describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { Button } from "./button";

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
    await expect.element(button).not.toHaveAttribute("data-focus-visible");
    await expect.element(button).toHaveStyle({ outlineStyle: "none" });

    await userEvent.click(page.getByRole("textbox", { name: "Name" }));
    await userEvent.keyboard("{Tab}");
    await expect.element(button).toHaveAttribute("data-focus-visible");
    await expect.element(button).toHaveStyle({ outlineStyle: "solid", outlineWidth: "2px" });
  });
});
