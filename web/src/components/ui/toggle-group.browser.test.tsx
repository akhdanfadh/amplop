import { useState } from "react";
import { afterEach, describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { Button } from "./button";
import { Popover, PopoverTrigger } from "./popover";
import { ToggleGroup, ToggleGroupItem } from "./toggle-group";
import { Tooltip, TooltipTrigger } from "./tooltip";
import { applyTheme, type Theme } from "@/lib/theme";

// ThemeChoice is a popover holding a three-way theme choice, as the member
// menu is; its name differs from its button's, which React Aria would
// otherwise borrow.
function ThemeChoice() {
  const [theme, setTheme] = useState("system");
  return (
    <PopoverTrigger>
      <Button>Member menu</Button>
      <Popover aria-label="Account and theme">
        <ToggleGroup
          aria-label="Theme"
          selectionMode="single"
          disallowEmptySelection
          selectedKeys={[theme]}
          onSelectionChange={(keys) => setTheme(String([...keys][0]))}
        >
          <ToggleGroupItem id="light">Light</ToggleGroupItem>
          <ToggleGroupItem id="dark">Dark</ToggleGroupItem>
          <ToggleGroupItem id="system">System</ToggleGroupItem>
        </ToggleGroup>
        <Button>Sign out</Button>
      </Popover>
    </PopoverTrigger>
  );
}

// Themes is a two-way theme choice with a tooltip on each icon, whose
// pick switches the page's theme as the member menu's does.
function Themes() {
  const [theme, setTheme] = useState<Theme>("light");
  return (
    <ToggleGroup
      aria-label="Theme"
      selectionMode="single"
      disallowEmptySelection
      selectedKeys={[theme]}
      onSelectionChange={(keys) => {
        const picked = keys.has("dark") ? "dark" : "light";
        setTheme(picked);
        applyTheme(picked);
      }}
    >
      {(["light", "dark"] as const).map((choice) => (
        <TooltipTrigger key={choice}>
          <ToggleGroupItem id={choice} aria-label={choice}>
            {choice.charAt(0)}
          </ToggleGroupItem>
          <Tooltip>{choice}</Tooltip>
        </TooltipTrigger>
      ))}
    </ToggleGroup>
  );
}

describe("ToggleGroup in a Popover", () => {
  // A test that picks dark, or a failed one, leaves the page light for the next.
  afterEach(() => {
    applyTheme("light");
  });

  it("opens as a named dialog whose choice keeps one item picked", async () => {
    await render(<ThemeChoice />);
    await userEvent.click(page.getByRole("button", { name: "Member menu" }));
    const dialog = page.getByRole("dialog", { name: "Account and theme" });
    await expect.element(dialog).toBeVisible();

    const system = dialog.getByRole("radio", { name: "System" });
    await expect.element(system).toBeChecked();
    // Seen, not only announced: the chosen item has its own surface.
    expect(getComputedStyle(system.element()).backgroundColor).not.toBe(
      getComputedStyle(dialog.getByRole("radio", { name: "Light" }).element()).backgroundColor,
    );
    await userEvent.click(dialog.getByRole("radio", { name: "Dark" }));
    await expect.element(dialog.getByRole("radio", { name: "Dark" })).toBeChecked();
    await expect.element(system).not.toBeChecked();
    // Picking the chosen one again keeps it: there is always a theme.
    await userEvent.click(dialog.getByRole("radio", { name: "Dark" }));
    await expect.element(dialog.getByRole("radio", { name: "Dark" })).toBeChecked();
    // The choice applies at once; the popover stays for another change.
    await expect.element(dialog).toBeVisible();
  });

  it("moves between choices with arrow keys and rings the focused one", async () => {
    await render(<ThemeChoice />);
    await userEvent.click(page.getByRole("button", { name: "Member menu" }));
    const dialog = page.getByRole("dialog", { name: "Account and theme" });
    dialog.getByRole("radio", { name: "System" }).element().focus();
    await userEvent.keyboard("{ArrowLeft}");

    const dark = dialog.getByRole("radio", { name: "Dark" });
    await expect.element(dark).toHaveFocus();
    expect(getComputedStyle(dark.element()).outlineStyle).toBe("solid");
    await userEvent.keyboard("{Escape}");
    await expect.element(dialog).not.toBeInTheDocument();
  });

  it("removes a pressed item's tooltip even as the pick switches the theme", async () => {
    // A theme switch pauses fades (src/styles.css); a cancelled exit
    // animation would leave the closing tooltip on screen for good.
    await render(
      <PopoverTrigger>
        <Button>Member menu</Button>
        <Popover aria-label="Member menu">
          <Themes />
        </Popover>
      </PopoverTrigger>,
    );
    await userEvent.click(page.getByRole("button", { name: "Member menu" }));
    await userEvent.hover(page.getByRole("radio", { name: "dark" }));
    await expect.element(page.getByRole("tooltip")).toBeVisible();

    await userEvent.click(page.getByRole("radio", { name: "dark" }));
    await expect.element(page.getByRole("tooltip")).not.toBeInTheDocument();
  });

  it("keeps the popover's hairline in the dark theme, which has no shadow", async () => {
    document.documentElement.classList.add("dark");
    await render(<ThemeChoice />);
    await userEvent.click(page.getByRole("button", { name: "Member menu" }));
    await expect.element(page.getByRole("dialog", { name: "Account and theme" })).toBeVisible();
    const popover = document.querySelector("[data-trigger=DialogTrigger]");
    if (!popover) throw new Error("no popover");
    expect(getComputedStyle(popover).boxShadow).not.toBe("none");
  });
});
