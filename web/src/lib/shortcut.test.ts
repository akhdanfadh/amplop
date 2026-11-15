import { describe, expect, it } from "vite-plus/test";
import { shortcutModifier } from "./shortcut";

describe("shortcutModifier", () => {
  it.each([
    ["a Mac", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36", "Cmd"],
    [
      "an iPhone",
      "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15",
      "Cmd",
    ],
    ["an iPad", "Mozilla/5.0 (iPad; CPU OS 18_0 like Mac OS X) AppleWebKit/605.1.15", "Cmd"],
    ["Linux", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36", "Ctrl"],
    ["Windows", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36", "Ctrl"],
    ["ChromeOS", "Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36", "Ctrl"],
  ])("names %s's modifier", (_platform, userAgent, modifier) => {
    expect(shortcutModifier(userAgent)).toBe(modifier);
  });
});
