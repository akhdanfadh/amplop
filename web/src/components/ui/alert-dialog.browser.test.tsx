import { useState } from "react";
import { beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "./alert-dialog";
import { doubleClickTime } from "@/lib/double-click";
import { Button } from "./button";

// Confirm opens an AlertDialog from a button; onConfirm runs on its action.
function Confirm({ onConfirm }: { onConfirm: () => void }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button onPress={() => setOpen(true)}>Disconnect</Button>
      <AlertDialog isOpen={open} onOpenChange={setOpen}>
        <AlertDialogHeader>
          <AlertDialogTitle>Disconnect Studio?</AlertDialogTitle>
          <AlertDialogDescription>Your mail in Gmail stays as it is.</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction onPress={onConfirm}>Disconnect Studio</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialog>
    </>
  );
}

// The clock the double-click guard reads; a test moves it past the
// double-click time instead of waiting.
let now = 0;

// Shown is an AlertDialog whose isOpen the test sets through rerender.
function Shown({ open, onConfirm }: { open: boolean; onConfirm: () => void }) {
  return (
    <AlertDialog isOpen={open}>
      <AlertDialogHeader>
        <AlertDialogTitle>Disconnect Studio?</AlertDialogTitle>
      </AlertDialogHeader>
      <AlertDialogFooter>
        <AlertDialogCancel>Cancel</AlertDialogCancel>
        <AlertDialogAction onPress={onConfirm}>Disconnect Studio</AlertDialogAction>
      </AlertDialogFooter>
    </AlertDialog>
  );
}

describe("AlertDialog", () => {
  beforeEach(() => {
    now = 0;
    vi.spyOn(performance, "now").mockImplementation(() => now);
  });

  it("names and describes itself and focuses Cancel", async () => {
    await render(<Confirm onConfirm={() => {}} />);
    await userEvent.click(page.getByRole("button", { name: "Disconnect" }));

    const dialog = page.getByRole("alertdialog", { name: "Disconnect Studio?" });
    await expect.element(dialog).toHaveAccessibleDescription("Your mail in Gmail stays as it is.");
    await expect.element(page.getByRole("button", { name: "Cancel" })).toHaveFocus();
    // A tall dialog scrolls in its overlay, and stops there, not the page.
    const overlay = document.querySelector("[data-slot=alert-dialog-overlay]");
    if (!overlay) throw new Error("no overlay");
    expect(getComputedStyle(overlay).overscrollBehaviorY).toBe("contain");
    // The page behind is dimmed by the scrim token, black at 30%.
    expect(getComputedStyle(overlay).backgroundColor).toBe("rgba(0, 0, 0, 0.3)");
  });

  it("runs the action and returns focus to the opening button", async () => {
    const onConfirm = vi.fn<() => void>();
    await render(<Confirm onConfirm={onConfirm} />);
    const opener = page.getByRole("button", { name: "Disconnect" });
    await userEvent.click(opener);
    await expect.element(page.getByRole("alertdialog")).toBeInTheDocument();
    // Past the double-click time, so the press counts.
    now = doubleClickTime;
    await userEvent.click(page.getByRole("button", { name: "Disconnect Studio" }));

    expect(onConfirm).toHaveBeenCalledOnce();
    await expect.element(page.getByRole("alertdialog")).not.toBeInTheDocument();
    await expect.element(opener).toHaveFocus();
  });

  it("ignores the second click of a double-click that opened it", async () => {
    const onConfirm = vi.fn<() => void>();
    await render(<Confirm onConfirm={onConfirm} />);
    const opener = page.getByRole("button", { name: "Disconnect" });
    // Open, then click again at once where the action now is, as the second
    // click of a double-click lands.
    await userEvent.click(opener);
    await userEvent.click(page.getByRole("button", { name: "Disconnect Studio" }));

    expect(onConfirm).not.toHaveBeenCalled();
    await expect.element(page.getByRole("alertdialog")).toBeInTheDocument();
  });

  it("ignores a quick press after reopening while it closed", async () => {
    const onConfirm = vi.fn<() => void>();
    const { rerender } = await render(<Shown open onConfirm={onConfirm} />);
    now = doubleClickTime;
    // Closed and, at once, opened again: the dialog never unmounted.
    await rerender(<Shown open={false} onConfirm={onConfirm} />);
    expect(document.querySelector('[role="alertdialog"]')).not.toBeNull();
    await rerender(<Shown open onConfirm={onConfirm} />);
    await userEvent.click(page.getByRole("button", { name: "Disconnect Studio" }));

    expect(onConfirm).not.toHaveBeenCalled();
  });

  it("keeps a keyboard press at once", async () => {
    const onConfirm = vi.fn<() => void>();
    await render(<Confirm onConfirm={onConfirm} />);
    await userEvent.click(page.getByRole("button", { name: "Disconnect" }));
    await userEvent.keyboard("{Tab}{Enter}");

    expect(onConfirm).toHaveBeenCalledOnce();
  });

  it("leaves closing to the caller when asked", async () => {
    const onConfirm = vi.fn<() => void>();
    function Held() {
      const [open, setOpen] = useState(true);
      return (
        <AlertDialog isOpen={open} onOpenChange={setOpen}>
          <AlertDialogHeader>
            <AlertDialogTitle>Disconnect Studio?</AlertDialogTitle>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction closes="by-caller" onPress={onConfirm}>
              Disconnect Studio
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialog>
      );
    }
    await render(<Held />);
    await userEvent.keyboard("{Tab}{Enter}");

    expect(onConfirm).toHaveBeenCalledOnce();
    // Still open, and not on its way out: the caller closes it, e.g. once
    // the change it confirms is saved. A dialog that closes is marked as
    // exiting, or gone, by the time the press returns.
    expect(document.querySelector('[role="alertdialog"]')).not.toBeNull();
    expect(document.querySelector("[data-exiting]")).toBeNull();
  });

  it("cancels on Escape", async () => {
    const onConfirm = vi.fn<() => void>();
    await render(<Confirm onConfirm={onConfirm} />);
    await userEvent.click(page.getByRole("button", { name: "Disconnect" }));
    await userEvent.keyboard("{Escape}");

    await expect.element(page.getByRole("alertdialog")).not.toBeInTheDocument();
    expect(onConfirm).not.toHaveBeenCalled();
  });
});
