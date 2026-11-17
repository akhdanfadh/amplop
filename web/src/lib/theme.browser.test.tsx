import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { applyTheme, readTheme, storeTheme, themeKey, themeScript } from "./theme";

const html = document.documentElement;

afterEach(() => {
  html.classList.remove("dark");
  document.body.replaceChildren();
});

// frame waits for the next frame.
function frame(): Promise<number> {
  return new Promise((resolve) => requestAnimationFrame(resolve));
}

// betweenFrames, called in a frame, waits for the next ordinary task,
// which runs once that frame is drawn and before the next: where a
// click's handler runs.
function betweenFrames(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

// themed adds an element whose colours come from the theme and fade when
// they change, as a button's do.
function themed(): HTMLElement {
  const element = document.createElement("div");
  element.className = "bg-background text-foreground transition-colors";
  element.textContent = "Inbox";
  document.body.append(element);
  return element;
}

// runScript runs the root route's head script as the shell does.
function runScript() {
  // oxlint-disable-next-line typescript/no-implied-eval -- the script is our own constant.
  new Function(themeScript)();
}

describe("theme", () => {
  it("shows a stored dark or light choice before the app starts", () => {
    localStorage.setItem(themeKey, "dark");
    runScript();
    expect(html.classList.contains("dark")).toBe(true);

    localStorage.setItem(themeKey, "light");
    runScript();
    expect(html.classList.contains("dark")).toBe(false);
  });

  it("shows the system's choice the same before and after the app starts", () => {
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

  it("switches every colour at once, and fades again afterwards", async () => {
    const element = themed();
    await frame();
    await betweenFrames();
    applyTheme("dark");
    await frame();
    await frame();
    expect(element.getAnimations()).toHaveLength(0);
    expect(html.dataset.themeSwitching).toBeUndefined();

    // A colour change after the switch, e.g. a hover, still fades.
    element.classList.replace("bg-background", "bg-accent");
    await frame();
    expect(element.getAnimations().length).toBeGreaterThan(0);
  });

  it("finishes an overlay's closing animation at once", async () => {
    // React Aria removes a closing tooltip or menu when this finishes.
    const closing = document.createElement("div");
    closing.className = "bg-foreground animate-out fade-out-0 duration-200";
    document.body.append(closing);
    await frame();
    const [exit] = closing.getAnimations();
    let finished = false;
    void exit?.finished.then(() => {
      finished = true;
    });
    await betweenFrames();
    applyTheme("dark");
    await frame();
    expect(exit).toBeDefined();
    expect(finished).toBe(true);
  });
});
