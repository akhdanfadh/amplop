import { useState } from "react";
import { describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { InboxIcon, PlusIcon } from "lucide-react";
import { Button } from "./button";
import { Popover, PopoverTrigger } from "./popover";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupAction,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarInput,
  SidebarInset,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
  SidebarProvider,
  SidebarTrigger,
} from "./sidebar";

// Frame is a sidebar named Mail beside some content: a trigger and a text
// field. The sidebar's header has a wordmark the rail hides. Its owner
// holds the full layout's open state, as the app's frame does, starting
// from open.
function Frame({ open: initial = true }: { open?: boolean }) {
  const [open, setOpen] = useState(initial);
  return (
    <SidebarProvider open={open} onOpenChange={setOpen}>
      <Sidebar label="Mail">
        <SidebarHeader className="flex-row items-center justify-between">
          <a href="#top" className="group-data-[collapsible=icon]:hidden">
            inbok
          </a>
          <SidebarTrigger />
        </SidebarHeader>
        <SidebarContent>
          <SidebarMenu>
            <SidebarMenuItem>
              <SidebarMenuButton tooltip="Inbox" href="#inbox" isActive>
                <InboxIcon aria-hidden="true" />
                <span>Inbox</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
          <SidebarGroup>
            <SidebarGroupLabel>This inbok</SidebarGroupLabel>
            <SidebarMenu>
              <SidebarMenuItem>
                <SidebarMenuButton tooltip="Members">
                  <InboxIcon aria-hidden="true" />
                  <span>Members</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
            </SidebarMenu>
          </SidebarGroup>
        </SidebarContent>
        <SidebarFooter>
          <PopoverTrigger>
            <Button variant="ghost">Member menu</Button>
            <Popover aria-label="Member menu">
              <Button variant="ghost">Settings</Button>
            </Popover>
          </PopoverTrigger>
        </SidebarFooter>
      </Sidebar>
      <SidebarInset>
        <SidebarTrigger />
        <p>The list</p>
        <input aria-label="Search mail" />
      </SidebarInset>
    </SidebarProvider>
  );
}

// The sidebar's state: "expanded" or "collapsed".
function state(): string | null {
  return document.querySelector("[data-slot=sidebar]")?.getAttribute("data-state") ?? null;
}

// The width the sidebar's navigation takes on screen.
function navWidth(): number {
  return page.getByRole("navigation", { name: "Mail" }).element().getBoundingClientRect().width;
}

// frame lets React render what a native event changed.
function frame(): Promise<void> {
  return new Promise((resolve) =>
    requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
  );
}

// press sends a keydown as the browser would, without Playwright's
// keyboard, which always sends B as "b" on the KeyB key.
function press(init: KeyboardEventInit) {
  document.body.dispatchEvent(
    new KeyboardEvent("keydown", { bubbles: true, cancelable: true, ...init }),
  );
}

// sheet is the drawer the sidebar opens in.
function sheet() {
  return page.getByRole("dialog", { name: "Mail" });
}

// opened waits until drawer is on screen and React Aria has finished
// showing it, which can take two frames after it mounts.
// NOTE: closed sooner, the drawer can stay on screen for good, unseen (React
// Aria 3.53, adobe/react-spectrum#10772); no person closes it that fast.
async function opened(drawer = sheet()): Promise<void> {
  await expect.element(drawer).toBeVisible();
  await frame();
}

// expandButton is the sidebar's own trigger while it is collapsed.
function expandButton() {
  return page
    .getByRole("navigation", { name: "Mail" })
    .getByRole("button", { name: "Expand sidebar" });
}

// LabelsFrame is an open sidebar whose header filters its labels and whose
// Labels group has an action to add one.
function LabelsFrame() {
  const [open, setOpen] = useState(true);
  return (
    <SidebarProvider open={open} onOpenChange={setOpen}>
      <Sidebar label="Mail">
        <SidebarHeader>
          <SidebarInput aria-label="Filter labels" />
        </SidebarHeader>
        <SidebarContent>
          <SidebarGroup>
            <SidebarGroupLabel>Labels</SidebarGroupLabel>
            <SidebarGroupAction aria-label="Add label">
              <PlusIcon aria-hidden="true" />
            </SidebarGroupAction>
          </SidebarGroup>
        </SidebarContent>
      </Sidebar>
    </SidebarProvider>
  );
}

