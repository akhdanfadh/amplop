import { describe, expect, it } from "vite-plus/test";
import { page, userEvent } from "vite-plus/test/browser/context";
import { render } from "vitest-browser-react";
import { Button } from "./button";
import { SheetContent, SheetTitle, SheetTrigger } from "./sheet";

describe("SheetContent", () => {
  it("opens as the sheet it names, titled and closable", async () => {
    await render(
      <SheetTrigger>
        <Button>Filters</Button>
        <SheetContent side="left">
          <SheetTitle>Filters</SheetTitle>
        </SheetContent>
      </SheetTrigger>,
    );
    await userEvent.click(page.getByRole("button", { name: "Filters" }));

    const sheet = page.getByRole("dialog", { name: "Filters" });
    await expect.element(sheet).toBeVisible();
    await userEvent.click(sheet.getByRole("button", { name: "Close" }));
    await expect.element(sheet).not.toBeInTheDocument();
  });
});
