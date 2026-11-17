import { RouterProvider } from "@tanstack/react-router";
import { describe, expect, it } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { appRouter } from "@/test/app-router";

// What every signed-out page does (SignedOutPage), shown on one of them.
// Each page's own flows sit with its feature, e.g.
// features/password-reset/pages.browser.test.tsx.

describe("signed-out pages", () => {
  it("open with focus on their title", async () => {
    const router = await appRouter("/forgot-password");
    await render(<RouterProvider router={router} />);

    await expect.element(page.getByRole("heading", { name: "Reset your password" })).toHaveFocus();
  });
});
