// The theme a member picks in this browser (DESIGN.md "Themes"): light,
// dark, or the system's. The page is dark while <html> has the class dark.

const themes = ["light", "dark", "system"] as const;
export type Theme = (typeof themes)[number];

// The localStorage key that holds the choice.
export const themeKey = "inbok-theme";
// The window event that tells this tab's readers the choice changed.
const changeEvent = "inbok-theme-change";
const darkQuery = "(prefers-color-scheme: dark)";
// A choice storage refused to keep; it holds until the page reloads.
let unsaved: Theme | null = null;

// themeScript is the root route's head script. It marks the page dark
// before the first paint, as readTheme and applyTheme would; it stands
// alone because the shell's head loads nothing else first.
export const themeScript = `(function () {
  var theme = null;
  try {
    theme = localStorage.getItem(${JSON.stringify(themeKey)});
  } catch (error) {}
  var dark = theme === "dark" || (theme !== "light" && matchMedia(${JSON.stringify(darkQuery)}).matches);
  document.documentElement.classList.toggle("dark", dark);
})();`;

// readTheme returns the choice made in this page that storage refused,
// else the stored choice, else system.
export function readTheme(): Theme {
  if (unsaved) return unsaved;
  let stored: string | null = null;
  try {
    stored = localStorage.getItem(themeKey);
  } catch {
    // Storage blocked, e.g. by the browser's site data settings.
  }
  return themes.find((theme) => theme === stored) ?? "system";
}

// storeTheme saves theme for this browser, applies it, and tells this
// tab's readers. The page still changes when storage cannot be written.
export function storeTheme(theme: Theme): void {
  try {
    if (theme === "system") localStorage.removeItem(themeKey);
    else localStorage.setItem(themeKey, theme);
    unsaved = null;
  } catch {
    // Storage blocked, e.g. by the browser's site data settings.
    unsaved = theme;
  }
  applyTheme(theme);
  window.dispatchEvent(new Event(changeEvent));
}

// applyTheme marks the page dark or light for theme, with no fade from
// one to the other.
export function applyTheme(theme: Theme): void {
  const dark = theme === "dark" || (theme === "system" && matchMedia(darkQuery).matches);
  const root = document.documentElement;
  if (root.classList.contains("dark") === dark) return;

  // 1. Set every transition and animation to zero (src/styles.css) before
  // the colours change: otherwise an element with a colour transition
  // would fade from one theme to the other, and a closing tooltip would
  // flash in the other theme's colours.
  root.dataset.themeSwitching = "";

  // 2. Switch the colours, and have the browser work them out now, while
  // every duration is zero: they change at once, and an overlay closing
  // at this moment finishes.
  // NOTE: reading a computed style makes the browser restyle the page on
  // the spot, a "style change event", the moment transitions start from
  // (CSS Transitions, "Starting of transitions").
  root.classList.toggle("dark", dark);
  void getComputedStyle(root).color;

  // 3. Let transitions run again. The colours have already changed, so
  // nothing fades; the next hover does.
  delete root.dataset.themeSwitching;
}

// watchTheme keeps the page's theme current: it follows the system while
// the choice is system, and a choice made in another tab. It calls onChange
// after each change and returns a function that stops watching.
export function watchTheme(onChange: () => void = () => {}): () => void {
  const media = matchMedia(darkQuery);
  const follow = () => {
    applyTheme(readTheme());
    onChange();
  };
  const fromStorage = (event: StorageEvent) => {
    if (event.key === themeKey || event.key === null) follow();
  };
  media.addEventListener("change", follow);
  window.addEventListener("storage", fromStorage);
  window.addEventListener(changeEvent, onChange);
  return () => {
    media.removeEventListener("change", follow);
    window.removeEventListener("storage", fromStorage);
    window.removeEventListener(changeEvent, onChange);
  };
}
