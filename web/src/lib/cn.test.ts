import { describe, expect, it } from "vite-plus/test";
import { cn } from "./cn";

describe("cn", () => {
  it("knows the theme's text sizes apart from text colours", () => {
    expect(cn("text-snippet text-muted-foreground")).toBe("text-snippet text-muted-foreground");
    expect(cn("text-message", "text-foreground")).toBe("text-message text-foreground");
  });

  it("lets a theme size override a stock one, as a className does", () => {
    expect(cn("text-sm text-muted-foreground", "text-snippet")).toBe(
      "text-muted-foreground text-snippet",
    );
    expect(cn("text-subject", "text-lg")).toBe("text-lg");
  });

  it("knows the theme's easing", () => {
    expect(cn("ease-standard", "ease-out")).toBe("ease-out");
  });

  it("merges stock classes as before", () => {
    expect(cn("px-2 py-1", "p-3")).toBe("p-3");
    expect(cn("bg-card", false, "bg-popover")).toBe("bg-popover");
  });
});
