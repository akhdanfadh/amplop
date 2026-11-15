import { describe, expect, it } from "vite-plus/test";
import { cdp, page } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { Spinner } from "./spinner";

describe("Spinner", () => {
  it("fades in and out instead of turning with reduced motion", async () => {
    await render(<Spinner data-testid="spinner" />);
    const spinner = page.getByTestId("spinner");
    await expect.element(spinner).toHaveStyle({ animationName: "spin" });
    const session = cdp();
    await session.send("Emulation.setEmulatedMedia", {
      features: [{ name: "prefers-reduced-motion", value: "reduce" }],
    });
    try {
      await expect.element(spinner).toBeVisible();
      await expect.element(spinner).toHaveStyle({ animationName: "pulse" });
    } finally {
      await session.send("Emulation.setEmulatedMedia", { features: [] });
    }
  });
});