describe("Sidebar", () => {
  it("collapses to the rail and back with Ctrl+B in a wide window", async () => {
    await page.viewport(1440, 900);
    await render(<Frame />);
    await expect.element(page.getByRole("navigation", { name: "Mail" })).toBeVisible();
    expect(navWidth()).toBe(240);

    await userEvent.keyboard("{Control>}b{/Control}");
    expect(state()).toBe("collapsed");
    expect(navWidth()).toBe(56);
    await expect.element(expandButton()).toBeInTheDocument();

    // Caps Lock gives "B"; the key, not the letter, counts.
    await userEvent.keyboard("{Control>}{Shift>}{/Shift}B{/Control}");
    expect(state()).toBe("expanded");
  });

  it("takes Cmd+B too, but not Ctrl+Alt+B or a held key", async () => {
    await page.viewport(1440, 900);
    await render(<Frame />);
    await userEvent.keyboard("{Meta>}b{/Meta}");
    expect(state()).toBe("collapsed");

    await userEvent.keyboard("{Control>}{Alt>}b{/Alt}{/Control}");
    expect(state()).toBe("collapsed");
    press({ key: "b", code: "KeyB", ctrlKey: true, repeat: true });
    await frame();
    expect(state()).toBe("collapsed");
    press({ key: "b", code: "KeyB", ctrlKey: true, metaKey: true });
    await frame();
    expect(state()).toBe("collapsed");
    await userEvent.click(page.getByText("The list"));
    await userEvent.keyboard("b");
    await frame();
    expect(state()).toBe("collapsed");
  });

  it("follows the key in a non-Latin layout, the letter in a Latin one", async () => {
    await page.viewport(1440, 900);
    await render(<Frame />);
    // Cyrillic: the B key types "и".
    press({ key: "и", code: "KeyB", ctrlKey: true });
    await frame();
    expect(state()).toBe("collapsed");
    press({ key: "и", code: "KeyB", ctrlKey: true });
    await frame();
    expect(state()).toBe("expanded");

    // Dvorak: the B key types "x", where Ctrl+X is cut, and "b" is on the
    // N key.
    press({ key: "x", code: "KeyB", ctrlKey: true });
    await frame();
    expect(state()).toBe("expanded");
    press({ key: "b", code: "KeyN", ctrlKey: true });
    await frame();
    expect(state()).toBe("collapsed");
  });

  it("keeps focus in the sidebar when the rail hides the focused part", async () => {
    await page.viewport(1440, 900);
    await render(<Frame />);
    const wordmark = page.getByRole("link", { name: "inbok" });
    wordmark.element().focus();
    await userEvent.keyboard("{Control>}b{/Control}");
    expect(state()).toBe("collapsed");
    await expect.element(expandButton()).toHaveFocus();
  });

  it("never lets CSS hide the sidebar React chose", async () => {
    // A breakpoint in rem follows the browser's default font size, which
    // the layout's px queries do not; a member with a larger font would
    // be left without navigation.
    await page.viewport(1100, 800);
    await render(<Frame />);
    for (const element of document.querySelectorAll("[data-slot^=sidebar]")) {
      expect(element.className).not.toMatch(/\b(sm|md|lg|xl|2xl):/);
    }
  });

  it("leaves Ctrl+B to a text field and to Ctrl+Shift+B", async () => {
    await page.viewport(1440, 900);
    await render(<Frame />);
    await userEvent.click(page.getByRole("textbox", { name: "Search mail" }));
    await userEvent.keyboard("{Control>}b{/Control}");
    expect(state()).toBe("expanded");

    await userEvent.click(page.getByText("The list"));
    await userEvent.keyboard("{Control>}{Shift>}b{/Shift}{/Control}");
    expect(state()).toBe("expanded");
  });

  it("expands the rail into the drawer, over the content, in the rail layout", async () => {
    await page.viewport(1100, 800);
    await render(<Frame />);
    // The member's open choice is for the full layout; this one shows the rail.
    expect(state()).toBe("collapsed");
    expect(navWidth()).toBe(56);

    await userEvent.click(expandButton());
    await expect.element(sheet()).toBeVisible();
    // Open in the drawer, the items show their names, not a rail's icons.
    await expect.element(sheet().getByText("Members")).toBeVisible();
    // The content keeps its place: only the rail's room is reserved.
    const gap = document.querySelector("[data-slot=sidebar-gap]");
    expect(gap?.getBoundingClientRect().width).toBe(56);

    // It collapses back with its own button, Escape, or a press beside it.
    await userEvent.click(sheet().getByRole("button", { name: "Collapse sidebar" }));
    await expect.element(sheet()).not.toBeInTheDocument();
    await userEvent.click(expandButton());
    await opened();
    await userEvent.keyboard("{Escape}");
    await expect.element(sheet()).not.toBeInTheDocument();
    await userEvent.click(expandButton());
    await opened();
    const scrim = document.querySelector("[data-slot=sheet-overlay]");
    if (!scrim) throw new Error("no scrim");
    await userEvent.click(page.elementLocator(scrim), { position: { x: 700, y: 400 } });
    await expect.element(sheet()).not.toBeInTheDocument();
  });

  it("opens and closes the rail's drawer with Ctrl+B, focus in it", async () => {
    await page.viewport(1100, 800);
    await render(<Frame />);
    await userEvent.click(page.getByText("The list"));
    await userEvent.keyboard("{Control>}b{/Control}");
    await opened();
    await expect.poll(() => sheet().element().contains(document.activeElement)).toBe(true);
    await userEvent.keyboard("{Control>}b{/Control}");
    await expect.element(sheet()).not.toBeInTheDocument();

    await userEvent.keyboard("{Control>}b{/Control}");
    await opened();
    await userEvent.keyboard("{Escape}");
    await expect.element(sheet()).not.toBeInTheDocument();
  });

  it("leaves the drawer to Escape in its own popover", async () => {
    await page.viewport(1100, 800);
    await render(<Frame />);
    await userEvent.click(expandButton());
    await userEvent.click(sheet().getByRole("button", { name: "Member menu" }));
    const popover = page.getByRole("dialog", { name: "Member menu" });
    await expect.element(popover).toBeVisible();
    await userEvent.keyboard("{Escape}");
    await expect.element(popover).not.toBeInTheDocument();
    await expect.element(sheet()).toBeVisible();
  });

  it("leaves Ctrl+B alone in an open popover", async () => {
    await page.viewport(1440, 900);
    await render(<Frame />);
    await userEvent.click(page.getByRole("button", { name: "Member menu" }));
    await expect.element(page.getByRole("dialog", { name: "Member menu" })).toBeVisible();
    await userEvent.keyboard("{Control>}b{/Control}");
    await frame();
    expect(state()).toBe("expanded");
  });

  it("closes the rail's drawer when the window leaves the rail layout and returns", async () => {
    await page.viewport(1100, 800);
    await render(<Frame />);
    await userEvent.click(expandButton());
    await opened();
    await expect.poll(() => sheet().element().contains(document.activeElement)).toBe(true);
    await page.viewport(1440, 900);
    await expect.element(sheet()).not.toBeInTheDocument();
    // Focus was in the drawer: React Aria gives it back to the button that
    // opened it, which the wide layout keeps.
    await expect
      .element(
        page
          .getByRole("navigation", { name: "Mail" })
          .getByRole("button", { name: "Collapse sidebar" }),
      )
      .toHaveFocus();
    await page.viewport(1100, 800);
    await frame();
    await expect.element(sheet()).not.toBeInTheDocument();
    expect(state()).toBe("collapsed");
  });

  it("is a drawer below 1024 px that stays closed after the window changes", async () => {
    await page.viewport(900, 800);
    await render(<Frame />);
    await expect.element(page.getByRole("navigation", { name: "Mail" })).not.toBeInTheDocument();

    await userEvent.click(page.getByRole("button", { name: "Open sidebar" }));
    const drawer = page.getByRole("dialog", { name: "Mail" });
    await expect.element(drawer).toBeVisible();
    await expect.element(drawer.getByRole("navigation", { name: "Mail" })).toBeVisible();
    const panel = document.querySelector("[data-slot=sheet-content]");
    expect(panel?.getBoundingClientRect().width).toBe(288);

    // The phone shows the same drawer, so it stays open.
    await page.viewport(390, 844);
    await expect.element(drawer).toBeVisible();
    // The drawer opens with its own button under the pointer, whose
    // tooltip a first Escape would close; wait until it has gone.
    await userEvent.hover(drawer.getByRole("link", { name: "Inbox" }));
    await expect.element(page.getByRole("tooltip")).not.toBeInTheDocument();
    await userEvent.keyboard("{Escape}");
    await expect.element(drawer).not.toBeInTheDocument();

    // A press on the scrim beside it closes it too.
    await userEvent.click(page.getByRole("button", { name: "Open sidebar" }));
    await opened(drawer);
    const scrim = document.querySelector("[data-slot=sheet-overlay]");
    if (!scrim) throw new Error("no scrim");
    await userEvent.click(page.elementLocator(scrim), { position: { x: 360, y: 400 } });
    await expect.element(drawer).not.toBeInTheDocument();

    await page.viewport(900, 800);
    await userEvent.click(page.getByRole("button", { name: "Open sidebar" }));
    await opened(drawer);
    await page.viewport(1440, 900);
    await expect.element(drawer).not.toBeInTheDocument();
    await page.viewport(900, 800);
    await expect.element(page.getByRole("button", { name: "Open sidebar" })).toBeVisible();
    await expect.element(drawer).not.toBeInTheDocument();
  });

  it("marks the open item with a bar and aria-current, not bold, which means unread", async () => {
    await page.viewport(1440, 900);
    await render(<Frame />);
    const link = page.getByRole("link", { name: "Inbox" });
    await expect.element(link).toHaveAttribute("aria-current", "page");
    const inbox = link.element();
    expect(getComputedStyle(inbox, "::before").width).toBe("2px");
    expect(getComputedStyle(inbox).fontWeight).toBe("500");
    // Its tint stays while hovered.
    const tint = getComputedStyle(inbox).backgroundColor;
    await userEvent.hover(link);
    // After the colour transition (150 ms) would have run.
    await new Promise((resolve) => setTimeout(resolve, 300));
    expect(getComputedStyle(inbox).backgroundColor).toBe(tint);
  });

  it("keeps a nested open item's tint while hovered", async () => {
    await page.viewport(1440, 900);
    await render(
      <SidebarProvider open onOpenChange={() => {}}>
        <Sidebar label="Mail">
          <SidebarMenu>
            <SidebarMenuItem>
              <SidebarMenuSub>
                <SidebarMenuSubItem>
                  <SidebarMenuSubButton href="#work" isActive>
                    <span>Work</span>
                  </SidebarMenuSubButton>
                </SidebarMenuSubItem>
              </SidebarMenuSub>
            </SidebarMenuItem>
          </SidebarMenu>
        </Sidebar>
      </SidebarProvider>,
    );
    const link = page.getByRole("link", { name: "Work" });
    const tint = getComputedStyle(link.element()).backgroundColor;
    await userEvent.hover(link);
    await new Promise((resolve) => setTimeout(resolve, 300));
    expect(getComputedStyle(link.element()).backgroundColor).toBe(tint);
  });

  it("cuts a long name short before its count, which keeps its width", async () => {
    await page.viewport(1440, 900);
    await render(
      <SidebarProvider open onOpenChange={() => {}}>
        <Sidebar label="Mail">
          <SidebarMenu>
            <SidebarMenuItem>
              <SidebarMenuButton href="#studio">
                <InboxIcon aria-hidden="true" />
                <span>studio.team.accounts@inbok-example.org</span>
                <SidebarMenuBadge>
                  1,204<span className="sr-only"> unread</span>
                </SidebarMenuBadge>
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
        </Sidebar>
      </SidebarProvider>,
    );
    const link = page.getByRole("link", { name: /studio/ }).element();
    const name = link.querySelector("span:not([data-slot])");
    const count = link.querySelector("[data-slot=sidebar-menu-badge]");
    if (!name || !count) throw new Error("no name or count");
    expect(name.getBoundingClientRect().right).toBeLessThanOrEqual(
      count.getBoundingClientRect().left,
    );
    expect(name.scrollWidth).toBeGreaterThan(name.clientWidth);
    // A screen reader hears the count with the name.
    await expect
      .element(
        page.getByRole("link", { name: "studio.team.accounts@inbok-example.org 1,204 unread" }),
      )
      .toBeInTheDocument();
  });

  it("names each icon in a tooltip on the rail", async () => {
    await page.viewport(1440, 900);
    await render(<Frame />);
    await userEvent.keyboard("{Control>}b{/Control}");
    await userEvent.hover(page.getByRole("button", { name: "Members" }));
    await expect.element(page.getByRole("tooltip")).toHaveTextContent("Members");
  });

  it("ends a group's label with its action, which the rail hides", async () => {
    await page.viewport(1440, 900);
    await render(<LabelsFrame />);
    const label = page.getByText("Labels").element().getBoundingClientRect();
    const action = page.getByRole("button", { name: "Add label" }).element();
    const box = action.getBoundingClientRect();
    expect(box.top + box.height / 2).toBe(label.top + label.height / 2);
    expect(box.right).toBe(label.right);
    // As tall as the rows beside it.
    const field = page.getByRole("textbox", { name: "Filter labels" }).element();
    expect(field.getBoundingClientRect().height).toBe(32);

    await userEvent.keyboard("{Control>}b{/Control}");
    expect(action.checkVisibility()).toBe(false);
  });

  it("keeps a group's items in place on the rail, its label a hairline", async () => {
    await page.viewport(1440, 900);
    await render(<Frame />);
    const members = page.getByRole("button", { name: "Members" }).element();
    const before = members.getBoundingClientRect().top;

    await userEvent.keyboard("{Control>}b{/Control}");
    expect(members.getBoundingClientRect().top).toBe(before);
    // Still named for screen readers, its words hidden behind a line.
    const label = page.getByText("This inbok").element();
    expect(getComputedStyle(label).color).toBe("rgba(0, 0, 0, 0)");
    expect(getComputedStyle(label, "::before").height).toBe("1px");
  });

  it("names the shortcut in the trigger's tooltip", async () => {
    await page.viewport(1440, 900);
    await render(<Frame />);
    // The keyboard reaches the trigger after the wordmark; focus opens its
    // tooltip. Apple keyboards say Cmd.
    const key = /Mac|iPhone|iPad/.test(navigator.userAgent) ? "Cmd" : "Ctrl";
    await userEvent.keyboard("{Tab}{Tab}");
    await expect
      .poll(() => document.querySelector("[role=tooltip]")?.textContent)
      .toBe(`Collapse sidebar (${key}+B)`);
  });

  it("stops scrolling at the drawer's end, so the page behind stays still", async () => {
    // React Aria's iOS rule for this is a <style>, which the
    // Content-Security-Policy blocks; the sidebar's own class stands in.
    await page.viewport(390, 844);
    await render(<Frame />);
    await userEvent.click(page.getByRole("button", { name: "Open sidebar" }));
    const content = document.querySelector("[data-slot=sidebar-content]");
    if (!content) throw new Error("no sidebar content");
    expect(getComputedStyle(content).overscrollBehaviorY).toBe("contain");
  });
});
