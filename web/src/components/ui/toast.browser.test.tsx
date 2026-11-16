import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { Button } from "./button";
import { Sheet, SheetTitle } from "./sheet";
import { closeAbout, closeToasts, showError, Toaster } from "./toast";

// Counts the toasts on screen now, without waiting.
function toasts(): number {
  return document.querySelectorAll("[role=alertdialog]").length;
}

// toastSpace returns the space the toasts take, as Toaster sets it.
function toastSpace(): string {
  return document.documentElement.style.getPropertyValue("--toast-space");
}

// Moves the fake clock by ms and lets React draw what changed.
async function wait(ms: number): Promise<void> {
  vi.advanceTimersByTime(ms);
  await new Promise((resolve) => requestAnimationFrame(resolve));
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
  it("is reached by F6, its region taking focus", async () => {
    await render(
      <>
        <Button>Archive</Button>
        <Toaster />
      </>,
    );
    await userEvent.click(page.getByRole("button", { name: "Archive" }));
    showError("Couldn't archive the conversation");
    await expect.element(page.getByText("Couldn't archive the conversation")).toBeVisible();
    await userEvent.keyboard("{F6}");

    const region = document.activeElement?.closest("[role=region]");
    expect(region).not.toBeNull();
    // The ring shows where focus went: the region itself.
    expect(getComputedStyle(document.activeElement ?? document.body).outlineStyle).toBe("solid");
  });

  it("gives scrolling boxes the space the toasts take, until they close", async () => {
    await render(<Toaster />);
    showError("Couldn't archive the conversation");

    await expect.poll(() => Number.parseFloat(toastSpace())).toBeGreaterThan(0);
    closeToasts();
    await expect.poll(toastSpace).toBe("");
  });

  it("stands above the Undo bar while it shows", async () => {
    // As the Undo bar sets it (features/mail/undo.tsx).
    document.documentElement.style.setProperty("--undo-space", "100px");
    try {
      await render(<Toaster />);
      showError("Couldn't archive the conversation");
      const toast = page.getByRole("alertdialog");
      await expect.element(toast).toBeVisible();
      const region = toast.element().closest("[role=region]") ?? toast.element();
      // 16 px above the bar, as above the window's edge without one.
      expect(window.innerHeight - region.getBoundingClientRect().bottom).toBe(116);
    } finally {
      document.documentElement.style.removeProperty("--undo-space");
    }
  });

  it("keeps many toasts within the window, each Close scrolled to in turn", async () => {
    await page.viewport(390, 500);
    // As the Undo bar sets it (features/mail/undo.tsx).
    document.documentElement.style.setProperty("--undo-space", "100px");
    try {
      await render(<Toaster />);
      for (const n of [1, 2, 3, 4, 5, 6, 7, 8]) {
        showError(`Failure ${n}`, "Check your connection, then try again in a moment.");
      }
      await expect.element(page.getByText("Failure 8")).toBeVisible();

      const region = page.getByRole("alertdialog").first().element().closest("[role=region]");
      if (!(region instanceof HTMLElement)) throw new Error("No toast region.");
      const box = region.getBoundingClientRect();
      expect(box.top).toBeGreaterThanOrEqual(0);
      expect(window.innerHeight - box.bottom).toBe(116);
      for (const close of page.getByRole("button", { name: "Close" }).elements()) {
        close.scrollIntoView({ block: "nearest" });
        const at = close.getBoundingClientRect();
        const top = document.elementFromPoint(at.x + at.width / 2, at.y + at.height / 2);
        expect(close.contains(top)).toBe(true);
      }
    } finally {
      document.documentElement.style.removeProperty("--undo-space");
    }
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

  it("shows the same words about other things once, closing only when all of it changed", async () => {
    await render(<Toaster />);
    const failed = "Couldn't archive 2 conversations";
    const why = "Gmail didn't answer for 2 conversations. Try again.";
    showError(failed, why, { about: ["a", "b"] });
    showError(failed, why, { about: ["c", "d"] });
    await expect.element(page.getByText(failed)).toBeVisible();
    expect(toasts()).toBe(1);

    // A newer action changed a and b, not c and d: the failure still stands.
    closeAbout(["a", "b"]);
    await new Promise((done) => requestAnimationFrame(done));
    expect(toasts()).toBe(1);
    closeAbout(["c", "d"]);

    await expect.poll(toasts).toBe(0);
  });

  it("closes for a key that covers what it is about, the keys under it", async () => {
    await render(<Toaster />);
    showError("Couldn't undo", undefined, { about: ["c1 place inbox"] });
    await expect.element(page.getByText("Couldn't undo")).toBeVisible();

    // A key that only starts the same way covers nothing.
    closeAbout(["c1 place in"]);
    await new Promise((done) => requestAnimationFrame(done));
    expect(toasts()).toBe(1);
    closeAbout(["c1 place"]);

    await expect.poll(toasts).toBe(0);
  });

  it("keeps Show details out of the alert, its lists named by their reasons", async () => {
    await render(<Toaster />);
    showError("Couldn't archive 2 conversations", "1 conversation is no longer in Gmail.", {
      details: [{ heading: "No longer in Gmail", items: ["Plans · ana@example.com"] }],
    });
    const alert = page.getByRole("alert");
    await expect.element(alert).toBeVisible();
    expect(alert.element().textContent).toContain("Couldn't archive 2 conversations");
    expect(alert.element().textContent).not.toContain("Show details");

    await userEvent.click(page.getByRole("button", { name: "Show details" }));

    await expect.element(page.getByRole("list", { name: "No longer in Gmail" })).toBeVisible();
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
