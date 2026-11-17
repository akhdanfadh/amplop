import { describe, expect, it } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { ConfirmDialog } from "./confirm-dialog";

// Held shows a ConfirmDialog whose pending state the test sets.
function Held({ pending }: { pending: boolean }) {
  return (
    <ConfirmDialog
      isOpen
      onOpenChange={() => {}}
      title="Open registration?"
      description="Anyone who can reach this inbok can create a member account."
      action="Open registration"
      working="Opening..."
      pending={pending}
      failure={undefined}
      onConfirm={() => {}}
    />
  );
}

describe("ConfirmDialog", () => {
  it("keeps its button's width while the change is saved", async () => {
    // Wide enough for the buttons to sit side by side, as on a desktop.
    await page.viewport(1024, 768);
    const { rerender } = await render(<Held pending={false} />);
    const idle = page.getByRole("button", { name: "Open registration" });
    await expect.element(idle).toBeVisible();
    // Past the dialog's opening zoom, so widths are at full scale: React
    // Aria marks the dialog as entering until that animation ends.
    await expect.poll(() => document.querySelector("[data-entering]")).toBeNull();
    const width = idle.element().getBoundingClientRect().width;

    await rerender(<Held pending />);

    const working = page.getByRole("button", { name: "Opening..." });
    await expect.element(working).toBeVisible();
    // Only the label changes: nothing moves under the pointer.
    expect(working.element().getBoundingClientRect().width).toBe(width);
  });
});
