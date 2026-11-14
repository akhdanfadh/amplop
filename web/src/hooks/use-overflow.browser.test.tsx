import { describe, expect, it } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { useOverflow } from "./use-overflow";

// Name shows text cut short with an ellipsis in a box width wide, and says
// whether it is cut.
function Name({ text, width }: { text: string; width: number }) {
  const [ref, cut] = useOverflow(text);
  return (
    <p style={{ width }}>
      <span ref={ref} className="block truncate">
        {text}
      </span>
      <output>{cut ? "cut" : "whole"}</output>
    </p>
  );
}

describe("useOverflow", () => {
  it("tells a cut name from a whole one, and follows a resize", async () => {
    const name = "Aleksandra Wiśniewska-Kowalczyk, Northwind Industries Holdings";
    const { rerender } = await render(<Name text={name} width={120} />);
    await expect.element(page.getByRole("status")).toHaveTextContent("cut");

    await rerender(<Name text="Jo" width={120} />);
    await expect.element(page.getByRole("status")).toHaveTextContent("whole");

    await rerender(<Name text={name} width={900} />);
    await expect.element(page.getByRole("status")).toHaveTextContent("whole");
  });
});
