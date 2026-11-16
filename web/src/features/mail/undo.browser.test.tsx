import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { cdp, page, userEvent } from "vite-plus/test/browser/context";
import { useRef, type ReactElement } from "react";
import { render } from "vitest-browser-react";
import { shortcutModifier } from "@/lib/shortcut";
import { applyTheme } from "@/lib/theme";
import { pointAway, pointTo } from "@/test/pointer";
import { closeUndo, sayUndone, showUndo, UndoBar } from "./undo";

afterEach(() => {
  closeUndo();
  document.documentElement.style.fontSize = "";
});

// Bar is the Undo bar under a heading, its focus to fall back on.
function Bar(): ReactElement {
  const heading = useRef<HTMLHeadingElement>(null);
  return (
    <>
      <h1 ref={heading} tabIndex={-1}>
        Inbox
      </h1>
      <UndoBar fallbackFocus={heading} />
    </>
  );
}

// bar returns the visible bar.
function bar(): HTMLElement {
  const element = document.querySelector<HTMLElement>("[data-slot=undo-bar]");
  if (!element) throw new Error("No Undo bar.");
  return element;
}

// countdown returns the bar's draining line.
function countdown(): HTMLElement {
  const line = bar().querySelector<HTMLElement>("[data-countdown]");
  if (!line) throw new Error("No countdown.");
  return line;
}

// space returns --undo-space as the bar sets it.
function space(): string {
  return document.documentElement.style.getPropertyValue("--undo-space");
}

// outside adds a button outside the bar, focused, as a list row would be,
// and returns it.
function outside(): HTMLButtonElement {
  const button = document.createElement("button");
  button.textContent = "Row";
  document.body.prepend(button);
  button.focus();
  return button;
}

// undo shows an Undo of an archived conversation, running onUndo.
function undo(onUndo = () => {}) {
  return showUndo({
    title: "Archived",
    subject: "Venue options",
    count: 1,
    where: "All mail",
    onUndo,
  });
}

