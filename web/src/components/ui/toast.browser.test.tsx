import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { Button } from "./button";
import { Sheet, SheetTitle } from "./sheet";
import { pointAway, pointTo } from "@/test/pointer";
import { closeToasts, showError, showUndo, Toaster } from "./toast";

const undoTimeout = 10_000;

// Counts the toasts on screen now, without waiting.
function toasts(): number {
  return document.querySelectorAll("[role=alertdialog]").length;
}

// Moves the fake clock by ms and lets React draw what changed.
async function wait(ms: number): Promise<void> {
  vi.advanceTimersByTime(ms);
  await new Promise((resolve) => requestAnimationFrame(resolve));
}

// shown waits until the toast saying text is on screen and its timer
// runs, which React Aria starts in an effect after drawing the toast; a
// clock moved before that would not count.
async function shown(text: string): Promise<void> {
  await expect.element(page.getByText(text)).toBeVisible();
  await expect.poll(() => vi.getTimerCount()).toBeGreaterThan(0);
}

// Host has a button that opens a drawer, beside the toast region.
function Host() {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button onPress={() => setOpen(true)}>Open drawer</Button>
      <Sheet isOpen={open} onOpenChange={setOpen} side="left">
        <SheetTitle>Mail</SheetTitle>
      </Sheet>
      <Toaster />
    </>
  );
}

beforeEach(() => {
  // Only the clocks React Aria's toast timer reads: setTimeout to close,
  // Date to work out the time left after a pause. The runner polls with
  // its own real timers, but moves this clock about 50 ms per 50 ms while
  // a locator waits, so the checks below keep a margin.
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "Date"] });
});

afterEach(() => {
  closeToasts();
  vi.useRealTimers();
});

