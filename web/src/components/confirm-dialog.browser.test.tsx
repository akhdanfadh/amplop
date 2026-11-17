import { useRef, useState } from "react";
import { describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { pressInDialog } from "@/test/dialog";
import { held } from "@/test/server";
import { ConfirmDialog, type Failure } from "./confirm-dialog";

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

// Change is a page's use of ConfirmDialog: a button that opens the dialog,
// which holds the change save makes until it settles, closes once it is
// saved, and shows why it failed, the error's message, when save rejects.
// With opener "goes" the saved change takes the opening button away, as
// closing a paused registration does, and focus falls back to the link.
function Change({
  save,
  opener = "stays",
}: {
  save: () => Promise<void>;
  opener?: "stays" | "goes";
}) {
  const [open, setOpen] = useState(false);
  const [pending, setPending] = useState(false);
  const [failure, setFailure] = useState<Failure>();
  const [saved, setSaved] = useState(false);
  const linkRef = useRef<HTMLAnchorElement>(null);

  function confirm(): void {
    setPending(true);
    setFailure(undefined);
    save().then(
      () => {
        setPending(false);
        setSaved(true);
        setOpen(false);
      },
      (err: Error) => {
        setPending(false);
        setFailure({ message: err.message, requestId: "req-7" });
      },
    );
  }

  return (
    <>
      {!(saved && opener === "goes") && (
        <button type="button" onClick={() => setOpen(true)}>
          Close registration
        </button>
      )}
      <a ref={linkRef} href="#system-email">
        Go to System email
      </a>
      <ConfirmDialog
        isOpen={open}
        onOpenChange={setOpen}
        title="Close registration?"
        description="Sign-up links that haven't been used stop working."
        action="Close registration"
        working="Closing..."
        pending={pending}
        failure={failure}
        onConfirm={confirm}
        fallbackFocus={() => linkRef.current}
      />
    </>
  );
}

const dialog = () => page.getByRole("alertdialog");
const opener = () => page.getByRole("button", { name: "Close registration" });

describe("ConfirmDialog", () => {
  it("keeps its button's width while the change is saved", async () => {
    // Wide enough for the buttons to sit side by side, as on a desktop.
    await page.viewport(1024, 768);
    const { rerender } = await render(<Held pending={false} />);
    const idle = page.getByRole("button", { name: "Open registration" });
    await expect.element(idle).toBeVisible();
    // The width the layout gives the button, which the dialog's opening
    // zoom does not scale, so the two reads need not wait for it to end.
    const width = getComputedStyle(idle.element()).width;

    await rerender(<Held pending />);

    const working = page.getByRole("button", { name: "Opening..." });
    await expect.element(working).toBeVisible();
    // Only the label changes: nothing moves under the pointer.
    expect(getComputedStyle(working.element()).width).toBe(width);
  });

  it("holds the change until it is saved, then returns focus to its button", async () => {
    const answer = held();
    await render(<Change save={() => answer.promise} />);

    await userEvent.click(opener());
    await pressInDialog("Close registration");
    await expect.element(dialog().getByRole("button", { name: "Closing..." })).toBeVisible();
    await expect.element(dialog().getByRole("button", { name: "Cancel" })).toBeDisabled();
    // A change already sent can't be called back.
    await userEvent.keyboard("{Escape}");
    await expect.element(dialog()).toBeVisible();

    answer.resolve();
    await expect.element(dialog()).not.toBeInTheDocument();
    await expect.element(opener()).toHaveFocus();
  });

  it("shows a failure inside, with its reference, where pressing again retries", async () => {
    const save = vi
      .fn<() => Promise<void>>()
      .mockRejectedValueOnce(new Error("Couldn't close registration."))
      .mockResolvedValue(undefined);
    await render(<Change save={save} />);

    await userEvent.click(opener());
    await pressInDialog("Close registration");
    await expect
      .element(dialog().getByRole("alert"))
      .toHaveTextContent("Couldn't close registration.");
    await expect.element(dialog().getByText("Request reference: req-7")).toBeVisible();
    // The dialog stays, with focus still on the button that failed.
    await expect
      .element(dialog().getByRole("button", { name: "Close registration" }))
      .toHaveFocus();

    await userEvent.keyboard("{Enter}");
    await expect.element(dialog()).not.toBeInTheDocument();
    expect(save).toHaveBeenCalledTimes(2);
  });

  it("can be cancelled once a change failed", async () => {
    const save = vi
      .fn<() => Promise<void>>()
      .mockRejectedValue(new Error("Couldn't close registration."));
    await render(<Change save={save} />);

    await userEvent.click(opener());
    await pressInDialog("Close registration");
    await expect.element(dialog().getByRole("alert")).toBeVisible();
    await userEvent.keyboard("{Escape}");

    await expect.element(dialog()).not.toBeInTheDocument();
    await expect.element(opener()).toHaveFocus();
    expect(save).toHaveBeenCalledTimes(1);
  });

  it("gives focus to the fallback when the change took its button away", async () => {
    await render(<Change save={() => Promise.resolve()} opener="goes" />);

    await userEvent.click(opener());
    await pressInDialog("Close registration");

    await expect.element(dialog()).not.toBeInTheDocument();
    await expect.element(page.getByRole("link", { name: "Go to System email" })).toHaveFocus();
  });
});
