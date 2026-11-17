import { describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { Checkbox } from "./checkbox";

describe("Checkbox", () => {
  it("toggles on a press of its label, keeping its caller's id and description", async () => {
    const onChange = vi.fn<(selected: boolean) => void>();
    await render(
      <>
        <Checkbox id="highlighted" aria-describedby="note" onChange={onChange}>
          Highlighted
        </Checkbox>
        <p id="note">Shown first in the list.</p>
      </>,
    );
    const box = page.getByRole("checkbox", { name: "Highlighted" });
    await userEvent.click(page.getByText("Highlighted"));

    expect(onChange).toHaveBeenCalledWith(true);
    await expect.element(box).toHaveAttribute("id", "highlighted");
    await expect.element(box).toHaveAccessibleDescription("Shown first in the list.");
  });
});
