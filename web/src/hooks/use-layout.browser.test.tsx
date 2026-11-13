import { describe, expect, it } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { useLayout } from "./use-layout";

function Layout() {
  return <output>{useLayout()}</output>;
}

describe("useLayout", () => {
  it.each([
    [1280, "full"],
    [1279, "rail"],
    [1024, "rail"],
    [1023, "drawer"],
    [780, "drawer"],
    [779, "phone"],
  ])("names a %i px window %s", async (width, layout) => {
    await page.viewport(width, 800);
    await render(<Layout />);
    await expect.element(page.getByRole("status")).toHaveTextContent(layout);
  });

  it("follows the window as it resizes", async () => {
    await page.viewport(1440, 900);
    await render(<Layout />);
    await expect.element(page.getByRole("status")).toHaveTextContent("full");
    await page.viewport(390, 844);
    await expect.element(page.getByRole("status")).toHaveTextContent("phone");
  });
});
