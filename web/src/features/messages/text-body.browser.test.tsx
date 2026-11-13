import { describe, expect, it } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { TextBody } from "./text-body";

describe("TextBody", () => {
  it("shows a body's web address as one of the app's links, opening a new tab", async () => {
    await render(<TextBody text="The agenda is at example.com/agenda." />);
    const link = page.getByRole("link", { name: "example.com/agenda" });

    await expect.element(link).toHaveAttribute("href", "https://example.com/agenda");
    await expect.element(link).toHaveAttribute("target", "_blank");
    // The light theme's primary (DESIGN.md "Links").
    expect(getComputedStyle(link.element()).color).toBe("rgb(37, 87, 214)");
  });
});
