import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { EditorCard } from "./editor";

afterEach(() => {
  vi.restoreAllMocks();
});

describe("EditorCard", () => {
  it("sends on Ctrl+Enter after the field's own key handling", async () => {
    const order: string[] = [];
    vi.spyOn(HTMLFormElement.prototype, "requestSubmit").mockImplementation(() => {
      order.push("submit");
    });
    await render(
      <form aria-label="Message">
        <EditorCard send={<button type="submit">Send</button>}>
          <input
            aria-label="To"
            // As a recipient row keeps its typed text as chips.
            onKeyDown={(event) => event.key === "Enter" && order.push("field")}
          />
        </EditorCard>
      </form>,
    );

    await userEvent.click(page.getByRole("textbox", { name: "To" }));
    await userEvent.keyboard("{Control>}{Enter}{/Control}");

    expect(order).toEqual(["field", "submit"]);
  });
});
