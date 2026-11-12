import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { applyTheme, readTheme, storeTheme, themeKey, themeScript } from "./theme";

const html = document.documentElement;

afterEach(() => {
  localStorage.clear();
  html.classList.remove("dark");
});

// runScript runs the root route's head script as the shell does.
function runScript() {
  // oxlint-disable-next-line typescript/no-implied-eval -- the script is our own constant.
  new Function(themeScript)();
}

describe("theme", () => {
  it("head script applies a stored dark or light choice", () => {
    localStorage.setItem(themeKey, "dark");
    runScript();
    expect(html.classList.contains("dark")).toBe(true);

    localStorage.setItem(themeKey, "light");
    runScript();
    expect(html.classList.contains("dark")).toBe(false);
  });

  it("head script and applyTheme agree on the system choice", () => {
    runScript();
    const fromScript = html.classList.contains("dark");
    applyTheme("system");
    expect(html.classList.contains("dark")).toBe(fromScript);
  });

  it("stores a choice, and system clears it", () => {
    storeTheme("dark");
    expect(readTheme()).toBe("dark");
    expect(html.classList.contains("dark")).toBe(true);

    storeTheme("system");
    expect(localStorage.getItem(themeKey)).toBeNull();
    expect(readTheme()).toBe("system");
  });

  it("keeps a choice storage refuses until the page reloads", () => {
    const write = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("Blocked", "SecurityError");
    });
    storeTheme("dark");
    expect(readTheme()).toBe("dark");
    expect(html.classList.contains("dark")).toBe(true);

    // Storage works again: a later choice is stored and the kept one goes.
    write.mockRestore();
    storeTheme("system");
    expect(readTheme()).toBe("system");
  });

  it("reads an unknown stored value as system", () => {
    localStorage.setItem(themeKey, "sepia");
    expect(readTheme()).toBe("system");
  });
});
