import { describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { Input } from "./input";
import { InputGroup, InputGroupInput } from "./input-group";
import { Textarea } from "./textarea";

describe("Input", () => {
  it("shows the focus ring when clicked, as a text field should", async () => {
    await render(<Input aria-label="Email" />);
    const input = page.getByRole("textbox", { name: "Email" });
    await userEvent.click(input);

    await expect.element(input).toHaveStyle({ outlineStyle: "solid", outlineWidth: "2px" });
  });

  it("shows the focus ring on a clicked textarea too", async () => {
    await render(<Textarea aria-label="Message" />);
    const textarea = page.getByRole("textbox", { name: "Message" });
    await userEvent.click(textarea);

    await expect.element(textarea).toHaveStyle({ outlineStyle: "solid", outlineWidth: "2px" });
  });

  it("draws the ring around an input group, not inside it", async () => {
    await render(
      <InputGroup data-testid="group">
        <InputGroupInput aria-label="Password" />
      </InputGroup>,
    );
    await userEvent.click(page.getByRole("textbox", { name: "Password" }));

    await expect.element(page.getByTestId("group")).toHaveStyle({ outlineStyle: "solid" });
    await expect
      .element(page.getByRole("textbox", { name: "Password" }))
      .toHaveStyle({ outlineStyle: "none" });
  });
});
