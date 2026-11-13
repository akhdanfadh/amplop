import { useRef, useState } from "react";
import { describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { Button } from "@/components/ui/button";
import { Sheet, SheetTitle } from "@/components/ui/sheet";
import { useFocusOnOpen } from "./use-focus-on-open";

// Place is a page whose heading names place, which a pick in a drawer
// changes, as the mail sidebar's drawer opens a place.
function Place({ focus }: { focus: "on open" | "never" }) {
  const [place, setPlace] = useState("Inbox");
  const [drawerOpen, setDrawerOpen] = useState(false);
  const headingRef = useRef<HTMLHeadingElement>(null);
  useFocusOnOpen(headingRef, focus === "on open" ? place : null);
  return (
    <>
      <Button onPress={() => setDrawerOpen(true)}>Open sidebar</Button>
      <Sheet isOpen={drawerOpen} onOpenChange={setDrawerOpen} side="left">
        <SheetTitle>Mail</SheetTitle>
        <Button
          onPress={() => {
            // The page changes before the drawer closes, as the router's
            // navigation draws first in the app.
            setPlace("Starred");
            requestAnimationFrame(() => setDrawerOpen(false));
          }}
        >
          Starred
        </Button>
      </Sheet>
      <h1 ref={headingRef} tabIndex={-1}>
        {place}
      </h1>
    </>
  );
}

// Page is a heading that takes focus each time opened changes, beside a
// button, under a drawer that is open while drawerOpen is.
function Page({ opened, drawerOpen }: { opened: string | null; drawerOpen: boolean }) {
  const headingRef = useRef<HTMLHeadingElement>(null);
  useFocusOnOpen(headingRef, opened);
  return (
    <>
      <Button>Elsewhere</Button>
      <Sheet isOpen={drawerOpen} side="left">
        <SheetTitle>Mail</SheetTitle>
      </Sheet>
      <h1 ref={headingRef} tabIndex={-1}>
        Inbox
      </h1>
    </>
  );
}

// frame lets React render what a native event changed.
function frame(): Promise<void> {
  return new Promise((resolve) =>
    requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
  );
}

describe("useFocusOnOpen", () => {
  it("focuses the page a drawer's pick opens, not the drawer's button", async () => {
    await render(<Place focus="on open" />);
    await expect.element(page.getByRole("heading", { name: "Inbox" })).toHaveFocus();

    await userEvent.click(page.getByRole("button", { name: "Open sidebar" }));
    const drawer = page.getByRole("dialog", { name: "Mail" });
    await expect.element(drawer).toBeVisible();
    await frame();
    await userEvent.keyboard("{Tab}");
    await userEvent.keyboard("{Enter}");

    await expect.element(drawer).not.toBeInTheDocument();
    await frame();
    await expect.element(page.getByRole("heading", { name: "Starred" })).toHaveFocus();
  });

  it("leaves focus alone once nothing is opened", async () => {
    const screen = await render(<Page opened="Inbox" drawerOpen={false} />);
    await expect.element(page.getByRole("heading", { name: "Inbox" })).toHaveFocus();
    const elsewhere = page.getByRole("button", { name: "Elsewhere" });
    elsewhere.element().focus();

    await screen.rerender(<Page opened={null} drawerOpen={false} />);
    await frame();
    await expect.element(elsewhere).toHaveFocus();
  });

  it("stops waiting for an inert page once something else is opened", async () => {
    const screen = await render(<Page opened="Inbox" drawerOpen />);
    const drawer = page.getByRole("dialog", { name: "Mail" });
    await expect.element(drawer).toBeVisible();
    // The page is inert, so the heading waits; then the reading pane, say,
    // takes over before the drawer closes.
    await screen.rerender(<Page opened={null} drawerOpen />);
    await frame();
    await screen.rerender(<Page opened={null} drawerOpen={false} />);
    await expect.element(drawer).not.toBeInTheDocument();
    await frame();
    await frame();
    await expect.element(page.getByRole("heading", { name: "Inbox" })).not.toHaveFocus();
  });
});