describe("UndoBar", () => {
  it("says what happened, to which conversation, and where it went", async () => {
    await render(<Bar />);
    undo();
    await expect.element(page.getByRole("button", { name: "Undo" })).toBeVisible();

    expect(bar().textContent).toContain("Archived");
    expect(bar().textContent).toContain("Venue options");
    // For screen readers, which hear it as it appears.
    await expect
      .element(page.getByRole("status"))
      .toHaveTextContent("Archived: Venue options. Find it in All mail.");
  });

  it("keeps its live region in the page, empty between Undos", async () => {
    await render(<Bar />);
    await expect.element(page.getByRole("status")).toBeInTheDocument();
    expect(page.getByRole("status").element().textContent).toBe("");

    undo();
    await expect.poll(() => page.getByRole("status").element().textContent).toContain("Archived");
    closeUndo();
    await expect.poll(() => page.getByRole("status").element().textContent).toBe("");
  });

  it("hands focus back where it came from after Undo, else to the heading after Close", async () => {
    await render(<Bar />);
    const row = outside();
    try {
      undo();
      await userEvent.keyboard("{Tab}");
      await expect.element(page.getByRole("button", { name: "Undo" })).toHaveFocus();
      await userEvent.keyboard("{Enter}");
      expect(document.activeElement).toBe(row);

      undo();
      await userEvent.keyboard("{Tab}");
      await expect.element(page.getByRole("button", { name: "Undo" })).toHaveFocus();
      // The row it came from is gone, e.g. the list redrew.
      row.remove();
      await userEvent.keyboard("{Tab}");
      await expect.element(page.getByRole("button", { name: "Close" })).toHaveFocus();
      await userEvent.keyboard("{Enter}");
      await expect.element(page.getByRole("heading", { name: "Inbox" })).toHaveFocus();
    } finally {
      row.remove();
    }
  });

  it("keeps its height as --undo-space while it shows, for toasts to stand above", async () => {
    await render(<Bar />);
    undo();
    await expect.element(page.getByRole("button", { name: "Undo" })).toBeVisible();
    await expect.poll(() => Number.parseFloat(space())).toBeGreaterThan(0);

    closeUndo();
    await expect.poll(space).toBe("");
  });

  it("puts its buttons under the words when narrow, e.g. a phone at large text", async () => {
    await page.viewport(390, 800);
    document.documentElement.style.fontSize = "200%";
    await render(<Bar />);
    undo();
    const undoButton = page.getByRole("button", { name: "Undo" });
    await expect.element(undoButton).toBeVisible();
    const subject = page.getByText("Venue options").element().getBoundingClientRect();
    await expect
      .poll(() => undoButton.element().getBoundingClientRect().top)
      .toBeGreaterThanOrEqual(subject.bottom);
    // The subject keeps the whole width rather than a few letters.
    expect(subject.width).toBeGreaterThan(200);
  });

  it("runs Undo and closes", async () => {
    const onUndo = vi.fn<() => void>();
    await render(<Bar />);
    undo(onUndo);

    await userEvent.click(page.getByRole("button", { name: "Undo" }));

    expect(onUndo).toHaveBeenCalledOnce();
    await expect.element(page.getByRole("button", { name: "Undo" })).not.toBeInTheDocument();
  });

  it("closes with Close, or when its countdown runs out", async () => {
    await render(<Bar />);
    undo();
    await userEvent.click(page.getByRole("button", { name: "Close" }));
    await expect.element(page.getByRole("button", { name: "Undo" })).not.toBeInTheDocument();

    undo();
    await expect.element(page.getByRole("button", { name: "Undo" })).toBeVisible();
    countdown().dispatchEvent(new AnimationEvent("animationend", { bubbles: true }));
    await expect.element(page.getByRole("button", { name: "Undo" })).not.toBeInTheDocument();
  });

  it("counts down 10 s, paused while the pointer or focus is on it", async () => {
    await render(<Bar />);
    undo();
    await expect.element(page.getByRole("button", { name: "Undo" })).toBeVisible();
    expect(getComputedStyle(countdown()).animationDuration).toBe("10s");
    expect(countdown().style.animationPlayState).toBe("running");

    pointTo(bar());
    await expect.poll(() => countdown().style.animationPlayState).toBe("paused");
    pointAway(bar());
    await expect.poll(() => countdown().style.animationPlayState).toBe("running");

    page.getByRole("button", { name: "Undo" }).element().focus();
    await expect.poll(() => countdown().style.animationPlayState).toBe("paused");
  });

  it("shows one Undo, the last action's, its countdown from full", async () => {
    await render(<Bar />);
    undo();
    await expect.element(page.getByRole("button", { name: "Undo" })).toBeVisible();
    const first = countdown();
    showUndo({
      title: "Moved to Trash",
      subject: "Plans",
      count: 1,
      where: "Trash",
      onUndo: () => {},
    });

    await expect.element(page.getByText("Moved to Trash", { exact: true })).toBeVisible();
    expect(page.getByText("Archived").elements()).toHaveLength(0);
    expect(countdown()).not.toBe(first);
  });

  it("counts down from the start after a mouse closed the last one", async () => {
    await render(<Bar />);
    undo();
    await userEvent.click(page.getByRole("button", { name: "Undo" }));
    await expect.element(page.getByRole("button", { name: "Undo" })).not.toBeInTheDocument();
    // The pointer moves off where the bar was.
    await userEvent.hover(page.getByRole("heading", { name: "Inbox" }));

    undo();

    await expect.element(page.getByRole("button", { name: "Undo" })).toBeVisible();
    expect(countdown().style.animationPlayState).toBe("running");
  });

  it("keeps counting down through a theme switch", async () => {
    await render(<Bar />);
    undo();
    await expect.element(page.getByRole("button", { name: "Undo" })).toBeVisible();
    const dark = document.documentElement.classList.contains("dark");

    try {
      applyTheme(dark ? "light" : "dark");
      await new Promise((done) => setTimeout(done, 300));

      expect(page.getByRole("button", { name: "Undo" }).query()).not.toBeNull();
      expect(getComputedStyle(countdown()).animationDuration).toBe("10s");
    } finally {
      applyTheme(dark ? "dark" : "light");
    }
  });

  it("draws its countdown in the text's colour in forced colours", async () => {
    await render(<Bar />);
    undo();
    await expect.element(page.getByRole("button", { name: "Undo" })).toBeVisible();
    const session = cdp();
    await session.send("Emulation.setEmulatedMedia", {
      features: [{ name: "forced-colors", value: "active" }],
    });
    try {
      const text = getComputedStyle(page.getByText("Archived").element()).color;
      await expect.poll(() => getComputedStyle(countdown()).backgroundColor).toBe(text);
      expect(text).not.toBe(getComputedStyle(bar()).backgroundColor);
    } finally {
      await session.send("Emulation.setEmulatedMedia", { features: [] });
    }
  });

  it("says each Undo, the same words twice too, politely", async () => {
    await render(<Bar />);
    const region = page.getByRole("status");
    await expect.element(region).toHaveAttribute("aria-live", "polite");
    undo();
    await expect.element(region).toHaveTextContent("Archived: Venue options. Find it in All mail.");
    const first = region.element().firstElementChild;

    undo();

    await expect.poll(() => region.element().firstElementChild).not.toBe(first);
    expect(region.element().textContent).toBe("Archived: Venue options. Find it in All mail.");
  });

  it("says that an Undo ran, until the next one shows", async () => {
    await render(<Bar />);
    const region = page.getByRole("status");
    undo();
    await userEvent.click(page.getByRole("button", { name: "Undo" }));

    sayUndone("Venue options");

    await expect.element(region).toHaveTextContent("Undone: Venue options.");
    undo();
    await expect.element(region).toHaveTextContent("Archived: Venue options. Find it in All mail.");
  });

  it("names its conversation in Undo's description", async () => {
    await render(<Bar />);
    undo();
    const key = shortcutModifier();

    await expect
      .element(page.getByRole("button", { name: "Undo" }))
      .toHaveAccessibleDescription(`Archived Venue options ${key}+Z`);
  });
});
