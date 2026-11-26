import { useRef, type KeyboardEvent, type MouseEvent, type PointerEvent } from "react";
import { doubleClickTime } from "@/lib/double-click";

// PressGuard keeps a list's presses from acting for a moment: hold starts
// it; the handlers go on the list's box, in the capture phase.
export type PressGuard = {
  hold: () => void;
  onPointerDownCapture: (event: PointerEvent) => boolean;
  onClickCapture: (event: MouseEvent) => boolean;
  onKeyDownCapture: (event: KeyboardEvent) => boolean;
};

// usePressGuard returns a PressGuard that, for doubleClickTime after hold,
// drops every press in the list: a pointer going down, a click, and Enter
// or Space. Each handler reports whether it dropped its event.
//
// DESIGN: after a row's action, the row below slides under a still pointer,
// and a double press's second press would open it, or would press the same
// row's Mark again. Dropped on the list's box before any row, button, or
// React Aria press hears it; scrolling still works.
export function usePressGuard(): PressGuard {
  const until = useRef(0);
  const drop = (event: PointerEvent | MouseEvent | KeyboardEvent) => {
    if (performance.now() >= until.current) return false;
    event.preventDefault();
    event.stopPropagation();
    return true;
  };
  return {
    hold: () => {
      until.current = performance.now() + doubleClickTime;
    },
    onPointerDownCapture: drop,
    onClickCapture: drop,
    onKeyDownCapture: (event) => (event.key === "Enter" || event.key === " ") && drop(event),
  };
}
