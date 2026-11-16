// isShortcut reports whether event is Ctrl or Cmd with letter alone, e.g.
// Ctrl+B or Cmd+B for "b" and "KeyB": no Shift or Alt, not a held key's
// repeat, not a key another handler took, not typed into a text field, and
// not in a dialog or menu, but one inside ownDialog, a selector for the
// overlay the shortcut itself opens and closes, e.g. the sidebar's drawer.
// code is the physical key where QWERTY has letter.
export function isShortcut(
  event: KeyboardEvent,
  letter: string,
  code: string,
  ownDialog?: string,
): boolean {
  // The letter in any Latin layout, Caps Lock or not, so Dvorak's Ctrl+X
  // stays cut; in any other layout, e.g. Cyrillic, it is the key where
  // QWERTY has it.
  const latin = /^[a-z]$/i.test(event.key);
  if (latin ? event.key.toLowerCase() !== letter : event.code !== code) return false;
  if (event.repeat || event.defaultPrevented) return false;
  if (event.shiftKey || event.altKey || event.ctrlKey === event.metaKey) return false;
  // In a field the keys already mean something, e.g. Ctrl+B back one
  // character on a Mac, bold in an editor, Ctrl+Z undo typing.
  const target = event.target instanceof Element ? event.target : null;
  if (target?.closest("input, textarea, select, [contenteditable]")) return false;
  // An open popover, menu, or dialog has the member's attention; what
  // changes behind it would change out of sight. The overlay a shortcut
  // opens, e.g. the sidebar's drawer, the same shortcut still closes.
  const overlay = target?.closest("[role=dialog], [role=alertdialog], [role=menu]");
  return !overlay || (ownDialog !== undefined && overlay.closest(ownDialog) !== null);
}

// The key a shortcut holds down with a letter or Enter, as the platform's
// keyboard writes it.
export type ShortcutModifier = "Cmd" | "Ctrl";

// shortcutModifier returns the modifier key that shortcuts name on the
// platform userAgent describes, the browser's own by default: Cmd on Apple
// keyboards, Ctrl on the rest. Words, not the Command symbol, which no
// keyboard types (DESIGN.md "Wording").
export function shortcutModifier(userAgent: string = navigator.userAgent): ShortcutModifier {
  return /Mac|iPhone|iPad/.test(userAgent) ? "Cmd" : "Ctrl";
}

// shortcutName returns the words for the platform's modifier with letter,
// e.g. "Ctrl+B", or "Cmd+B" on an Apple keyboard.
export function shortcutName(letter: string): string {
  return `${shortcutModifier()}+${letter.toUpperCase()}`;
}
