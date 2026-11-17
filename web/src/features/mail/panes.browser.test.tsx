import { describe, expect, it } from "vite-plus/test";
import { page } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { NotFound } from "@/components/route-states";
import { stubRouter } from "@/test/stub-router";
import { MailPanes } from "./panes";

// listWidth sets the window width wide and returns the list's width.
async function listWidth(width: number): Promise<number> {
  await page.viewport(width, 900);
  const list = page.getByText("The list").element().parentElement;
  await expect.poll(() => window.innerWidth).toBe(width);
  return list?.getBoundingClientRect().width ?? 0;
}

// showNotFound renders the panes: a list holding the page's main, and Page
// not found in the reading pane, which needs a router around it.
async function showNotFound() {
  await stubRouter(
    <MailPanes
      pane="reader"
      list={
        <main aria-label="Inbox">
          <h1>Inbox</h1>
        </main>
      }
      reader={<NotFound />}
    />,
  );
}

// shown counts the elements matching selector that are on screen.
function shown(selector: string): number {
  return [...document.querySelectorAll(selector)].filter((element) => element.checkVisibility())
    .length;
}

describe("MailPanes", () => {
  it("keeps one main and one h1 with Page not found in the reading pane", async () => {
    await page.viewport(1440, 900);
    await showNotFound();
    await expect
      .element(page.getByRole("heading", { name: "Page not found", level: 2 }))
      .toHaveFocus();
    expect(shown("main")).toBe(1);
    expect(shown("h1")).toBe(1);

    // On a phone the reading pane is the page.
    await page.viewport(390, 844);
    await expect
      .element(page.getByRole("heading", { name: "Page not found", level: 1 }))
      .toBeVisible();
    expect(shown("main")).toBe(1);
    expect(shown("h1")).toBe(1);
  });

  it("keeps the list, and focus in it, when the window changes between phone and wider", async () => {
    await page.viewport(390, 844);
    await render(
      <MailPanes pane="list" list={<button>In the list</button>} reader={<p>The reader</p>} />,
    );
    const inList = page.getByRole("button", { name: "In the list" });
    inList.element().focus();
    await page.viewport(900, 800);
    await expect.element(page.getByText("The reader")).toBeVisible();
    await expect.element(inList).toHaveFocus();
    await page.viewport(390, 844);
    await expect.element(page.getByText("The reader")).not.toBeInTheDocument();
    await expect.element(inList).toHaveFocus();
  });

  it("grows the list with the window, from 300 px to 440 px", async () => {
    await page.viewport(900, 900);
    await render(<MailPanes pane="list" list={<p>The list</p>} reader={<p>The reader</p>} />);
    await expect.element(page.getByText("The reader")).toBeVisible();
    expect(await listWidth(900)).toBe(300);
    expect(await listWidth(1440)).toBeCloseTo(374.4, 0);
    expect(await listWidth(1920)).toBe(440);
  });

  it("shows one pane on a phone: the list, or the open conversation", async () => {
    await page.viewport(390, 844);
    const screen = await render(
      <MailPanes pane="list" list={<p>The list</p>} reader={<p>The reader</p>} />,
    );
    await expect.element(page.getByText("The list")).toBeVisible();
    await expect.element(page.getByText("The reader")).not.toBeInTheDocument();

    await screen.rerender(
      <MailPanes pane="reader" list={<p>The list</p>} reader={<p>The reader</p>} />,
    );
    await expect.element(page.getByText("The reader")).toBeVisible();
    await expect.element(page.getByText("The list")).not.toBeVisible();
  });
});
