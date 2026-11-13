import { useEffect, type RefObject } from "react";

// useFocusOnOpen focuses target, e.g. a page's heading, each time opened
// changes to something other than null: what was opened, e.g. a mail place.
// null leaves focus alone, e.g. while something inside takes it itself.
//
// DESIGN: a page opened from a drawer draws while the closing drawer still
// keeps the rest of the window inert, where nothing takes focus, so this
// waits until the window lets go. Focus taken then also keeps React Aria
// from giving focus back to the drawer's button, which it does only for
// focus left in the drawer or on the page.
export function useFocusOnOpen(target: RefObject<HTMLElement | null>, opened: unknown): void {
  useEffect(() => {
    if (opened === null) return undefined;
    let frame = 0;
    const focus = () => {
      const element = target.current;
      if (element?.closest("[inert]")) {
        frame = requestAnimationFrame(focus);
        return;
      }
      element?.focus();
    };
    focus();
    return () => cancelAnimationFrame(frame);
  }, [target, opened]);
}