describe("Toaster", () => {
  it("closes an Undo toast after 10 seconds", async () => {
    await render(
      <>
        <Button>Elsewhere</Button>
        <Toaster />
      </>,
    );
    // A pointer left over the region by an earlier test would pause it.
    await userEvent.hover(page.getByRole("button", { name: "Elsewhere" }));
    showUndo("Conversation archived", () => {});
    await shown("Conversation archived");

    await wait(undoTimeout - 1_000);
    expect(toasts()).toBe(1);
    await wait(1_000);
    expect(toasts()).toBe(0);
  });

  it("closes an Undo toast with its time left once the pointer leaves", async () => {
    await render(
      <>
        <Button>Elsewhere</Button>
        <Toaster />
      </>,
    );
    await userEvent.hover(page.getByRole("button", { name: "Elsewhere" }));
    showUndo("Conversation archived", () => {});
    await shown("Conversation archived");
    await wait(4_000);
    const toast = page.getByText("Conversation archived").element();
    pointTo(toast);
    await wait(60_000);
    expect(toasts()).toBe(1);

    pointAway(toast);
    await wait(5_000);
    expect(toasts()).toBe(1);
    await wait(2_000);
    expect(toasts()).toBe(0);
  });

  it("keeps an Undo toast while the pointer is over it", async () => {
    await render(<Toaster />);
    showUndo("Conversation archived", () => {});
    await shown("Conversation archived");
    pointTo(page.getByText("Conversation archived").element());

    await wait(undoTimeout * 2);
    expect(toasts()).toBe(1);
  });

  it("keeps an Undo toast while focus is in it, which F6 moves to", async () => {
    await render(
      <>
        <Button>Archive</Button>
        <Toaster />
      </>,
    );
    await userEvent.click(page.getByRole("button", { name: "Archive" }));
    showUndo("Conversation archived", () => {});
    await shown("Conversation archived");
    await userEvent.keyboard("{F6}");
    const region = document.activeElement?.closest("[role=region]");
    expect(region).not.toBeNull();
    // The ring shows where focus went: the region itself.
    expect(getComputedStyle(document.activeElement ?? document.body).outlineStyle).toBe("solid");

    await wait(undoTimeout * 2);
    expect(toasts()).toBe(1);
  });

  it("runs Undo and closes the toast", async () => {
    const onUndo = vi.fn<() => void>();
    await render(<Toaster />);
    showUndo("Conversation archived", onUndo);
    await userEvent.click(page.getByRole("button", { name: "Undo" }));

    expect(onUndo).toHaveBeenCalledOnce();
    expect(toasts()).toBe(0);
  });

  it("keeps an error toast until it is closed", async () => {
    await render(<Toaster />);
    showError("Couldn't archive the conversation", "Check your connection, then try again.");
    // On screen first: React Aria starts a toast's timer when it mounts.
    await expect.element(page.getByText("Couldn't archive the conversation")).toBeVisible();
    await wait(60_000);
    expect(toasts()).toBe(1);

    await userEvent.click(page.getByRole("button", { name: "Close" }));
    expect(toasts()).toBe(0);
  });

  it("shows an error that is already showing only once", async () => {
    await render(<Toaster />);
    // A repeated failure, e.g. each action while offline.
    showError("Couldn't archive the conversation", "Check your connection, then try again.");
    showError("Couldn't archive the conversation", "Check your connection, then try again.");
    // The same title for another cause is another error.
    showError("Couldn't archive the conversation", "inbok isn't answering.");
    showError("Couldn't sign out");
    await expect.element(page.getByText("Couldn't sign out")).toBeVisible();
    expect(toasts()).toBe(3);

    // Once closed, it can show again.
    await userEvent.click(page.getByRole("button", { name: "Close" }).last());
    showError("Couldn't archive the conversation", "Check your connection, then try again.");
    await expect.poll(toasts).toBe(3);
  });

  it("shows every toast, newest first, with the dark theme's focus ring", async () => {
    await render(<Toaster />);
    for (const n of [1, 2, 3, 4]) showError(`Failure ${n}`);
    await expect.element(page.getByText("Failure 4")).toBeVisible();

    const titles = [...document.querySelectorAll("[role=alertdialog]")].map((toast) =>
      toast.textContent?.slice(0, 9),
    );
    expect(titles).toEqual(["Failure 4", "Failure 3", "Failure 2", "Failure 1"]);
    // The light theme's primary is too dark for a ring on the dark surface.
    const close = page.getByRole("button", { name: "Close" }).first().element();
    expect(getComputedStyle(close).getPropertyValue("--ring").trim()).toBe("#86a8ff");
  });

  it("rings a focused toast on its own surface, where the ring shows", async () => {
    await render(<Toaster />);
    showError("Couldn't archive the conversation");
    await expect.element(page.getByText("Couldn't archive the conversation")).toBeVisible();
    await userEvent.keyboard("{F6}{Tab}");

    const toast = page.getByRole("alertdialog").element();
    expect(document.activeElement).toBe(toast);
    const style = getComputedStyle(toast);
    expect(style.outlineStyle).toBe("solid");
    expect(Number.parseFloat(style.outlineOffset)).toBeLessThan(0);
  });

  it("stays above a drawer opened after it, so Close still works", async () => {
    await page.viewport(390, 844);
    await render(<Host />);
    // The toast first, then the drawer: later in the page, the drawer's
    // scrim would cover a toast whose layer were no higher.
    showError("Couldn't archive the conversation");
    await expect.element(page.getByText("Couldn't archive the conversation")).toBeVisible();
    await userEvent.click(page.getByRole("button", { name: "Open drawer" }));
    await expect.element(page.getByRole("dialog", { name: "Mail" })).toBeVisible();

    const close = page.getByRole("alertdialog").getByRole("button", { name: "Close" });
    const box = close.element().getBoundingClientRect();
    const top = document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2);
    expect(close.element().contains(top)).toBe(true);
    await userEvent.click(close);
    expect(toasts()).toBe(0);
    await expect.element(page.getByRole("dialog", { name: "Mail" })).toBeVisible();
  });
});
