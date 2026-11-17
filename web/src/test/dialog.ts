import { expect } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";

// pressInDialog presses the button named exactly name in the open alert
// dialog, e.g. the confirming "Discard": it waits for the button to show,
// focuses it, and presses Enter.
//
// DESIGN: it presses by keyboard, the one way past the dialog's guard
// without a wait. The confirming button ignores a mouse or touch press
// that starts within a double-click (doubleClickTime, 500 ms) of the
// dialog opening, and keeps a keyboard press (AlertDialogAction in
// components/ui/alert-dialog.tsx). The guard reads performance.now(),
// which fakeClock leaves real, so a test cannot move a clock past it, and
// a click would have to sleep through it. The guard's own tests, in
// components/ui/alert-dialog.browser.test.tsx, click.
export async function pressInDialog(name: string): Promise<void> {
  const button = page.getByRole("alertdialog").getByRole("button", { name, exact: true });
  await expect.element(button).toBeVisible();
  button.element().focus();
  await expect.element(button).toHaveFocus();
  await userEvent.keyboard("{Enter}");
}
