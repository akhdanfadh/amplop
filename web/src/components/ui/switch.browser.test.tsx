import { useState } from "react";
import { describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { Switch } from "./switch";

// Toggle is a switch that follows its own presses.
function Toggle() {
  const [on, setOn] = useState(false);
  return (
    <Switch isSelected={on} onChange={setOn}>
      Open registration
    </Switch>
  );
}

describe("Switch", () => {
  it("is named by its label and toggles on a press of the label", async () => {
    await render(<Toggle />);
    const control = page.getByRole("switch", { name: "Open registration" });
    await userEvent.click(page.getByText("Open registration"));

    await expect.element(control).toBeChecked();
    await expect.element(control).toHaveFocus();
  });

  it("stays focusable and unchanged while read-only", async () => {
    const onChange = vi.fn<(selected: boolean) => void>();
    await render(
      <Switch isReadOnly aria-describedby="why" onChange={onChange}>
        Open registration
      </Switch>,
    );
    const control = page.getByRole("switch", { name: "Open registration" });
    await userEvent.keyboard("{Tab}");
    await expect.element(control).toHaveFocus();
    await userEvent.keyboard(" ");

    expect(onChange).not.toHaveBeenCalled();
    await expect.element(control).not.toBeChecked();
    await expect.element(control).toHaveAttribute("aria-readonly", "true");
  });
});
