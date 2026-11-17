import { describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { lineLink, textLink } from "./link-style";

describe("link styles", () => {
  it("underlines a link in primary, thicker under the pointer, ringed on focus", async () => {
    await render(
      <>
        <a href="#text" className={textLink}>
          sign in
        </a>
        <a href="#line" className={lineLink}>
          Back to sign in
        </a>
      </>,
    );
    const text = page.getByRole("link", { name: "sign in", exact: true }).element();
    const line = page.getByRole("link", { name: "Back to sign in" }).element();

    // The light theme's primary (DESIGN.md "Colour").
    expect(getComputedStyle(text).color).toBe("rgb(37, 87, 214)");
    expect(getComputedStyle(line).color).toBe("rgb(37, 87, 214)");
    // DESIGN.md "Space, size, shape, motion", Links: a link on a line of
    // its own is 44 px tall.
    expect(line.getBoundingClientRect().height).toBe(44);
    expect(getComputedStyle(text).textDecorationLine).toBe("underline");

    // The underline thickens under the pointer, and focus shows the ring.
    await userEvent.hover(text);
    expect(getComputedStyle(text).textDecorationThickness).toBe("2px");
    text.focus();
    await expect.element(page.getByRole("link", { name: "sign in", exact: true })).toHaveStyle({
      outlineStyle: "solid",
    });
  });
});
