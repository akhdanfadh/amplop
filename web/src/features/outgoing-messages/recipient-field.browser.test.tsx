import { useState } from "react";
import { describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { RecipientField } from "./recipient-field";

const none: string[] = [];
function ignore(): void {}

// Field renders a To field that starts with initial and reports each list
// it is given to onChange.
function Field({
  initial = none,
  onChange = ignore,
  onBlur = ignore,
  readOnly = false,
}: {
  initial?: string[];
  onChange?: (value: string[]) => void;
  onBlur?: () => void;
  readOnly?: boolean;
}) {
  const [value, setValue] = useState(initial);
  return (
    <>
      <label htmlFor="to">To</label>
      <RecipientField
        id="to"
        label="To"
        value={value}
        onChange={(next) => {
          setValue(next);
          onChange(next);
        }}
        onBlur={onBlur}
        readOnly={readOnly}
      />
      <button type="button">Elsewhere</button>
    </>
  );
}

const input = () => page.getByRole("textbox", { name: "To" });
const chips = () => page.getByRole("row");

describe("RecipientField", () => {
  it("turns an address into a chip on comma, semicolon, Enter, and leaving", async () => {
    const onChange = vi.fn<(value: string[]) => void>();
    await render(<Field onChange={onChange} />);

    await userEvent.click(input());
    await userEvent.keyboard("ana@example.com,bo@example.com;cy@example.com{Enter}di@example.com");
    await userEvent.click(page.getByRole("button", { name: "Elsewhere" }));

    expect(onChange).toHaveBeenLastCalledWith([
      "ana@example.com",
      "bo@example.com",
      "cy@example.com",
      "di@example.com",
    ]);
    await expect.element(chips()).toHaveLength(4);
    await expect.element(input()).toHaveValue("");
  });

  it("splits a pasted list into chips", async () => {
    const onChange = vi.fn<(value: string[]) => void>();
    await render(<Field onChange={onChange} />);

    await userEvent.click(input());
    // userEvent.paste only pastes what was copied, so the event carries the list.
    const data = new DataTransfer();
    data.setData("text", "Ana <ana@example.com>, bo@example.com\ncy@example.com");
    input()
      .element()
      .dispatchEvent(new ClipboardEvent("paste", { clipboardData: data, bubbles: true }));

    expect(onChange).toHaveBeenLastCalledWith([
      "ana@example.com",
      "bo@example.com",
      "cy@example.com",
    ]);
  });

  it("removes a chip by its button, by Delete on it, and the last by Backspace", async () => {
    const onChange = vi.fn<(value: string[]) => void>();
    await render(
      <Field
        initial={["ana@example.com", "bo@example.com", "cy@example.com"]}
        onChange={onChange}
      />,
    );

    await userEvent.click(page.getByRole("button", { name: "Remove bo@example.com" }));
    expect(onChange).toHaveBeenLastCalledWith(["ana@example.com", "cy@example.com"]);

    await userEvent.click(input());
    await userEvent.keyboard("{Backspace}");
    expect(onChange).toHaveBeenLastCalledWith(["ana@example.com"]);

    await userEvent.keyboard("{Shift>}{Tab}{/Shift}");
    await expect.element(page.getByRole("row", { name: "ana@example.com" })).toHaveFocus();
    await userEvent.keyboard("{Delete}");
    expect(onChange).toHaveBeenLastCalledWith([]);
  });

  it("marks an address that isn't one", async () => {
    await render(<Field initial={["ana@example.com", "bo@"]} />);

    await expect
      .element(page.getByRole("row", { name: "bo@, not an email address" }))
      .toBeInTheDocument();
    await expect.element(page.getByRole("row", { name: "ana@example.com" })).toBeInTheDocument();
  });

  it("reports leaving the field, after keeping what was typed", async () => {
    const onChange = vi.fn<(value: string[]) => void>();
    const onBlur = vi.fn<() => void>();
    await render(<Field onChange={onChange} onBlur={onBlur} />);

    await userEvent.click(input());
    await userEvent.keyboard("ana@example.com");
    await userEvent.click(page.getByRole("button", { name: "Elsewhere" }));

    expect(onChange).toHaveBeenLastCalledWith(["ana@example.com"]);
    expect(onBlur).toHaveBeenCalledOnce();
  });

  it("takes no paste while read-only, e.g. while sending", async () => {
    const onChange = vi.fn<(value: string[]) => void>();
    await render(<Field initial={["ana@example.com"]} onChange={onChange} readOnly />);

    await userEvent.click(input());
    const data = new DataTransfer();
    data.setData("text", "bo@example.com, cy@example.com");
    input()
      .element()
      .dispatchEvent(new ClipboardEvent("paste", { clipboardData: data, bubbles: true }));

    expect(onChange).not.toHaveBeenCalled();
  });
});
